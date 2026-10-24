package handlers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nikyjain/whatomate/internal/assignment"
	"github.com/nikyjain/whatomate/internal/contactutil"
	"github.com/nikyjain/whatomate/internal/leadutil"
	"github.com/nikyjain/whatomate/internal/models"
	"github.com/nikyjain/whatomate/internal/templateutil"
	"github.com/nikyjain/whatomate/internal/websocket"
)

// LeadProcessor is a background worker that fetches leads from Meta Graph API,
// creates/updates contacts, resolves form mappings, and sends template messages.
type LeadProcessor struct {
	app      *App
	interval time.Duration
	kick     chan struct{}
	stopCh   chan struct{}
	workers  int
}

// NewLeadProcessor creates a new LeadProcessor instance
func NewLeadProcessor(app *App, interval time.Duration) *LeadProcessor {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	return &LeadProcessor{
		app:      app,
		interval: interval,
		kick:     make(chan struct{}, 1),
		stopCh:   make(chan struct{}),
		workers:  4,
	}
}

// Start runs the processor loops
func (p *LeadProcessor) Start(ctx context.Context) {
	p.app.Log.Info("Lead processor started", "interval", p.interval, "workers", p.workers)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	// Initial poll immediately
	p.processBatch(ctx)

	for {
		select {
		case <-ctx.Done():
			p.app.Log.Info("Lead processor stopped by context")
			return
		case <-p.stopCh:
			p.app.Log.Info("Lead processor stopped")
			return
		case <-p.kick:
			p.processBatch(ctx)
		case <-ticker.C:
			p.processBatch(ctx)
		}
	}
}

// Kick triggers an immediate processing run without waiting for ticker
func (p *LeadProcessor) Kick() {
	select {
	case p.kick <- struct{}{}:
	default:
	}
}

// Stop terminates the lead processor
func (p *LeadProcessor) Stop() {
	select {
	case <-p.stopCh:
	default:
		close(p.stopCh)
	}
}

// processBatch claims up to 20 leads with SKIP LOCKED and processes them
func (p *LeadProcessor) processBatch(ctx context.Context) {
	var leads []models.Lead

	// Claim batch with 2-minute lease so crashed workers can be reclaimed
	err := p.app.DB.Raw(`
		UPDATE leads 
		SET attempts = attempts + 1, 
		    next_attempt_at = NOW() + INTERVAL '2 minutes' 
		WHERE id IN (
			SELECT id FROM leads 
			WHERE status IN ('received', 'fetched', 'retry') 
			  AND next_attempt_at <= NOW() 
			  AND deleted_at IS NULL 
			ORDER BY next_attempt_at ASC 
			LIMIT 20 
			FOR UPDATE SKIP LOCKED
		) 
		RETURNING *;
	`).Scan(&leads).Error

	if err != nil {
		p.app.Log.Error("Failed to claim leads batch", "error", err)
		return
	}

	if len(leads) == 0 {
		return
	}

	for i := range leads {
		if ctx.Err() != nil {
			return
		}
		p.processLead(ctx, &leads[i])
	}
}

func (p *LeadProcessor) broadcastLeadUpdate(lead *models.Lead) {
	if p.app.WSHub != nil {
		p.app.WSHub.BroadcastToOrg(lead.OrganizationID, websocket.WSMessage{
			Type:    websocket.TypeLeadUpdate,
			Payload: lead,
		})
	}
}

