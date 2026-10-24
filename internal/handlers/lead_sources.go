package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nikyjain/whatomate/internal/leadutil"
	"github.com/nikyjain/whatomate/internal/models"
	"github.com/nikyjain/whatomate/internal/templateutil"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
	"gorm.io/gorm/clause"
)

// LeadSourceCreateRequest defines input for creating a connected Page
type LeadSourceCreateRequest struct {
	Name               string `json:"name"`
	PageID             string `json:"page_id"`
	PageAccessToken    string `json:"page_access_token"`
	AppID              string `json:"app_id"`
	AppSecret          string `json:"app_secret"`
	DefaultCountryCode string `json:"default_country_code"`
	APIVersion         string `json:"api_version"`
}

// LeadSourceUpdateRequest defines input for updating a connected Page
type LeadSourceUpdateRequest struct {
	Name               string `json:"name"`
	PageAccessToken    string `json:"page_access_token"` // blank = keep existing
	AppID              string `json:"app_id"`
	AppSecret          string `json:"app_secret"`        // blank = keep existing
	DefaultCountryCode string `json:"default_country_code"`
	APIVersion         string `json:"api_version"`
	IsActive           *bool  `json:"is_active"`
}

// LeadSourceResponse masks secret tokens
type LeadSourceResponse struct {
	models.LeadSource
	WebhookURL string `json:"webhook_url"`
}

// ListLeadSources lists connected Pages for an organization
func (a *App) ListLeadSources(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceLeadAds, models.ActionRead); err != nil {
		return err
	}

	var sources []models.LeadSource
	if err := a.DB.Where("organization_id = ?", orgID).Order("created_at DESC").Find(&sources).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to list lead sources", nil, "")
	}

	baseWebhook := a.Config.Server.PublicURL
	if baseWebhook == "" {
		baseWebhook = fmt.Sprintf("http://localhost:%d", a.Config.Server.Port)
	}
	webhookURL := fmt.Sprintf("%s/api/webhook/leadgen", strings.TrimSuffix(baseWebhook, "/"))

	resp := make([]LeadSourceResponse, len(sources))
	for i, s := range sources {
		resp[i] = LeadSourceResponse{
			LeadSource: s,
			WebhookURL: webhookURL,
		}
	}

	return r.SendEnvelope(resp)
}

// CreateLeadSource connects a new Facebook Page
func (a *App) CreateLeadSource(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceLeadAds, models.ActionWrite); err != nil {
		return err
	}

	var req LeadSourceCreateRequest
	if err := a.decodeRequest(r, &req); err != nil {
		return err
	}

	req.Name = strings.TrimSpace(req.Name)
	req.PageID = strings.TrimSpace(req.PageID)
	req.PageAccessToken = strings.TrimSpace(req.PageAccessToken)
	req.AppSecret = strings.TrimSpace(req.AppSecret)

	if req.Name == "" || req.PageID == "" || req.PageAccessToken == "" {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Name, PageID, and PageAccessToken are required", nil, "")
	}

	// Multi-tenant isolation: check if page_id is already connected in any other org
	var existingCount int64
	a.DB.Model(&models.LeadSource{}).Where("page_id = ?", req.PageID).Count(&existingCount)
	if existingCount > 0 {
		return r.SendErrorEnvelope(fasthttp.StatusConflict, "Page ID is already connected", nil, "")
	}

	apiVersion := req.APIVersion
	if apiVersion == "" {
		apiVersion = "v21.0"
	}
	defaultCC := req.DefaultCountryCode
	if defaultCC == "" {
		defaultCC = "91"
	}

	// Validate token with GET /{page_id}?fields=name
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pageProfile, err := a.WhatsApp.GetPage(ctx, apiVersion, req.PageID, req.PageAccessToken)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, fmt.Sprintf("Failed to validate Page token: %v", err), nil, "")
	}

	// Generate 32-hex webhook verify token
	verifyBytes := make([]byte, 16)
	_, _ = rand.Read(verifyBytes)
	webhookVerifyToken := hex.EncodeToString(verifyBytes)

	source := models.LeadSource{
		BaseModel:          models.BaseModel{ID: uuid.New()},
		OrganizationID:     orgID,
		Name:               req.Name,
		PageID:             req.PageID,
		PageName:           pageProfile.Name,
		AppID:              req.AppID,
		AppSecret:          req.AppSecret,
		PageAccessToken:    req.PageAccessToken,
		WebhookVerifyToken: webhookVerifyToken,
		APIVersion:         apiVersion,
		DefaultCountryCode: defaultCC,
		IsActive:           true,
		Subscribed:         false,
		CreatedByID:        &userID,
	}

	source.EncryptSecrets(a.Config.App.EncryptionKey)

	if err := a.DB.Create(&source).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to save lead source", nil, "")
	}

	a.InvalidateLeadSourceCache(source.PageID)

	baseWebhook := a.Config.Server.PublicURL
	if baseWebhook == "" {
		baseWebhook = fmt.Sprintf("http://localhost:%d", a.Config.Server.Port)
	}
	webhookURL := fmt.Sprintf("%s/api/webhook/leadgen", strings.TrimSuffix(baseWebhook, "/"))

	return r.SendEnvelope(LeadSourceResponse{
		LeadSource: source,
		WebhookURL: webhookURL,
	})
}

