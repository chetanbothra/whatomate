package handlers

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nikyjain/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// maxStatusLookupIDs caps how many messages a single batch lookup may ask for.
const maxStatusLookupIDs = 100

// MessageStatusResponse is the on-demand equivalent of the status callback
// payload. The field names match the callback body exactly, so an integrator
// can feed both into the same handler.
type MessageStatusResponse struct {
	MessageID         string `json:"message_id"`
	WhatsAppMessageID string `json:"whatsapp_message_id,omitempty"`
	Status            string `json:"status"`
	ErrorMessage      string `json:"error_message,omitempty"`
	ContactID         string `json:"contact_id,omitempty"`
	ContactPhone      string `json:"contact_phone,omitempty"`
	ContactName       string `json:"contact_name,omitempty"`
	WhatsAppAccount   string `json:"whatsapp_account,omitempty"`
	TemplateName      string `json:"template_name,omitempty"`
	Direction         string    `json:"direction,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// BatchMessageStatusRequest asks for the current status of several messages at
// once. Either list may be used, and they may be mixed in one request.
type BatchMessageStatusRequest struct {
	MessageIDs         []string `json:"message_ids"`
	WhatsAppMessageIDs []string `json:"whatsapp_message_ids"`
}

// buildMessageStatusResponse converts a message (plus optional contact) into the
// response shape. Timestamps are emitted in UTC to match the callback payload.
func buildMessageStatusResponse(msg models.Message, contact *models.Contact) MessageStatusResponse {
	resp := MessageStatusResponse{
		MessageID:         msg.ID.String(),
		WhatsAppMessageID: msg.WhatsAppMessageID,
		Status:            string(msg.Status),
		ErrorMessage:      msg.ErrorMessage,
		WhatsAppAccount:   msg.WhatsAppAccount,
		TemplateName:      msg.TemplateName,
		Direction:         string(msg.Direction),
		CreatedAt:         msg.CreatedAt.UTC(),
		UpdatedAt:         msg.UpdatedAt.UTC(),
	}
	if msg.ContactID != uuid.Nil {
		resp.ContactID = msg.ContactID.String()
	}
	if contact != nil {
		resp.ContactPhone = contact.PhoneNumber
		resp.ContactName = contact.ProfileName
	}
	return resp
}

// GetMessageStatus returns the current delivery status of a single message.
//
// The {id} path parameter accepts either the Whatomate message UUID (the `id`
// returned when the message was sent) or the WhatsApp message ID (`wamid...`).
// External systems use this to reconcile when a status callback never arrived.
func (a *App) GetMessageStatus(r *fastglue.Request) error {
	orgID, err := a.getOrgID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}

	rawID, _ := r.RequestCtx.UserValue("id").(string)
	rawID = strings.TrimSpace(rawID)
	if rawID == "" {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Message ID is required", nil, "")
	}

	msg, found := a.findMessageForStatus(orgID, rawID)
	if !found {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Message not found", nil, "")
	}

	return r.SendEnvelope(buildMessageStatusResponse(*msg, a.loadContactForStatus(orgID, msg.ContactID)))
}

// BatchMessageStatus returns the current status of up to maxStatusLookupIDs
// messages in one call. IDs that do not exist in the caller's organization are
// listed in `not_found` rather than failing the whole request, so a reconciliation
// job can process what it can and flag the rest.
func (a *App) BatchMessageStatus(r *fastglue.Request) error {
	orgID, err := a.getOrgID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}

	var req BatchMessageStatusRequest
	if err := a.decodeRequest(r, &req); err != nil {
		return nil
	}

	// Preserve caller order and drop blanks/duplicates
	seen := make(map[string]struct{})
	requested := make([]string, 0, len(req.MessageIDs)+len(req.WhatsAppMessageIDs))
	for _, raw := range append(append([]string{}, req.MessageIDs...), req.WhatsAppMessageIDs...) {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		requested = append(requested, id)
	}

	if len(requested) == 0 {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "message_ids or whatsapp_message_ids is required", nil, "")
	}
	if len(requested) > maxStatusLookupIDs {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Too many IDs requested (maximum is 100)", nil, "")
	}

	statuses := make([]MessageStatusResponse, 0, len(requested))
	notFound := make([]string, 0)
	contacts := make(map[uuid.UUID]*models.Contact)

	for _, id := range requested {
		msg, found := a.findMessageForStatus(orgID, id)
		if !found {
			notFound = append(notFound, id)
			continue
		}

		contact, cached := contacts[msg.ContactID]
		if !cached {
			contact = a.loadContactForStatus(orgID, msg.ContactID)
			contacts[msg.ContactID] = contact
		}
		statuses = append(statuses, buildMessageStatusResponse(*msg, contact))
	}

	return r.SendEnvelope(map[string]any{
		"statuses":  statuses,
		"not_found": notFound,
	})
}

// findMessageForStatus resolves a message within the organization by either its
// Whatomate UUID or its WhatsApp message ID. Org scoping is always applied.
func (a *App) findMessageForStatus(orgID uuid.UUID, rawID string) (*models.Message, bool) {
	var msg models.Message

	query := a.DB.Where("organization_id = ?", orgID)
	if id, err := uuid.Parse(rawID); err == nil {
		query = query.Where("id = ?", id)
	} else {
		query = query.Where("whats_app_message_id = ?", rawID)
	}

	if err := query.First(&msg).Error; err != nil {
		return nil, false
	}
	return &msg, true
}

// loadContactForStatus loads the contact for a message, returning nil when it
// cannot be found. A missing contact must not fail the status lookup.
func (a *App) loadContactForStatus(orgID, contactID uuid.UUID) *models.Contact {
	if contactID == uuid.Nil {
		return nil
	}
	var contact models.Contact
	if err := a.DB.Where("id = ? AND organization_id = ?", contactID, orgID).First(&contact).Error; err != nil {
		return nil
	}
	return &contact
}
