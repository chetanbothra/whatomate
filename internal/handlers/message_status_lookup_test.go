package handlers_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/nikyjain/whatomate/internal/handlers"
	"github.com/nikyjain/whatomate/internal/models"
	"github.com/nikyjain/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// statusLookupFixture creates an org, account, contact and one outgoing message.
func statusLookupFixture(t *testing.T, app *handlers.App, status models.MessageStatus, errMsg string) (*models.Organization, *models.Contact, *models.Message) {
	t.Helper()
	uid := uuid.New().String()[:8]

	org := testutil.CreateTestOrganization(t, app.DB)
	account := createTestAccount(t, app, org.ID)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithContactAccount(account.Name))

	msg := &models.Message{
		BaseModel:         models.BaseModel{ID: uuid.New()},
		OrganizationID:    org.ID,
		WhatsAppAccount:   account.Name,
		ContactID:         contact.ID,
		WhatsAppMessageID: "wamid.lookup-" + uid,
		Direction:         models.DirectionOutgoing,
		MessageType:       models.MessageTypeTemplate,
		TemplateName:      "credentials_v1",
		Content:           "Hello",
		Status:            status,
		ErrorMessage:      errMsg,
	}
	require.NoError(t, app.DB.Create(msg).Error)

	return org, contact, msg
}

func TestGetMessageStatus_ByMessageUUID(t *testing.T) {
	mockServer := newMockWhatsAppServer()
	defer mockServer.close()
	app := newMsgTestApp(t, mockServer)

	org, contact, msg := statusLookupFixture(t, app, models.MessageStatusDelivered, "")
	user := testutil.CreateTestUser(t, app.DB, org.ID)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, user.ID)
	testutil.SetPathParam(req, "id", msg.ID.String())

	require.NoError(t, app.GetMessageStatus(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var got handlers.MessageStatusResponse
	testutil.ParseEnvelopeResponse(t, req, &got)

	assert.Equal(t, msg.ID.String(), got.MessageID)
	assert.Equal(t, msg.WhatsAppMessageID, got.WhatsAppMessageID)
	assert.Equal(t, "delivered", got.Status)
	assert.Equal(t, contact.ID.String(), got.ContactID)
	assert.Equal(t, contact.PhoneNumber, got.ContactPhone)
	assert.Equal(t, "credentials_v1", got.TemplateName)
	assert.Equal(t, "outgoing", got.Direction)
	assert.Empty(t, got.ErrorMessage)
	assert.False(t, got.CreatedAt.IsZero())
}

func TestGetMessageStatus_ByWhatsAppMessageID(t *testing.T) {
	mockServer := newMockWhatsAppServer()
	defer mockServer.close()
	app := newMsgTestApp(t, mockServer)

	org, _, msg := statusLookupFixture(t, app, models.MessageStatusRead, "")
	user := testutil.CreateTestUser(t, app.DB, org.ID)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, user.ID)
	testutil.SetPathParam(req, "id", msg.WhatsAppMessageID)

	require.NoError(t, app.GetMessageStatus(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var got handlers.MessageStatusResponse
	testutil.ParseEnvelopeResponse(t, req, &got)
	assert.Equal(t, msg.ID.String(), got.MessageID)
	assert.Equal(t, "read", got.Status)
}

func TestGetMessageStatus_FailedIncludesErrorMessage(t *testing.T) {
	mockServer := newMockWhatsAppServer()
	defer mockServer.close()
	app := newMsgTestApp(t, mockServer)

	org, _, msg := statusLookupFixture(t, app, models.MessageStatusFailed, "Template is paused")
	user := testutil.CreateTestUser(t, app.DB, org.ID)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, user.ID)
	testutil.SetPathParam(req, "id", msg.ID.String())

	require.NoError(t, app.GetMessageStatus(req))

	var got handlers.MessageStatusResponse
	testutil.ParseEnvelopeResponse(t, req, &got)
	assert.Equal(t, "failed", got.Status)
	assert.Equal(t, "Template is paused", got.ErrorMessage)
}

func TestGetMessageStatus_UnknownIDReturns404(t *testing.T) {
	mockServer := newMockWhatsAppServer()
	defer mockServer.close()
	app := newMsgTestApp(t, mockServer)

	org := testutil.CreateTestOrganization(t, app.DB)
	user := testutil.CreateTestUser(t, app.DB, org.ID)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, user.ID)
	testutil.SetPathParam(req, "id", "wamid.does-not-exist")

	require.NoError(t, app.GetMessageStatus(req))
	assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req))
}

func TestGetMessageStatus_OtherOrgMessageIsNotVisible(t *testing.T) {
	mockServer := newMockWhatsAppServer()
	defer mockServer.close()
	app := newMsgTestApp(t, mockServer)

	_, _, msg := statusLookupFixture(t, app, models.MessageStatusSent, "")

	otherOrg := testutil.CreateTestOrganization(t, app.DB)
	otherUser := testutil.CreateTestUser(t, app.DB, otherOrg.ID)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, otherOrg.ID, otherUser.ID)
	testutil.SetPathParam(req, "id", msg.ID.String())

	require.NoError(t, app.GetMessageStatus(req))
	assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req))
}

