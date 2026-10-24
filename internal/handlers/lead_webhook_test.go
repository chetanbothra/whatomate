package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nikyjain/whatomate/internal/config"
	"github.com/nikyjain/whatomate/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
	"github.com/zerodha/logf"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestApp(t *testing.T) (*App, *models.Organization, *models.LeadSource) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	err = db.AutoMigrate(&models.Organization{}, &models.LeadSource{}, &models.Lead{}, &models.LeadFormMapping{})
	require.NoError(t, err)

	org := &models.Organization{
		BaseModel: models.BaseModel{ID: uuid.New()},
		Name:      "Test Org",
	}
	require.NoError(t, db.Create(org).Error)

	source := &models.LeadSource{
		BaseModel:          models.BaseModel{ID: uuid.New()},
		OrganizationID:     org.ID,
		Name:               "Test Page",
		PageID:             "123456789",
		AppSecret:          "secret123",
		PageAccessToken:    "token123",
		WebhookVerifyToken: "verify_token_abc",
		IsActive:           true,
	}
	require.NoError(t, db.Create(source).Error)

	cfg := &config.Config{
		WhatsApp: config.WhatsAppConfig{
			WebhookVerifyToken: "global_token_xyz",
		},
		App: config.AppConfig{
			EncryptionKey: "01234567890123456789012345678901",
		},
	}

	app := &App{
		DB:     db,
		Config: cfg,
		Log:    logf.New(logf.Opts{}),
	}

	return app, org, source
}

func TestLeadWebhookVerify(t *testing.T) {
	app, _, source := setupTestApp(t)

	// 1. Verify with global token
	ctx := &fasthttp.RequestCtx{}
	ctx.QueryArgs().Set("hub.mode", "subscribe")
	ctx.QueryArgs().Set("hub.verify_token", "global_token_xyz")
	ctx.QueryArgs().Set("hub.challenge", "challenge_123")
	req := fastglue.NewRequest(ctx)

	err := app.LeadWebhookVerify(req)
	assert.NoError(t, err)
	assert.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
	assert.Equal(t, "challenge_123", string(ctx.Response.Body()))

	// 2. Verify with lead source token
	ctx2 := &fasthttp.RequestCtx{}
	ctx2.QueryArgs().Set("hub.mode", "subscribe")
	ctx2.QueryArgs().Set("hub.verify_token", source.WebhookVerifyToken)
	ctx2.QueryArgs().Set("hub.challenge", "challenge_source")
	req2 := fastglue.NewRequest(ctx2)

	err = app.LeadWebhookVerify(req2)
	assert.NoError(t, err)
	assert.Equal(t, fasthttp.StatusOK, ctx2.Response.StatusCode())
	assert.Equal(t, "challenge_source", string(ctx2.Response.Body()))

	// 3. Invalid token returns 403
	ctx3 := &fasthttp.RequestCtx{}
	ctx3.QueryArgs().Set("hub.mode", "subscribe")
	ctx3.QueryArgs().Set("hub.verify_token", "wrong_token")
	req3 := fastglue.NewRequest(ctx3)

	_ = app.LeadWebhookVerify(req3)
	assert.Equal(t, fasthttp.StatusForbidden, ctx3.Response.StatusCode())
}

func TestLeadWebhookHandler(t *testing.T) {
	app, org, source := setupTestApp(t)

	body := []byte(fmt.Sprintf(`{
		"object": "page",
		"entry": [{
			"id": "%s",
			"time": %d,
			"changes": [{
				"field": "leadgen",
				"value": {
					"leadgen_id": "lead_999",
					"page_id": "%s",
					"form_id": "form_123",
					"ad_id": "ad_456",
					"created_time": 1729780000
				}
			}]
		}]
	}`, source.PageID, time.Now().Unix(), source.PageID))

	// Compute HMAC signature with AppSecret
	mac := hmac.New(sha256.New, []byte(source.AppSecret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	// 1. Success valid signature
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetBody(body)
	ctx.Request.Header.Set("X-Hub-Signature-256", sig)
	req := fastglue.NewRequest(ctx)

	err := app.LeadWebhookHandler(req)
	assert.NoError(t, err)
	assert.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())

	// Check DB row inserted
	var lead models.Lead
	err = app.DB.Where("leadgen_id = ?", "lead_999").First(&lead).Error
	require.NoError(t, err)
	assert.Equal(t, org.ID, lead.OrganizationID)
	assert.Equal(t, models.StatusLeadReceived, lead.Status)
	assert.Equal(t, "form_123", lead.FormID)

	// 2. Duplicate webhook POST should not duplicate row
	ctxDup := &fasthttp.RequestCtx{}
	ctxDup.Request.SetBody(body)
	ctxDup.Request.Header.Set("X-Hub-Signature-256", sig)
	reqDup := fastglue.NewRequest(ctxDup)

	err = app.LeadWebhookHandler(reqDup)
	assert.NoError(t, err)

	var count int64
	app.DB.Model(&models.Lead{}).Where("leadgen_id = ?", "lead_999").Count(&count)
	assert.Equal(t, int64(1), count)

	// 3. Bad signature returns 403
	ctxBadSig := &fasthttp.RequestCtx{}
	ctxBadSig.Request.SetBody(body)
	ctxBadSig.Request.Header.Set("X-Hub-Signature-256", "sha256=invalid")
	reqBadSig := fastglue.NewRequest(ctxBadSig)

	_ = app.LeadWebhookHandler(reqBadSig)
	assert.Equal(t, fasthttp.StatusForbidden, ctxBadSig.Response.StatusCode())

	// 4. Non-page object returns 400
	ctxNonPage := &fasthttp.RequestCtx{}
	ctxNonPage.Request.SetBody([]byte(`{"object":"user"}`))
	reqNonPage := fastglue.NewRequest(ctxNonPage)

	_ = app.LeadWebhookHandler(reqNonPage)
	assert.Equal(t, fasthttp.StatusBadRequest, ctxNonPage.Response.StatusCode())
}