// GetLeadSource retrieves a single lead source
func (a *App) GetLeadSource(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceLeadAds, models.ActionRead); err != nil {
		return err
	}

	idStr := r.RequestCtx.UserValue("id").(string)
	sourceID, err := uuid.Parse(idStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid ID", nil, "")
	}

	var source models.LeadSource
	if err := a.DB.Where("id = ? AND organization_id = ?", sourceID, orgID).First(&source).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Lead source not found", nil, "")
	}

	baseWebhook := a.Config.Server.PublicURL
	if baseWebhook == "" {
		baseWebhook = fmt.Sprintf("http://localhost:%d", a.Config.Server.Port)
	}
	webhookURL := fmt.Sprintf("%s/api/webhook/leadgen", strings.TrimSuffix(baseWebhook, "/"))

	return r.SendEnvelope(LeadSourceResponse{
		LeadSource: source,
		WebhookURL: webhookURL,
	})
}

// UpdateLeadSource updates an existing lead source
func (a *App) UpdateLeadSource(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceLeadAds, models.ActionWrite); err != nil {
		return err
	}

	idStr := r.RequestCtx.UserValue("id").(string)
	sourceID, err := uuid.Parse(idStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid ID", nil, "")
	}

	var source models.LeadSource
	if err := a.DB.Where("id = ? AND organization_id = ?", sourceID, orgID).First(&source).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Lead source not found", nil, "")
	}

	var req LeadSourceUpdateRequest
	if err := a.decodeRequest(r, &req); err != nil {
		return err
	}

	if req.Name != "" {
		source.Name = strings.TrimSpace(req.Name)
	}
	if req.AppID != "" {
		source.AppID = strings.TrimSpace(req.AppID)
	}
	if req.APIVersion != "" {
		source.APIVersion = strings.TrimSpace(req.APIVersion)
	}
	if req.DefaultCountryCode != "" {
		source.DefaultCountryCode = strings.TrimSpace(req.DefaultCountryCode)
	}
	if req.IsActive != nil {
		source.IsActive = *req.IsActive
	}

	source.DecryptSecrets(a.Config.App.EncryptionKey)

	if strings.TrimSpace(req.PageAccessToken) != "" {
		source.PageAccessToken = strings.TrimSpace(req.PageAccessToken)
	}
	if strings.TrimSpace(req.AppSecret) != "" {
		source.AppSecret = strings.TrimSpace(req.AppSecret)
	}

	source.EncryptSecrets(a.Config.App.EncryptionKey)

	if err := a.DB.Save(&source).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to update lead source", nil, "")
	}

	a.InvalidateLeadSourceCache(source.PageID)
	return r.SendEnvelope(source)
}

