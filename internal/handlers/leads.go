package handlers

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/nikyjain/whatomate/internal/leadutil"
	"github.com/nikyjain/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// ListLeads lists leads with pagination and filtering
func (a *App) ListLeads(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceLeadAds, models.ActionRead); err != nil {
		return err
	}

	query := a.DB.Model(&models.Lead{}).
		Preload("LeadSource").
		Preload("Mapping").
		Preload("Contact").
		Preload("Message").
		Where("leads.organization_id = ?", orgID)

	// Filters
	sourceID := string(r.RequestCtx.QueryArgs().Peek("source_id"))
	if sourceID != "" {
		if sID, err := uuid.Parse(sourceID); err == nil {
			query = query.Where("lead_source_id = ?", sID)
		}
	}
	formID := string(r.RequestCtx.QueryArgs().Peek("form_id"))
	if formID != "" {
		query = query.Where("form_id = ?", formID)
	}
	status := string(r.RequestCtx.QueryArgs().Peek("status"))
	if status != "" {
		query = query.Where("status = ?", status)
	}
	phone := string(r.RequestCtx.QueryArgs().Peek("phone"))
	if phone != "" {
		query = query.Where("phone LIKE ? OR phone_raw LIKE ?", "%"+phone+"%", "%"+phone+"%")
	}
	fromStr := string(r.RequestCtx.QueryArgs().Peek("from"))
	if fromStr != "" {
		if fromTime, err := time.Parse(time.RFC3339, fromStr); err == nil {
			query = query.Where("lead_created_at >= ?", fromTime)
		}
	}
	toStr := string(r.RequestCtx.QueryArgs().Peek("to"))
	if toStr != "" {
		if toTime, err := time.Parse(time.RFC3339, toStr); err == nil {
			query = query.Where("lead_created_at <= ?", toTime)
		}
	}

	var total int64
	query.Count(&total)

	page := 1
	if p, err := strconv.Atoi(string(r.RequestCtx.QueryArgs().Peek("page"))); err == nil && p > 0 {
		page = p
	}
	limit := 20
	if l, err := strconv.Atoi(string(r.RequestCtx.QueryArgs().Peek("limit"))); err == nil && l > 0 && l <= 100 {
		limit = l
	}

	var leads []models.Lead
	if err := query.Order("lead_created_at DESC, created_at DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&leads).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to fetch leads", nil, "")
	}

	return r.SendEnvelope(map[string]interface{}{
		"leads": leads,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// GetLead retrieves a full lead detail
func (a *App) GetLead(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceLeadAds, models.ActionRead); err != nil {
		return err
	}

	idStr := r.RequestCtx.UserValue("id").(string)
	leadID, err := uuid.Parse(idStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid ID", nil, "")
	}

	var lead models.Lead
	if err := a.DB.Preload("LeadSource").
		Preload("Mapping").
		Preload("Contact").
		Preload("Message").
		Where("id = ? AND organization_id = ?", leadID, orgID).
		First(&lead).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Lead not found", nil, "")
	}

	return r.SendEnvelope(lead)
}

// RetryLead manually re-queues a lead for processing
func (a *App) RetryLead(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceLeadAds, models.ActionExecute); err != nil {
		return err
	}

	idStr := r.RequestCtx.UserValue("id").(string)
	leadID, err := uuid.Parse(idStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid ID", nil, "")
	}

	var lead models.Lead
	if err := a.DB.Where("id = ? AND organization_id = ?", leadID, orgID).First(&lead).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Lead not found", nil, "")
	}

	now := time.Now()
	if len(lead.FieldData) > 0 {
		lead.Status = models.StatusLeadFetched
	} else {
		lead.Status = models.StatusLeadReceived
	}
	lead.NextAttemptAt = &now
	lead.LastError = ""

	if err := a.DB.Save(&lead).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to retry lead", nil, "")
	}

	if a.LeadProcessor != nil {
		a.LeadProcessor.Kick()
	}

	return r.SendEnvelope(lead)
}

// SendLeadNow forces a template send for a lead, bypassing deduplication
func (a *App) SendLeadNow(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceLeadAds, models.ActionExecute); err != nil {
		return err
	}

	idStr := r.RequestCtx.UserValue("id").(string)
	leadID, err := uuid.Parse(idStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid ID", nil, "")
	}

	var lead models.Lead
	if err := a.DB.Where("id = ? AND organization_id = ?", leadID, orgID).First(&lead).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Lead not found", nil, "")
	}

	// If processor is available, execute synchronous processing bypassing dedupe
	if p, ok := a.LeadProcessor.(*LeadProcessor); ok {
		p.processLead(context.Background(), &lead)
		return r.SendEnvelope(lead)
	}

	now := time.Now()
	lead.Status = models.StatusLeadFetched
	lead.NextAttemptAt = &now
	_ = a.DB.Save(&lead).Error

	if a.LeadProcessor != nil {
		a.LeadProcessor.Kick()
	}

	return r.SendEnvelope(lead)
}

// DeleteLead soft-deletes a lead or purges PII if purge=true
func (a *App) DeleteLead(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceLeadAds, models.ActionDelete); err != nil {
		return err
	}

	idStr := r.RequestCtx.UserValue("id").(string)
	leadID, err := uuid.Parse(idStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid ID", nil, "")
	}

	var lead models.Lead
	if err := a.DB.Where("id = ? AND organization_id = ?", leadID, orgID).First(&lead).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Lead not found", nil, "")
	}

	purge := string(r.RequestCtx.QueryArgs().Peek("purge")) == "true"
	if purge {
		// Hard-delete lead and purge metadata.lead from contact if linked
		if lead.ContactID != nil {
			var contact models.Contact
			if err := a.DB.Where("id = ? AND organization_id = ?", lead.ContactID, orgID).First(&contact).Error; err == nil {
				if contact.Metadata != nil {
					delete(contact.Metadata, "lead")
					_ = a.DB.Save(&contact).Error
				}
			}
		}
		if err := a.DB.Unscoped().Delete(&lead).Error; err != nil {
			return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to purge lead", nil, "")
		}
	} else {
		if err := a.DB.Delete(&lead).Error; err != nil {
			return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to delete lead", nil, "")
		}
	}

	return r.SendEnvelope(map[string]bool{"success": true})
}

// GetLeadStats returns status metrics (today and total) for the top strip
func (a *App) GetLeadStats(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceLeadAds, models.ActionRead); err != nil {
		return err
	}

	startOfToday := time.Now().Truncate(24 * time.Hour)

	type StatusCount struct {
		Status string `json:"status"`
		Count  int64  `json:"count"`
	}

	var todayCounts []StatusCount
	a.DB.Model(&models.Lead{}).
		Select("status, count(*) as count").
		Where("organization_id = ? AND lead_created_at >= ?", orgID, startOfToday).
		Group("status").
		Scan(&todayCounts)

	var totalCounts []StatusCount
	a.DB.Model(&models.Lead{}).
		Select("status, count(*) as count").
		Where("organization_id = ?", orgID).
		Group("status").
		Scan(&totalCounts)

	return r.SendEnvelope(map[string]interface{}{
		"today": todayCounts,
		"total": totalCounts,
	})
}
