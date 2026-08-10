package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/nikyjain/whatomate/internal/handlers"
	"github.com/nikyjain/whatomate/internal/models"
	"github.com/nikyjain/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// callbackSink records status callbacks posted by Whatomate.
type callbackSink struct {
	mu       sync.Mutex
	server   *httptest.Server
	payloads []map[string]any
	auths    []string
}

func newCallbackSink(t *testing.T) *callbackSink {
	t.Helper()
	sink := &callbackSink{}
	sink.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)

		sink.mu.Lock()
		sink.payloads = append(sink.payloads, payload)
		sink.auths = append(sink.auths, r.Header.Get("Authorization"))
		sink.mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(sink.server.Close)
	return sink
}

func (s *callbackSink) snapshot() ([]map[string]any, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	payloads := make([]map[string]any, len(s.payloads))
	copy(payloads, s.payloads)
	auths := make([]string, len(s.auths))
	copy(auths, s.auths)
	return payloads, auths
}

// newStatusCallbackTemplate creates an approved template for status callback tests.
func newStatusCallbackTemplate(t *testing.T, app *handlers.App, orgID uuid.UUID, accountName string) *models.Template {
	t.Helper()
	tpl := &models.Template{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  orgID,
		WhatsAppAccount: accountName,
		Name:            "status_cb_tpl_" + uuid.New().String()[:8],
		DisplayName:     "Status Callback Template",
		Language:        "en",
		Status:          string(models.TemplateStatusApproved),
		BodyContent:     "Hello {{name}}",
	}
	require.NoError(t, app.DB.Create(tpl).Error)
	return tpl
}

func TestSendTemplateMessage_StatusCallback_ReportsSent(t *testing.T) {
	mockServer := newMockWhatsAppServer()
	defer mockServer.close()

	app := newMsgTestApp(t, mockServer)
	sink := newCallbackSink(t)

	org := testutil.CreateTestOrganization(t, app.DB)
	adminRole := testutil.CreateAdminRole(t, app.DB, org.ID)
	user := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&adminRole.ID))
	account := createTestAccount(t, app, org.ID)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithContactAccount(account.Name))
	tpl := newStatusCallbackTemplate(t, app, org.ID, account.Name)

	req := testutil.NewJSONRequest(t, map[string]interface{}{
		"contact_id":                   contact.ID.String(),
		"template_name":                tpl.Name,
		"template_params":              map[string]string{"name": "Nitin Jain"},
		"status_callback_url":          sink.server.URL,
		"status_callback_bearer_token": "status-secret",
	})
	testutil.SetAuthContext(req, org.ID, user.ID)

	require.NoError(t, app.SendTemplateMessage(req))
	assert.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	app.WaitForBackgroundTasks()

	payloads, auths := sink.snapshot()
	require.Len(t, payloads, 1)
	assert.Equal(t, "sent", payloads[0]["status"])
	assert.Equal(t, mockServer.nextMessageID, payloads[0]["whatsapp_message_id"])
	assert.Equal(t, contact.ID.String(), payloads[0]["contact_id"])
	assert.Equal(t, contact.PhoneNumber, payloads[0]["contact_phone"])
	assert.Equal(t, tpl.Name, payloads[0]["template_name"])
	assert.NotEmpty(t, payloads[0]["message_id"])
	assert.Equal(t, []string{"Bearer status-secret"}, auths)

	// Callback config must be persisted so later Meta status updates can reuse it
	var dbMsg models.Message
	require.NoError(t, app.DB.Where("contact_id = ?", contact.ID).First(&dbMsg).Error)
	assert.Equal(t, sink.server.URL, dbMsg.Metadata["status_callback_url"])
	assert.Equal(t, "status-secret", dbMsg.Metadata["status_callback_bearer_token"])
}

func TestSendTemplateMessage_StatusCallback_ReportsFailed(t *testing.T) {
	mockServer := newMockWhatsAppServer()
	defer mockServer.close()
	mockServer.returnError = true
	mockServer.errorMessage = "Template is paused"

	app := newMsgTestApp(t, mockServer)
	sink := newCallbackSink(t)

	org := testutil.CreateTestOrganization(t, app.DB)
	adminRole := testutil.CreateAdminRole(t, app.DB, org.ID)
	user := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&adminRole.ID))
	account := createTestAccount(t, app, org.ID)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithContactAccount(account.Name))
	tpl := newStatusCallbackTemplate(t, app, org.ID, account.Name)

	req := testutil.NewJSONRequest(t, map[string]interface{}{
		"contact_id":          contact.ID.String(),
		"template_name":       tpl.Name,
		"template_params":     map[string]string{"name": "Nitin Jain"},
		"status_callback_url": sink.server.URL,
	})
	testutil.SetAuthContext(req, org.ID, user.ID)

	require.NoError(t, app.SendTemplateMessage(req))
	// API still accepts the request; the failure is reported through the callback
	assert.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	app.WaitForBackgroundTasks()

	payloads, auths := sink.snapshot()
	require.Len(t, payloads, 1)
	assert.Equal(t, "failed", payloads[0]["status"])
	assert.NotEmpty(t, payloads[0]["error_message"])
	assert.Equal(t, []string{""}, auths)

	var dbMsg models.Message
	require.NoError(t, app.DB.Where("contact_id = ?", contact.ID).First(&dbMsg).Error)
	assert.Equal(t, models.MessageStatusFailed, dbMsg.Status)
}

func TestSendTemplateMessage_StatusCallback_NotConfiguredSendsNothing(t *testing.T) {
	mockServer := newMockWhatsAppServer()
	defer mockServer.close()

	app := newMsgTestApp(t, mockServer)
	sink := newCallbackSink(t)

	org := testutil.CreateTestOrganization(t, app.DB)
	adminRole := testutil.CreateAdminRole(t, app.DB, org.ID)
	user := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&adminRole.ID))
	account := createTestAccount(t, app, org.ID)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithContactAccount(account.Name))
	tpl := newStatusCallbackTemplate(t, app, org.ID, account.Name)

	req := testutil.NewJSONRequest(t, map[string]interface{}{
		"contact_id":      contact.ID.String(),
		"template_name":   tpl.Name,
		"template_params": map[string]string{"name": "Nitin Jain"},
	})
	testutil.SetAuthContext(req, org.ID, user.ID)

	require.NoError(t, app.SendTemplateMessage(req))
	assert.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	app.WaitForBackgroundTasks()

	payloads, _ := sink.snapshot()
	assert.Empty(t, payloads)
}

func TestSendTemplateMessage_StatusCallback_RejectsInvalidURL(t *testing.T) {
	mockServer := newMockWhatsAppServer()
	defer mockServer.close()

	app := newMsgTestApp(t, mockServer)

	org := testutil.CreateTestOrganization(t, app.DB)
	adminRole := testutil.CreateAdminRole(t, app.DB, org.ID)
	user := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&adminRole.ID))
	account := createTestAccount(t, app, org.ID)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithContactAccount(account.Name))
	tpl := newStatusCallbackTemplate(t, app, org.ID, account.Name)

	req := testutil.NewJSONRequest(t, map[string]interface{}{
		"contact_id":          contact.ID.String(),
		"template_name":       tpl.Name,
		"status_callback_url": "billing.example.com/status",
	})
	testutil.SetAuthContext(req, org.ID, user.ID)

	require.NoError(t, app.SendTemplateMessage(req))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req))

	app.WaitForBackgroundTasks()
	assert.Empty(t, mockServer.sentMessages)
}