// DeleteLeadSource deletes a lead source and unregisters subscribed apps
func (a *App) DeleteLeadSource(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceLeadAds, models.ActionDelete); err != nil {
		return err
	}

	idStr := r.RequestCtx.UserValue("id").(string)
	sourceID, err := uuid.Parse(idStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid ID", nil, "")
	}

	var source models.LeadSource
	if err := a.DB.Where("id = ? AND organization_id = ?", sourceID, orgID).First(&source).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Lead source not found", nil, "")
	}

	source.DecryptSecrets(a.Config.App.EncryptionKey)

	// Unsubscribe from Page
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = a.WhatsApp.UnsubscribePage(ctx, source.APIVersion, source.PageID, source.PageAccessToken)

	if err := a.DB.Delete(&source).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to delete lead source", nil, "")
	}

	a.InvalidateLeadSourceCache(source.PageID)
	return r.SendEnvelope(map[string]bool{"success": true})
}

// SubscribeLeadSource calls POST /{page-id}/subscribed_apps?subscribed_fields=leadgen
func (a *App) SubscribeLeadSource(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceLeadAds, models.ActionWrite); err != nil {
		return err
	}

	idStr := r.RequestCtx.UserValue("id").(string)
	sourceID, err := uuid.Parse(idStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid ID", nil, "")
	}

	var source models.LeadSource
	if err := a.DB.Where("id = ? AND organization_id = ?", sourceID, orgID).First(&source).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Lead source not found", nil, "")
	}

	source.DecryptSecrets(a.Config.App.EncryptionKey)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := a.WhatsApp.SubscribePage(ctx, source.APIVersion, source.PageID, source.PageAccessToken); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, fmt.Sprintf("Failed to subscribe page app: %v", err), nil, "")
	}

	source.Subscribed = true
	_ = a.DB.Model(&source).Update("subscribed", true).Error
	a.InvalidateLeadSourceCache(source.PageID)

	return r.SendEnvelope(map[string]bool{"subscribed": true})
}

// TestLeadSourceConnection checks token validity, leads access, and app subscription
func (a *App) TestLeadSourceConnection(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceLeadAds, models.ActionWrite); err != nil {
		return err
	}

	idStr := r.RequestCtx.UserValue("id").(string)
	sourceID, err := uuid.Parse(idStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid ID", nil, "")
	}

	var source models.LeadSource
	if err := a.DB.Where("id = ? AND organization_id = ?", sourceID, orgID).First(&source).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Lead source not found", nil, "")
	}

	source.DecryptSecrets(a.Config.App.EncryptionKey)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	checklist := map[string]interface{}{}

	// 1. Check Page Token & Read
	page, err := a.WhatsApp.GetPage(ctx, source.APIVersion, source.PageID, source.PageAccessToken)
	if err != nil {
		checklist["token_valid"] = false
		checklist["token_error"] = err.Error()
	} else {
		checklist["token_valid"] = true
		checklist["page_name"] = page.Name
	}

	// 2. Check Leads Access / forms
	forms, err := a.WhatsApp.GetPageForms(ctx, source.APIVersion, source.PageID, source.PageAccessToken)
	if err != nil {
		checklist["leads_access"] = false
		checklist["leads_access_error"] = err.Error()
	} else {
		checklist["leads_access"] = true
		checklist["forms_count"] = len(forms)
	}

	// 3. Subscription status
	checklist["subscribed"] = source.Subscribed

	return r.SendEnvelope(checklist)
}

