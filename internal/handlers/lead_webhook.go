package handlers

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/nikyjain/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
	"gorm.io/gorm/clause"
)

// LeadgenWebhook represents Meta's leadgen webhook payload structure
type LeadgenWebhook struct {
	Object string `json:"object"` // must be "page"
	Entry  []struct {
		ID      string `json:"id"` // page id
		Time    int64  `json:"time"`
		Changes []struct {
			Field string `json:"field"` // "leadgen"
			Value struct {
				LeadgenID   string `json:"leadgen_id"`
				PageID      string `json:"page_id"`
				FormID      string `json:"form_id"`
				AdID        string `json:"ad_id"`
				AdgroupID   string `json:"adgroup_id"`
				CreatedTime int64  `json:"created_time"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

// LeadWebhookVerify handles GET /api/webhook/leadgen
func (a *App) LeadWebhookVerify(r *fastglue.Request) error {
	mode := string(r.RequestCtx.QueryArgs().Peek("hub.mode"))
	token := string(r.RequestCtx.QueryArgs().Peek("hub.verify_token"))
	challenge := string(r.RequestCtx.QueryArgs().Peek("hub.challenge"))

	if mode != "subscribe" {
		a.Log.Warn("Lead webhook verification failed - invalid mode", "mode", mode)
		return r.SendErrorEnvelope(fasthttp.StatusForbidden, "Verification failed", nil, "")
	}

	// 1. Check against global config token
	if token == a.Config.WhatsApp.WebhookVerifyToken && token != "" {
		a.Log.Info("Lead webhook verified successfully (global token)")
		r.RequestCtx.SetStatusCode(fasthttp.StatusOK)
		r.RequestCtx.SetBodyString(challenge)
		return nil
	}

	// 2. Check against tokens configured in LeadSource
	var source models.LeadSource
	if err := a.DB.Where("webhook_verify_token = ?", token).First(&source).Error; err == nil {
		a.Log.Info("Lead webhook verified successfully (lead source token)", "source_name", source.Name, "page_id", source.PageID)
		r.RequestCtx.SetStatusCode(fasthttp.StatusOK)
		r.RequestCtx.SetBodyString(challenge)
		return nil
	}

	a.Log.Warn("Lead webhook verification failed - token not found", "token", token)
	return r.SendErrorEnvelope(fasthttp.StatusForbidden, "Verification failed", nil, "")
}

// LeadWebhookHandler handles POST /api/webhook/leadgen
func (a *App) LeadWebhookHandler(r *fastglue.Request) error {
	body := r.RequestCtx.PostBody()

	var payload LeadgenWebhook
	if err := json.Unmarshal(body, &payload); err != nil {
		a.Log.Warn("Failed to parse leadgen webhook body", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid JSON payload", nil, "")
	}

	// 1. Must be object "page"
	if payload.Object != "page" {
		a.Log.Warn("Rejected non-page webhook object", "object", payload.Object)
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Expected object=page", nil, "")
	}

	if len(payload.Entry) == 0 {
		r.RequestCtx.SetStatusCode(fasthttp.StatusOK)
		return nil
	}

	pageID := payload.Entry[0].ID
	if pageID == "" {
		r.RequestCtx.SetStatusCode(fasthttp.StatusOK)
		return nil
	}

	// 2. Lookup LeadSource by pageID
	source, err := a.getLeadSourceCached(pageID)
	if err != nil {
		a.Log.Warn("Leadgen webhook for unknown page ID", "page_id", pageID, "error", err)
		// Return 200 so Meta stops retrying
		r.RequestCtx.SetStatusCode(fasthttp.StatusOK)
		return nil
	}

	// 3. Signature is mandatory for leadgen webhooks
	sigHeader := r.RequestCtx.Request.Header.Peek("X-Hub-Signature-256")
	if len(sigHeader) == 0 {
		a.Log.Warn("Missing X-Hub-Signature-256 header on lead webhook", "page_id", pageID)
		return r.SendErrorEnvelope(fasthttp.StatusForbidden, "Missing signature", nil, "")
	}

	if !verifyWebhookSignature(body, sigHeader, []byte(source.AppSecret)) {
		a.Log.Warn("Invalid X-Hub-Signature-256 on lead webhook", "page_id", pageID)
		return r.SendErrorEnvelope(fasthttp.StatusForbidden, "Invalid signature", nil, "")
	}

	// 4. Process entries and insert leads
	now := time.Now()
	insertedCount := 0

	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			if change.Field != "leadgen" || change.Value.LeadgenID == "" {
				continue
			}

			val := change.Value
			leadCreatedAt := time.Unix(val.CreatedTime, 0)
			if val.CreatedTime == 0 {
				leadCreatedAt = now
			}

			lead := models.Lead{
				BaseModel:      models.BaseModel{ID: uuid.New()},
				OrganizationID: source.OrganizationID,
				LeadSourceID:   source.ID,
				LeadgenID:      val.LeadgenID,
				PageID:         source.PageID,
				FormID:         val.FormID,
				AdID:           val.AdID,
				Status:         models.StatusLeadReceived,
				NextAttemptAt:  &now,
				Attempts:       0,
				LeadCreatedAt:  leadCreatedAt,
			}

			// ON CONFLICT (leadgen_id) DO NOTHING
			res := a.DB.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "leadgen_id"}},
				DoNothing: true,
			}).Create(&lead)

			if res.Error != nil {
				a.Log.Error("Failed to insert leadgen webhook row", "leadgen_id", val.LeadgenID, "error", res.Error)
			} else if res.RowsAffected > 0 {
				insertedCount++
			}
		}
	}

	// Update LeadSource.LastEventAt
	_ = a.DB.Model(&models.LeadSource{}).Where("id = ?", source.ID).Update("last_event_at", now).Error

	// Kick background processor if new rows were inserted
	if insertedCount > 0 && a.LeadProcessor != nil {
		a.LeadProcessor.Kick()
	}

	r.RequestCtx.SetStatusCode(fasthttp.StatusOK)
	return nil
}
