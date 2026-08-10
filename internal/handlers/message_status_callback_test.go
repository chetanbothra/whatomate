package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nikyjain/whatomate/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// statusCallbackRecorder collects the payloads posted to a status callback URL.
type statusCallbackRecorder struct {
	mu       sync.Mutex
	server   *httptest.Server
	payloads []messageStatusCallbackPayload
	auths    []string
	respond  int
}

func newStatusCallbackRecorder(t *testing.T) *statusCallbackRecorder {
	t.Helper()
	rec := &statusCallbackRecorder{respond: http.StatusOK}
	rec.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload messageStatusCallbackPayload
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))

		rec.mu.Lock()
		rec.payloads = append(rec.payloads, payload)
		rec.auths = append(rec.auths, r.Header.Get("Authorization"))
		code := rec.respond
		rec.mu.Unlock()

		w.WriteHeader(code)
	}))
	t.Cleanup(rec.server.Close)
	return rec
}

func (r *statusCallbackRecorder) received() []messageStatusCallbackPayload {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]messageStatusCallbackPayload, len(r.payloads))
	copy(out, r.payloads)
	return out
}

func (r *statusCallbackRecorder) authHeaders() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.auths))
	copy(out, r.auths)
	return out
}

// statusCallbackFixture creates an org, account, contact and outgoing template
// message carrying the given status callback metadata.
func statusCallbackFixture(t *testing.T, app *App, callbackURL, token string) (*models.Organization, *models.Contact, *models.Message) {
	t.Helper()
	uid := uuid.New().String()[:8]

	org := models.Organization{
		BaseModel: models.BaseModel{ID: uuid.New()},
		Name:      "status-cb-" + uid,
		Slug:      "status-cb-" + uid,
	}
	require.NoError(t, app.DB.Create(&org).Error)

	account := models.WhatsAppAccount{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: org.ID,
		Name:           "status-cb-account-" + uid,
		PhoneID:        "phone-" + uid,
		AccessToken:    "token",
	}
	require.NoError(t, app.DB.Create(&account).Error)

	contact := models.Contact{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: org.ID,
		PhoneNumber:    "9199001" + uid,
		ProfileName:    "Status User",
	}
	require.NoError(t, app.DB.Create(&contact).Error)

	metadata := models.JSONB{}
	if callbackURL != "" {
		metadata[metaKeyStatusCallbackURL] = callbackURL
	}
	if token != "" {
		metadata[metaKeyStatusCallbackToken] = token
	}

	msg := models.Message{
		BaseModel:         models.BaseModel{ID: uuid.New()},
		OrganizationID:    org.ID,
		WhatsAppAccount:   account.Name,
		ContactID:         contact.ID,
		WhatsAppMessageID: "wamid.status-" + uid,
		Direction:         models.DirectionOutgoing,
		MessageType:       models.MessageTypeTemplate,
		TemplateName:      "credentials_v1",
		Content:           "Hello",
		Status:            models.MessageStatusSent,
		Metadata:          metadata,
	}
	require.NoError(t, app.DB.Create(&msg).Error)

	return &org, &contact, &msg
}

func TestValidateCallbackURL(t *testing.T) {
	assert.NoError(t, validateCallbackURL("status_callback_url", ""))
	assert.NoError(t, validateCallbackURL("status_callback_url", "https://billing.example.com/hook"))
	assert.NoError(t, validateCallbackURL("status_callback_url", "http://localhost:9000/hook"))

	assert.Error(t, validateCallbackURL("status_callback_url", "billing.example.com/hook"))
	assert.Error(t, validateCallbackURL("status_callback_url", "ftp://billing.example.com/hook"))
	assert.Error(t, validateCallbackURL("status_callback_url", "file:///etc/passwd"))
	assert.Error(t, validateCallbackURL("status_callback_url", "https://"))
}

func TestDispatchMessageStatusCallback_PostsPayloadWithToken(t *testing.T) {
	app := webhookTestApp(t)
	rec := newStatusCallbackRecorder(t)

	_, contact, msg := statusCallbackFixture(t, app, rec.server.URL, "status-secret")

	app.dispatchMessageStatusCallback(msg, contact, models.MessageStatusSent, "wamid.override", "")
	app.WaitForBackgroundTasks()

	received := rec.received()
	require.Len(t, received, 1)
	assert.Equal(t, msg.ID.String(), received[0].MessageID)
	assert.Equal(t, "wamid.override", received[0].WhatsAppMessageID)
	assert.Equal(t, "sent", received[0].Status)
	assert.Equal(t, contact.ID.String(), received[0].ContactID)
	assert.Equal(t, contact.PhoneNumber, received[0].ContactPhone)
	assert.Equal(t, "Status User", received[0].ContactName)
	assert.Equal(t, "credentials_v1", received[0].TemplateName)
	assert.Empty(t, received[0].ErrorMessage)
	assert.False(t, received[0].Timestamp.IsZero())

	assert.Equal(t, []string{"Bearer status-secret"}, rec.authHeaders())
}