// GetLeadSourceForms lists Meta forms merged with Whatomate mappings
func (a *App) GetLeadSourceForms(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceLeadAds, models.ActionRead); err != nil {
		return err
	}

	idStr := r.RequestCtx.UserValue("id").(string)
	sourceID, err := uuid.Parse(idStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid ID", nil, "")
	}

	var source models.LeadSource
	if err := a.DB.Where("id = ? AND organization_id = ?", sourceID, orgID).First(&source).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Lead source not found", nil, "")
	}

	source.DecryptSecrets(a.Config.App.EncryptionKey)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	metaForms, err := a.WhatsApp.GetPageForms(ctx, source.APIVersion, source.PageID, source.PageAccessToken)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, fmt.Sprintf("Failed to fetch forms: %v", err), nil, "")
	}

	// Load existing mappings
	var mappings []models.LeadFormMapping
	a.DB.Where("lead_source_id = ? AND organization_id = ?", source.ID, orgID).Find(&mappings)
	mappingByForm := make(map[string]models.LeadFormMapping)
	for _, m := range mappings {
		mappingByForm[m.FormID] = m
	}

	type FormItem struct {
		ID         string                  `json:"id"`
		Name       string                  `json:"name"`
		Status     string                  `json:"status"`
		Questions  []whatsapp.LeadFormQuestion `json:"questions"`
		LeadsCount int                     `json:"leads_count"`
		Mapping    *models.LeadFormMapping `json:"mapping,omitempty"`
	}

	result := make([]FormItem, len(metaForms))
	for i, f := range metaForms {
		var mapped *models.LeadFormMapping
		if m, ok := mappingByForm[f.ID]; ok {
			mapped = &m
		}
		result[i] = FormItem{
			ID:         f.ID,
			Name:       f.Name,
			Status:     f.Status,
			Questions:  f.Questions,
			LeadsCount: f.LeadsCount,
			Mapping:    mapped,
		}
	}

	return r.SendEnvelope(result)
}

// SyncLeadSource performs backfill of leads from form(s)
func (a *App) SyncLeadSource(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceLeadAds, models.ActionExecute); err != nil {
		return err
	}

	idStr := r.RequestCtx.UserValue("id").(string)
	sourceID, err := uuid.Parse(idStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid ID", nil, "")
	}

	var source models.LeadSource
	if err := a.DB.Where("id = ? AND organization_id = ?", sourceID, orgID).First(&source).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Lead source not found", nil, "")
	}

	var req struct {
		FormID string `json:"form_id"`
		Since  string `json:"since"`
		Send   bool   `json:"send"` // default false
	}
	if err := a.decodeRequest(r, &req); err != nil {
		return err
	}

	var sinceUnix int64
	if req.Since != "" {
		if t, err := time.Parse(time.RFC3339, req.Since); err == nil {
			sinceUnix = t.Unix()
		}
	}

	source.DecryptSecrets(a.Config.App.EncryptionKey)

	// Determine forms to sync
	var formIDs []string
	if req.FormID != "" {
		formIDs = append(formIDs, req.FormID)
	} else {
		// All mapped forms
		var mappings []models.LeadFormMapping
		a.DB.Where("lead_source_id = ? AND organization_id = ? AND is_active = ?", source.ID, orgID, true).Find(&mappings)
		for _, m := range mappings {
			if m.FormID != "*" {
				formIDs = append(formIDs, m.FormID)
			}
		}
	}

	if len(formIDs) == 0 {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "No form IDs found to sync", nil, "")
	}

	ctx := context.Background()
	totalSynced := 0
	now := time.Now()

	for _, formID := range formIDs {
		resp, err := a.WhatsApp.GetFormLeads(ctx, source.APIVersion, formID, source.PageAccessToken, sinceUnix, 100)
		if err != nil {
			a.Log.Error("Failed to fetch form leads for sync", "form_id", formID, "error", err)
			continue
		}

		for _, item := range resp.Data {
			leadCreatedAt, _ := time.Parse(time.RFC3339, item.CreatedTime)
			if leadCreatedAt.IsZero() {
				leadCreatedAt = now
			}

			initialStatus := models.StatusLeadReceived
			if !req.Send {
				// Don't send WhatsApp for backfilled historical leads
				initialStatus = models.StatusLeadStored
			}

			lead := models.Lead{
				BaseModel:      models.BaseModel{ID: uuid.New()},
				OrganizationID: orgID,
				LeadSourceID:   source.ID,
				LeadgenID:      item.ID,
				PageID:         source.PageID,
				FormID:         formID,
				AdID:           item.AdID,
				AdName:         item.AdName,
				CampaignName:   item.CampaignName,
				Platform:       item.Platform,
				IsOrganic:      item.IsOrganic,
				FieldData:      models.JSONB(leadutil.FlattenFieldData(item.FieldData)),
				Status:         initialStatus,
				NextAttemptAt:  &now,
				LeadCreatedAt:  leadCreatedAt,
			}

			res := a.DB.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "leadgen_id"}},
				DoNothing: true,
			}).Create(&lead)

			if res.Error == nil && res.RowsAffected > 0 {
				totalSynced++
			}
		}
	}

	// Update source last synced time
	_ = a.DB.Model(&source).Update("last_synced_at", now).Error

	if req.Send && totalSynced > 0 && a.LeadProcessor != nil {
		a.LeadProcessor.Kick()
	}

	return r.SendEnvelope(map[string]interface{}{
		"synced": totalSynced,
	})
}