func TestBatchMessageStatus_MixedIDsAndNotFound(t *testing.T) {
	mockServer := newMockWhatsAppServer()
	defer mockServer.close()
	app := newMsgTestApp(t, mockServer)

	org, _, first := statusLookupFixture(t, app, models.MessageStatusSent, "")
	user := testutil.CreateTestUser(t, app.DB, org.ID)

	second := &models.Message{
		BaseModel:         models.BaseModel{ID: uuid.New()},
		OrganizationID:    org.ID,
		WhatsAppAccount:   first.WhatsAppAccount,
		ContactID:         first.ContactID,
		WhatsAppMessageID: "wamid.batch-" + uuid.New().String()[:8],
		Direction:         models.DirectionOutgoing,
		MessageType:       models.MessageTypeTemplate,
		Status:            models.MessageStatusRead,
	}
	require.NoError(t, app.DB.Create(second).Error)

	req := testutil.NewJSONRequest(t, map[string]any{
		"message_ids":          []string{first.ID.String(), uuid.New().String()},
		"whatsapp_message_ids": []string{second.WhatsAppMessageID, "wamid.missing"},
	})
	testutil.SetAuthContext(req, org.ID, user.ID)

	require.NoError(t, app.BatchMessageStatus(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var got struct {
		Statuses []handlers.MessageStatusResponse `json:"statuses"`
		NotFound []string                         `json:"not_found"`
	}
	testutil.ParseEnvelopeResponse(t, req, &got)

	require.Len(t, got.Statuses, 2)
	assert.Equal(t, first.ID.String(), got.Statuses[0].MessageID)
	assert.Equal(t, "sent", got.Statuses[0].Status)
	assert.Equal(t, second.ID.String(), got.Statuses[1].MessageID)
	assert.Equal(t, "read", got.Statuses[1].Status)
	assert.Len(t, got.NotFound, 2)
}

func TestBatchMessageStatus_EmptyRequestRejected(t *testing.T) {
	mockServer := newMockWhatsAppServer()
	defer mockServer.close()
	app := newMsgTestApp(t, mockServer)

	org := testutil.CreateTestOrganization(t, app.DB)
	user := testutil.CreateTestUser(t, app.DB, org.ID)

	req := testutil.NewJSONRequest(t, map[string]any{"message_ids": []string{" ", ""}})
	testutil.SetAuthContext(req, org.ID, user.ID)

	require.NoError(t, app.BatchMessageStatus(req))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req))
}

func TestBatchMessageStatus_TooManyIDsRejected(t *testing.T) {
	mockServer := newMockWhatsAppServer()
	defer mockServer.close()
	app := newMsgTestApp(t, mockServer)

	org := testutil.CreateTestOrganization(t, app.DB)
	user := testutil.CreateTestUser(t, app.DB, org.ID)

	ids := make([]string, 101)
	for i := range ids {
		ids[i] = uuid.New().String()
	}

	req := testutil.NewJSONRequest(t, map[string]any{"message_ids": ids})
	testutil.SetAuthContext(req, org.ID, user.ID)

	require.NoError(t, app.BatchMessageStatus(req))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req))
}

func TestBatchMessageStatus_DuplicateIDsReturnedOnce(t *testing.T) {
	mockServer := newMockWhatsAppServer()
	defer mockServer.close()
	app := newMsgTestApp(t, mockServer)

	org, _, msg := statusLookupFixture(t, app, models.MessageStatusDelivered, "")
	user := testutil.CreateTestUser(t, app.DB, org.ID)

	req := testutil.NewJSONRequest(t, map[string]any{
		"message_ids": []string{msg.ID.String(), msg.ID.String()},
	})
	testutil.SetAuthContext(req, org.ID, user.ID)

	require.NoError(t, app.BatchMessageStatus(req))

	var got struct {
		Statuses []handlers.MessageStatusResponse `json:"statuses"`
		NotFound []string                         `json:"not_found"`
	}
	testutil.ParseEnvelopeResponse(t, req, &got)
	require.Len(t, got.Statuses, 1)
	assert.Empty(t, got.NotFound)
}

func TestBatchMessageStatus_OtherOrgMessagesReportedNotFound(t *testing.T) {
	mockServer := newMockWhatsAppServer()
	defer mockServer.close()
	app := newMsgTestApp(t, mockServer)

	_, _, msg := statusLookupFixture(t, app, models.MessageStatusSent, "")

	otherOrg := testutil.CreateTestOrganization(t, app.DB)
	otherUser := testutil.CreateTestUser(t, app.DB, otherOrg.ID)

	req := testutil.NewJSONRequest(t, map[string]any{"message_ids": []string{msg.ID.String()}})
	testutil.SetAuthContext(req, otherOrg.ID, otherUser.ID)

	require.NoError(t, app.BatchMessageStatus(req))

	var got struct {
		Statuses []handlers.MessageStatusResponse `json:"statuses"`
		NotFound []string                         `json:"not_found"`
	}
	testutil.ParseEnvelopeResponse(t, req, &got)
	assert.Empty(t, got.Statuses)
	require.Len(t, got.NotFound, 1)
	assert.Equal(t, msg.ID.String(), got.NotFound[0])
}