func TestDispatchMessageStatusCallback_NoURLIsNoOp(t *testing.T) {
	app := webhookTestApp(t)
	rec := newStatusCallbackRecorder(t)

	_, contact, msg := statusCallbackFixture(t, app, "", "")

	app.dispatchMessageStatusCallback(msg, contact, models.MessageStatusSent, "", "")
	app.WaitForBackgroundTasks()

	assert.Empty(t, rec.received())
}

func TestDispatchMessageStatusCallback_OmitsTokenWhenNotConfigured(t *testing.T) {
	app := webhookTestApp(t)
	rec := newStatusCallbackRecorder(t)

	_, contact, msg := statusCallbackFixture(t, app, rec.server.URL, "")

	app.dispatchMessageStatusCallback(msg, contact, models.MessageStatusFailed, "", "template paused")
	app.WaitForBackgroundTasks()

	received := rec.received()
	require.Len(t, received, 1)
	assert.Equal(t, "failed", received[0].Status)
	assert.Equal(t, "template paused", received[0].ErrorMessage)
	assert.Equal(t, []string{""}, rec.authHeaders())
}

func TestDispatchMessageStatusCallback_FallsBackToStoredWAMID(t *testing.T) {
	app := webhookTestApp(t)
	rec := newStatusCallbackRecorder(t)

	_, contact, msg := statusCallbackFixture(t, app, rec.server.URL, "")

	app.dispatchMessageStatusCallback(msg, contact, models.MessageStatusDelivered, "", "")
	app.WaitForBackgroundTasks()

	received := rec.received()
	require.Len(t, received, 1)
	assert.Equal(t, msg.WhatsAppMessageID, received[0].WhatsAppMessageID)
}

func TestDispatchMessageStatusCallback_ReceiverErrorDoesNotPanic(t *testing.T) {
	app := webhookTestApp(t)
	rec := newStatusCallbackRecorder(t)
	rec.respond = http.StatusInternalServerError

	_, contact, msg := statusCallbackFixture(t, app, rec.server.URL, "")

	app.dispatchMessageStatusCallback(msg, contact, models.MessageStatusRead, "", "")
	app.WaitForBackgroundTasks()

	require.Len(t, rec.received(), 1)
}

func TestUpdateMessageStatus_FiresStatusCallbackForDeliveredReadFailed(t *testing.T) {
	cases := []struct {
		name       string
		metaStatus string
		expected   string
	}{
		{name: "delivered", metaStatus: "delivered", expected: "delivered"},
		{name: "read", metaStatus: "read", expected: "read"},
		{name: "failed", metaStatus: "failed", expected: "failed"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := webhookTestApp(t)
			rec := newStatusCallbackRecorder(t)

			_, contact, msg := statusCallbackFixture(t, app, rec.server.URL, "tok")

			app.updateMessageStatus(msg.WhatsAppMessageID, tc.metaStatus, nil)
			app.WaitForBackgroundTasks()

			received := rec.received()
			require.Len(t, received, 1)
			assert.Equal(t, tc.expected, received[0].Status)
			assert.Equal(t, msg.WhatsAppMessageID, received[0].WhatsAppMessageID)
			assert.Equal(t, contact.PhoneNumber, received[0].ContactPhone)

			var updated models.Message
			require.NoError(t, app.DB.First(&updated, msg.ID).Error)
			assert.Equal(t, models.MessageStatus(tc.expected), updated.Status)
		})
	}
}

func TestUpdateMessageStatus_FailedCallbackCarriesMetaError(t *testing.T) {
	app := webhookTestApp(t)
	rec := newStatusCallbackRecorder(t)

	_, _, msg := statusCallbackFixture(t, app, rec.server.URL, "")

	var metaErr WebhookStatusError
	metaErr.Title = "Message undeliverable"
	metaErr.Message = "Message undeliverable"
	metaErr.ErrorData.Details = "Message failed to send because more than 24 hours have passed"

	app.updateMessageStatus(msg.WhatsAppMessageID, "failed", []WebhookStatusError{metaErr})
	app.WaitForBackgroundTasks()

	received := rec.received()
	require.Len(t, received, 1)
	assert.Equal(t, "failed", received[0].Status)
	assert.Equal(t, "Message failed to send because more than 24 hours have passed", received[0].ErrorMessage)
}