// ListLeadMappings lists form mappings for a source
func (a *App) ListLeadMappings(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceLeadAds, models.ActionRead); err != nil {
		return err
	}

	idStr := r.RequestCtx.UserValue("id").(string)
	sourceID, err := uuid.Parse(idStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid ID", nil, "")
	}

	var mappings []models.LeadFormMapping
	if err := a.DB.Preload("Template").Where("lead_source_id = ? AND organization_id = ?", sourceID, orgID).Find(&mappings).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to list mappings", nil, "")
	}

	return r.SendEnvelope(mappings)
}

// CreateLeadMapping creates a mapping for a lead source
func (a *App) CreateLeadMapping(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceLeadAds, models.ActionWrite); err != nil {
		return err
	}

	idStr := r.RequestCtx.UserValue("id").(string)
	sourceID, err := uuid.Parse(idStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid ID", nil, "")
	}

	var req models.LeadFormMapping
	if err := a.decodeRequest(r, &req); err != nil {
		return err
	}

	req.FormID = strings.TrimSpace(req.FormID)
	if req.FormID == "" {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Form ID is required", nil, "")
	}

	// Validate template if set
	if req.TemplateID != nil {
		var tmpl models.Template
		if err := a.DB.Where("id = ? AND organization_id = ?", req.TemplateID, orgID).First(&tmpl).Error; err != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Template not found", nil, "")
		}
		if tmpl.Status != models.TemplateStatusApproved {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, fmt.Sprintf("Template is not approved (status: %s)", tmpl.Status), nil, "")
		}

		// Validate all vars mapped
		bodyVars := templateutil.ExtParamNames(tmpl.Body)
		for _, v := range bodyVars {
			if _, ok := req.TemplateParams[v]; !ok {
				return r.SendErrorEnvelope(fasthttp.StatusBadRequest, fmt.Sprintf("Variable %q is not mapped", v), nil, "")
			}
		}
	}

	req.BaseModel = models.BaseModel{ID: uuid.New()}
	req.OrganizationID = orgID
	req.LeadSourceID = sourceID

	if err := a.DB.Create(&req).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to create mapping", nil, "")
	}

	return r.SendEnvelope(req)
}