// processLead handles a single lead through the lifecycle
func (p *LeadProcessor) processLead(ctx context.Context, lead *models.Lead) {
	// 1. Fetch LeadSource
	var source models.LeadSource
	if err := p.app.DB.Where("id = ? AND organization_id = ?", lead.LeadSourceID, lead.OrganizationID).First(&source).Error; err != nil {
		lead.Status = models.StatusLeadFailed
		lead.LastError = fmt.Sprintf("Lead source not found: %v", err)
		p.app.DB.Save(lead)
		p.broadcastLeadUpdate(lead)
		return
	}
	source.DecryptSecrets(p.app.Config.App.EncryptionKey)

	// Check if source was disabled
	if !source.IsActive {
		lead.Status = models.StatusLeadFailed
		lead.LastError = "Lead source is inactive"
		p.app.DB.Save(lead)
		p.broadcastLeadUpdate(lead)
		return
	}

	// 2. Fetch lead details from Graph API if still in "received" status
	if lead.Status == models.StatusLeadReceived {
		details, err := p.app.WhatsApp.GetLead(ctx, source.APIVersion, lead.LeadgenID, source.PageAccessToken)
		if err != nil {
			retryable, reason := leadutil.Classify(err)
			if retryable && lead.Attempts < 5 {
				lead.Status = models.StatusLeadRetry
				next := time.Now().Add(backoffDuration(lead.Attempts))
				lead.NextAttemptAt = &next
				lead.LastError = reason
			} else {
				lead.Status = models.StatusLeadFailed
				lead.LastError = reason
				if strings.Contains(reason, "190") {
					// Mark source inactive
					_ = p.app.DB.Model(&models.LeadSource{}).Where("id = ?", source.ID).Update("is_active", false).Error
				}
			}
			p.app.DB.Save(lead)
			p.broadcastLeadUpdate(lead)
			return
		}

		// Update lead fields from Graph API
		lead.AdID = details.AdID
		lead.AdName = details.AdName
		lead.CampaignName = details.CampaignName
		lead.Platform = details.Platform
		lead.IsOrganic = details.IsOrganic
		if details.FormID != "" {
			lead.FormID = details.FormID
		}
		lead.FieldData = models.JSONB(leadutil.FlattenFieldData(details.FieldData))
		lead.Status = models.StatusLeadFetched
		if err := p.app.DB.Save(lead).Error; err != nil {
			p.app.Log.Error("Failed to save fetched lead", "lead_id", lead.ID, "error", err)
			return
		}

		// Dispatch lead.received webhook event
		leadPayload := map[string]interface{}{
			"id":            lead.ID,
			"leadgen_id":    lead.LeadgenID,
			"page_id":       lead.PageID,
			"form_id":       lead.FormID,
			"ad_id":         lead.AdID,
			"ad_name":       lead.AdName,
			"campaign_name": lead.CampaignName,
			"platform":      lead.Platform,
			"is_organic":    lead.IsOrganic,
			"created_at":    lead.LeadCreatedAt,
		}
		var redactSetting models.SystemSetting
		if err := p.app.DB.Where("key = ?", "redact_lead_answers").First(&redactSetting).Error; err != nil || redactSetting.Value != "true" {
			leadPayload["field_data"] = lead.FieldData
		}
		p.app.DispatchWebhook(lead.OrganizationID, models.WebhookEventLeadReceived, leadPayload)
		p.broadcastLeadUpdate(lead)
	}

	// 3. Resolve Form Mapping
	var mapping models.LeadFormMapping
	err := p.app.DB.Where("lead_source_id = ? AND form_id = ? AND is_active = ?", source.ID, lead.FormID, true).First(&mapping).Error
	if err != nil {
		// Fallback to wildcard default mapping
		err = p.app.DB.Where("lead_source_id = ? AND form_id = ? AND is_active = ?", source.ID, "*", true).First(&mapping).Error
		if err != nil {
			lead.Status = models.StatusLeadSkippedNoMapping
			lead.LastError = fmt.Sprintf("No active mapping for form %s or default *", lead.FormID)
			p.app.DB.Save(lead)
			p.broadcastLeadUpdate(lead)
			return
		}
	}
	lead.MappingID = &mapping.ID

	// If form name is empty, try to populate
	if mapping.FormName == "" && lead.FormID != "*" {
		mapping.FormName = lead.FormID
	}

	// 4. Extract Name, Phone, Email
	phoneFieldName := mapping.PhoneField
	if phoneFieldName == "" {
		phoneFieldName = "phone_number"
	}
	nameFieldName := mapping.NameField
	if nameFieldName == "" {
		nameFieldName = "full_name"
	}

	var rawPhone, extractedName, extractedEmail string
	if val, ok := lead.FieldData[phoneFieldName]; ok && val != nil {
		rawPhone = fmt.Sprintf("%v", val)
	}
	if val, ok := lead.FieldData[nameFieldName]; ok && val != nil {
		extractedName = fmt.Sprintf("%v", val)
	} else {
		// Fallback to first_name + last_name
		fn, _ := lead.FieldData["first_name"]
		ln, _ := lead.FieldData["last_name"]
		extractedName = strings.TrimSpace(fmt.Sprintf("%v %v", fn, ln))
		extractedName = strings.TrimSpace(strings.ReplaceAll(extractedName, "<nil>", ""))
	}
	if val, ok := lead.FieldData["email"]; ok && val != nil {
		extractedEmail = fmt.Sprintf("%v", val)
	}

	lead.PhoneRaw = rawPhone
	lead.Name = extractedName
	lead.Email = extractedEmail

	// Normalize phone
	normalizedPhone, err := leadutil.NormalizePhone(rawPhone, source.DefaultCountryCode)
	if err != nil {
		lead.Status = models.StatusLeadSkippedInvalidPhone
		lead.LastError = fmt.Sprintf("Invalid phone %q: %v", rawPhone, err)
		p.app.DB.Save(lead)
		p.broadcastLeadUpdate(lead)
		return
	}
	lead.Phone = normalizedPhone

	// 5. Consent Enforcement
	if mapping.ConsentField != "" {
		consentVal, ok := lead.FieldData[mapping.ConsentField]
		if !ok || !leadutil.IsAffirmativeConsent(fmt.Sprintf("%v", consentVal)) {
			lead.Status = models.StatusLeadSkippedNoConsent
			lead.LastError = fmt.Sprintf("Consent field %q value %v not affirmative", mapping.ConsentField, consentVal)
			p.app.DB.Save(lead)
			p.broadcastLeadUpdate(lead)
			return
		}
	}

	// 6. Contact Upsert & Assignment
	contact, isNew, err := contactutil.GetOrCreateContact(p.app.DB, lead.OrganizationID, lead.Phone, lead.Name)
	if err != nil {
		lead.Status = models.StatusLeadFailed
		lead.LastError = fmt.Sprintf("Failed to get or create contact: %v", err)
		p.app.DB.Save(lead)
		p.broadcastLeadUpdate(lead)
		return
	}
	lead.ContactID = &contact.ID

	// Update contact whatsapp_account if empty
	if contact.WhatsAppAccount == "" && mapping.WhatsAppAccount != "" {
		contact.WhatsAppAccount = mapping.WhatsAppAccount
	}

	// Merge contact tags
	mergedTags := make(map[string]bool)
	for _, t := range contact.Tags {
		mergedTags[t] = true
	}
	for _, t := range mapping.Tags {
		mergedTags[t] = true
	}
	var tagList []string
	for t := range mergedTags {
		tagList = append(tagList, t)
	}
	contact.Tags = models.JSONBArray(tagList)

	// Merge metadata.lead
	if contact.Metadata == nil {
		contact.Metadata = models.JSONB{}
	}
	contact.Metadata["lead"] = map[string]interface{}{
		"source":        source.Name,
		"form_id":       lead.FormID,
		"form_name":     mapping.FormName,
		"ad_name":       lead.AdName,
		"campaign_name": lead.CampaignName,
		"leadgen_id":    lead.LeadgenID,
		"answers":       lead.FieldData,
	}

	// Assignment if unassigned
	if contact.AssignedUserID == nil {
		if mapping.AssignUserID != nil {
			contact.AssignedUserID = mapping.AssignUserID
		} else if mapping.AssignTeamID != nil && p.app.Assigner != nil {
			assigned := p.app.Assigner.AssignToTeam(*mapping.AssignTeamID, lead.OrganizationID, nil, nil)
			if assigned != nil {
				contact.AssignedUserID = assigned
			}
		}
	}

	_ = p.app.DB.Save(contact).Error
	if isNew {
		p.app.DispatchWebhook(lead.OrganizationID, models.WebhookEventContactCreated, contact)
	}

	// 7. Dedupe Check
	if mapping.DedupeHours > 0 {
		cutoff := time.Now().Add(-time.Duration(mapping.DedupeHours) * time.Hour)
		var dedupeCount int64
		p.app.DB.Model(&models.Lead{}).
			Where("organization_id = ? AND phone = ? AND form_id = ? AND status = ? AND sent_at > ? AND id != ?",
				lead.OrganizationID, lead.Phone, lead.FormID, models.StatusLeadSent, cutoff, lead.ID).
			Count(&dedupeCount)

		if dedupeCount > 0 {
			lead.Status = models.StatusLeadSkippedDuplicate
			lead.LastError = fmt.Sprintf("Duplicate lead for phone %s and form %s within %d hours", lead.Phone, lead.FormID, mapping.DedupeHours)
			p.app.DB.Save(lead)
			p.broadcastLeadUpdate(lead)
			return
		}
	}

	// 8. No Template: Set Stored
	if mapping.TemplateID == nil {
		lead.Status = models.StatusLeadStored
		p.app.DB.Save(lead)
		p.broadcastLeadUpdate(lead)
		return
	}

	// Load template
	var tmpl models.Template
	if err := p.app.DB.Where("id = ? AND organization_id = ?", mapping.TemplateID, lead.OrganizationID).First(&tmpl).Error; err != nil {
		lead.Status = models.StatusLeadFailed
		lead.LastError = fmt.Sprintf("Template %v not found: %v", mapping.TemplateID, err)
		p.app.DB.Save(lead)
		p.broadcastLeadUpdate(lead)
		return
	}

	if tmpl.Status != models.TemplateStatusApproved {
		lead.Status = models.StatusLeadFailed
		lead.LastError = fmt.Sprintf("Template %s is not approved (status: %s)", tmpl.Name, tmpl.Status)
		p.app.DB.Save(lead)
		p.broadcastLeadUpdate(lead)
		return
	}

	// 9. Marketing Opt-Out Check
	if tmpl.Category == string(models.TemplateCategoryMarketing) && contact.MarketingOptOut && mapping.SkipIfOptedOut {
		lead.Status = models.StatusLeadSkippedOptedOut
		lead.LastError = "Recipient has opted out of marketing templates"
		p.app.DB.Save(lead)
		p.broadcastLeadUpdate(lead)
		return
	}

	// 10. Send Delay
	if mapping.SendDelaySeconds > 0 {
		delayUntil := lead.LeadCreatedAt.Add(time.Duration(mapping.SendDelaySeconds) * time.Second)
		if time.Now().Before(delayUntil) {
			lead.NextAttemptAt = &delayUntil
			if lead.Attempts > 0 {
				lead.Attempts-- // Refund the attempt
			}
			p.app.DB.Save(lead)
			return
		}
	}

	// 11. Render Parameters and Send Message
	tmplData := leadutil.BuildTemplateData(lead, &mapping, source.PageName)

	bodyParams := make(map[string]string)
	for k, expr := range mapping.TemplateParams {
		rendered := ProcessTemplate(fmt.Sprintf("%v", expr), tmplData)
		cleaned := leadutil.CleanParamValue(rendered)
		if cleaned == "" {
			lead.Status = models.StatusLeadFailed
			lead.LastError = fmt.Sprintf("Missing parameter value for %q", k)
			p.app.DB.Save(lead)
			p.broadcastLeadUpdate(lead)
			return
		}
		bodyParams[k] = cleaned
	}

	// Header parameters fallback
	for k, expr := range mapping.HeaderParams {
		rendered := ProcessTemplate(fmt.Sprintf("%v", expr), tmplData)
		cleaned := leadutil.CleanParamValue(rendered)
		if cleaned != "" {
			bodyParams[k] = cleaned
		}
	}

	buttonParams := make(map[int]string)
	for k, expr := range mapping.ButtonParams {
		idx, err := strconv.Atoi(k)
		if err == nil {
			rendered := ProcessTemplate(fmt.Sprintf("%v", expr), tmplData)
			buttonParams[idx] = leadutil.CleanParamValue(rendered)
		}
	}

	// Resolve WhatsApp Account
	accountName := mapping.WhatsAppAccount
	var waAcc models.WhatsAppAccount
	query := p.app.DB.Where("organization_id = ?", lead.OrganizationID)
	if accountName != "" {
		query = query.Where("name = ?", accountName)
	} else {
		query = query.Where("is_default = ?", true)
	}
	if err := query.First(&waAcc).Error; err != nil {
		// Fallback to any active account
		if err := p.app.DB.Where("organization_id = ? AND is_active = ?", lead.OrganizationID, true).First(&waAcc).Error; err != nil {
			lead.Status = models.StatusLeadFailed
			lead.LastError = "No active WhatsApp account found to send template"
			p.app.DB.Save(lead)
			p.broadcastLeadUpdate(lead)
			return
		}
	}
	waAcc.DecryptSecrets(p.app.Config.App.EncryptionKey)

	// Send message synchronously
	msgReq := OutgoingMessageRequest{
		Account:       &waAcc,
		Contact:       contact,
		Type:          models.MessageTypeTemplate,
		Template:      &tmpl,
		BodyParams:    bodyParams,
		ButtonParams:  buttonParams,
		HeaderMediaID: mapping.HeaderMediaID,
	}

	msg, err := p.app.SendOutgoingMessage(ctx, msgReq, LeadSendOptions())
	if err != nil {
		retryable, reason := leadutil.Classify(err)
		if retryable && lead.Attempts < 5 {
			lead.Status = models.StatusLeadRetry
			next := time.Now().Add(backoffDuration(lead.Attempts))
			lead.NextAttemptAt = &next
			lead.LastError = reason
		} else {
			lead.Status = models.StatusLeadFailed
			lead.LastError = reason
			if strings.Contains(reason, "131050") {
				contact.MarketingOptOut = true
				_ = p.app.DB.Save(contact).Error
				lead.Status = models.StatusLeadSkippedOptedOut
			}
		}
		p.app.DB.Save(lead)
		p.broadcastLeadUpdate(lead)
		return
	}

	now := time.Now()
	lead.Status = models.StatusLeadSent
	lead.MessageID = &msg.ID
	lead.SentAt = &now
	lead.LastError = ""
	p.app.DB.Save(lead)

	// Dispatch lead.sent webhook event
	p.app.DispatchWebhook(lead.OrganizationID, models.WebhookEventLeadSent, map[string]interface{}{
		"id":         lead.ID,
		"leadgen_id": lead.LeadgenID,
		"phone":      lead.Phone,
		"message_id": msg.ID,
		"sent_at":    now,
	})
	p.broadcastLeadUpdate(lead)
}

func backoffDuration(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 30 * time.Second
	case 2:
		return 2 * time.Minute
	case 3:
		return 10 * time.Minute
	case 4:
		return 1 * time.Hour
	default:
		return 6 * time.Hour
	}
}