func TestUpdateMessageStatus_RepeatedFailedFiresCallbackOnce(t *testing.T) {
	app := webhookTestApp(t)
	rec := newStatusCallbackRecorder(t)

	_, _, msg := statusCallbackFixture(t, app, rec.server.URL, "")

	// Meta redelivers status webhooks; a repeated "failed" must not re-notify.
	app.updateMessageStatus(msg.WhatsAppMessageID, "failed", nil)
	app.updateMessageStatus(msg.WhatsAppMessageID, "failed", nil)
	app.WaitForBackgroundTasks()

	received := rec.received()
	require.Len(t, received, 1)
	assert.Equal(t, "failed", received[0].Status)
}

func TestUpdateMessageStatus_RepeatedDeliveredFiresCallbackOnce(t *testing.T) {
	app := webhookTestApp(t)
	rec := newStatusCallbackRecorder(t)

	_, _, msg := statusCallbackFixture(t, app, rec.server.URL, "")

	app.updateMessageStatus(msg.WhatsAppMessageID, "delivered", nil)
	app.updateMessageStatus(msg.WhatsAppMessageID, "delivered", nil)
	app.WaitForBackgroundTasks()

	require.Len(t, rec.received(), 1)
}

func TestDispatchMessageStatusCallback_TimestampIsUTC(t *testing.T) {
	app := webhookTestApp(t)
	rec := newStatusCallbackRecorder(t)

	_, contact, msg := statusCallbackFixture(t, app, rec.server.URL, "")

	app.dispatchMessageStatusCallback(msg, contact, models.MessageStatusSent, "", "")
	app.WaitForBackgroundTasks()

	received := rec.received()
	require.Len(t, received, 1)
	assert.Equal(t, time.UTC, received[0].Timestamp.Location())
}

func TestUpdateMessageStatus_DoesNotRepeatSentCallback(t *testing.T) {
	app := webhookTestApp(t)
	rec := newStatusCallbackRecorder(t)

	// Message is already "sent"; Meta's own "sent" status must not re-fire.
	_, _, msg := statusCallbackFixture(t, app, rec.server.URL, "")

	app.updateMessageStatus(msg.WhatsAppMessageID, "sent", nil)
	app.WaitForBackgroundTasks()

	assert.Empty(t, rec.received())
}

func TestCreateOutgoingMessage_StoresStatusCallbackMetadata(t *testing.T) {
	app := webhookTestApp(t)
	uid := uuid.New().String()[:8]

	org := models.Organization{
		BaseModel: models.BaseModel{ID: uuid.New()},
		Name:      "meta-org-" + uid,
		Slug:      "meta-org-" + uid,
	}
	require.NoError(t, app.DB.Create(&org).Error)

	account := &models.WhatsAppAccount{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: org.ID,
		Name:           "meta-account-" + uid,
	}
	contact := &models.Contact{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: org.ID,
		PhoneNumber:    "919911" + uid,
	}

	msg := app.createOutgoingMessage(OutgoingMessageRequest{
		Account:                   account,
		Contact:                   contact,
		Type:                      models.MessageTypeText,
		Content:                   "hello",
		StatusCallbackURL:         "  https://billing.example.com/status  ",
		StatusCallbackBearerToken: "secret",
	}, MessageSendOptions{})

	require.NotNil(t, msg.Metadata)
	assert.Equal(t, "https://billing.example.com/status", msg.Metadata[metaKeyStatusCallbackURL])
	assert.Equal(t, "secret", msg.Metadata[metaKeyStatusCallbackToken])
}

func TestCreateOutgoingMessage_NoStatusCallbackKeepsMetadataClean(t *testing.T) {
	app := webhookTestApp(t)

	account := &models.WhatsAppAccount{BaseModel: models.BaseModel{ID: uuid.New()}, Name: "acct"}
	contact := &models.Contact{BaseModel: models.BaseModel{ID: uuid.New()}, PhoneNumber: "919999999999"}

	msg := app.createOutgoingMessage(OutgoingMessageRequest{
		Account: account,
		Contact: contact,
		Type:    models.MessageTypeText,
		Content: "hello",
	}, MessageSendOptions{})

	_, hasURL := msg.Metadata[metaKeyStatusCallbackURL]
	assert.False(t, hasURL)
}