// UpdateLeadMapping updates a mapping
func (a *App) UpdateLeadMapping(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceLeadAds, models.ActionWrite); err != nil {
		return err
	}

	idStr := r.RequestCtx.UserValue("id").(string)
	mappingID, err := uuid.Parse(idStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid ID", nil, "")
	}

	var existing models.LeadFormMapping
	if err := a.DB.Where("id = ? AND organization_id = ?", mappingID, orgID).First(&existing).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Mapping not found", nil, "")
	}

	var req models.LeadFormMapping
	if err := a.decodeRequest(r, &req); err != nil {
		return err
	}

	// Validate template if set
	if req.TemplateID != nil {
		var tmpl models.Template
		if err := a.DB.Where("id = ? AND organization_id = ?", req.TemplateID, orgID).First(&tmpl).Error; err != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Template not found", nil, "")
		}
		if tmpl.Status != models.TemplateStatusApproved {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, fmt.Sprintf("Template is not approved (status: %s)", tmpl.Status), nil, "")
		}

		bodyVars := templateutil.ExtParamNames(tmpl.Body)
		for _, v := range bodyVars {
			if _, ok := req.TemplateParams[v]; !ok {
				return r.SendErrorEnvelope(fasthttp.StatusBadRequest, fmt.Sprintf("Variable %q is not mapped", v), nil, "")
			}
		}
	}

	existing.FormName = req.FormName
	existing.IsActive = req.IsActive
	existing.WhatsAppAccount = req.WhatsAppAccount
	existing.TemplateID = req.TemplateID
	existing.TemplateParams = req.TemplateParams
	existing.HeaderParams = req.HeaderParams
	existing.ButtonParams = req.ButtonParams
	existing.HeaderMediaID = req.HeaderMediaID
	existing.PhoneField = req.PhoneField
	existing.NameField = req.NameField
	existing.ConsentField = req.ConsentField
	existing.Tags = req.Tags
	existing.AssignUserID = req.AssignUserID
	existing.AssignTeamID = req.AssignTeamID
	existing.SendDelaySeconds = req.SendDelaySeconds
	existing.DedupeHours = req.DedupeHours
	existing.SkipIfOptedOut = req.SkipIfOptedOut

	if err := a.DB.Save(&existing).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to update mapping", nil, "")
	}

	return r.SendEnvelope(existing)
}

// DeleteLeadMapping deletes a mapping
func (a *App) DeleteLeadMapping(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceLeadAds, models.ActionDelete); err != nil {
		return err
	}

	idStr := r.RequestCtx.UserValue("id").(string)
	mappingID, err := uuid.Parse(idStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid ID", nil, "")
	}

	if err := a.DB.Where("id = ? AND organization_id = ?", mappingID, orgID).Delete(&models.LeadFormMapping{}).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to delete mapping", nil, "")
	}

	return r.SendEnvelope(map[string]bool{"success": true})
}

// PreviewLeadMapping renders the template message against sample data or an existing lead
func (a *App) PreviewLeadMapping(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceLeadAds, models.ActionRead); err != nil {
		return err
	}

	idStr := r.RequestCtx.UserValue("id").(string)
	mappingID, err := uuid.Parse(idStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid ID", nil, "")
	}

	var mapping models.LeadFormMapping
	if err := a.DB.Preload("Template").Where("id = ? AND organization_id = ?", mappingID, orgID).First(&mapping).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Mapping not found", nil, "")
	}

	if mapping.Template == nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Mapping has no template assigned", nil, "")
	}

	var req struct {
		LeadID string `json:"lead_id"`
	}
	_ = a.decodeRequest(r, &req)

	var lead models.Lead
	if req.LeadID != "" {
		if lID, err := uuid.Parse(req.LeadID); err == nil {
			_ = a.DB.Where("id = ? AND organization_id = ?", lID, orgID).First(&lead).Error
		}
	}

	// If no lead found, provide sample lead
	if lead.ID == uuid.Nil {
		lead = models.Lead{
			Name:          "John Doe",
			Phone:         "919876543210",
			Email:         "john@example.com",
			FormID:        mapping.FormID,
			AdName:        "Summer Campaign Ad #1",
			CampaignName:  "Lead Generation 2026",
			LeadCreatedAt: time.Now(),
			FieldData: models.JSONB{
				"full_name":    "John Doe",
				"phone_number": "+91 98765 43210",
				"email":        "john@example.com",
			},
		}
	}

	tmplData := leadutil.BuildTemplateData(&lead, &mapping, "Sample Page")
	renderedBody := mapping.Template.Body
	for k, expr := range mapping.TemplateParams {
		val := ProcessTemplate(fmt.Sprintf("%v", expr), tmplData)
		renderedBody = strings.ReplaceAll(renderedBody, "{{"+k+"}}", leadutil.CleanParamValue(val))
	}

	return r.SendEnvelope(map[string]interface{}{
		"template_name": mapping.Template.Name,
		"language":      mapping.Template.Language,
		"rendered_body": renderedBody,
	})
}
