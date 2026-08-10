package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/nikyjain/whatomate/internal/models"
	"github.com/nikyjain/whatomate/internal/websocket"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// WebhookVerify handles Meta's webhook verification challenge
func (a *App) WebhookVerify(r *fastglue.Request) error {
	mode := string(r.RequestCtx.QueryArgs().Peek("hub.mode"))
	token := string(r.RequestCtx.QueryArgs().Peek("hub.verify_token"))
	challenge := string(r.RequestCtx.QueryArgs().Peek("hub.challenge"))

	if mode != "subscribe" {
		a.Log.Warn("Webhook verification failed - invalid mode", "mode", mode)
		return r.SendErrorEnvelope(fasthttp.StatusForbidden, "Verification failed", nil, "")
	}

	// First check against global config token
	if token == a.Config.WhatsApp.WebhookVerifyToken && token != "" {
		a.Log.Info("Webhook verified successfully (global token)")
		r.RequestCtx.SetStatusCode(fasthttp.StatusOK)
		r.RequestCtx.SetBodyString(challenge)
		return nil
	}

	// Then check against tokens stored in WhatsApp accounts
	var account models.WhatsAppAccount
	result := a.DB.Where("webhook_verify_token = ?", token).First(&account)
	if result.Error == nil {
		a.Log.Info("Webhook verified successfully (account token)", "account", account.Name)
		r.RequestCtx.SetStatusCode(fasthttp.StatusOK)
		r.RequestCtx.SetBodyString(challenge)
		return nil
	}

	a.Log.Warn("Webhook verification failed - token not found", "token", token)
	return r.SendErrorEnvelope(fasthttp.StatusForbidden, "Verification failed", nil, "")
}

// WebhookStatusError represents an error in a status update
type WebhookStatusError struct {
	Code      int    `json:"code"`
	Title     string `json:"title"`
	Message   string `json:"message"`
	ErrorData struct {
		Details string `json:"details"`
	} `json:"error_data"`
}

// TemplateStatusUpdate represents a template status update from Meta webhook
type TemplateStatusUpdate struct {
	Event                   string `json:"event"`
	MessageTemplateID       int64  `json:"message_template_id"`
	MessageTemplateName     string `json:"message_template_name"`
	MessageTemplateLanguage string `json:"message_template_language"`
	Reason                  string `json:"reason,omitempty"`
}

// WebhookStatus represents a message status update from Meta
type WebhookStatus struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Timestamp    string `json:"timestamp"`
	RecipientID  string `json:"recipient_id"`
	Conversation *struct {
		ID string `json:"id"`
	} `json:"conversation,omitempty"`
	Pricing *struct {
		Billable     bool   `json:"billable"`
		PricingModel string `json:"pricing_model"`
		Category     string `json:"category"`
	} `json:"pricing,omitempty"`
	Errors []WebhookStatusError `json:"errors,omitempty"`
}

// WebhookPayload represents the incoming webhook from Meta
type WebhookPayload struct {
	Object string `json:"object"`
	Entry  []struct {
		ID      string `json:"id"`
		Changes []struct {
			Value struct {
				MessagingProduct string `json:"messaging_product"`
				Metadata         struct {
					DisplayPhoneNumber string `json:"display_phone_number"`
					PhoneNumberID      string `json:"phone_number_id"`
				} `json:"metadata"`
				// Template status update fields (when field == "message_template_status_update")
				Event                   string `json:"event,omitempty"`
				MessageTemplateID       int64  `json:"message_template_id,omitempty"`
				MessageTemplateName     string `json:"message_template_name,omitempty"`
				MessageTemplateLanguage string `json:"message_template_language,omitempty"`
				Reason                  string `json:"reason,omitempty"`
				Contacts                []struct {
					Profile struct {
						Name string `json:"name"`
					} `json:"profile"`
					WaID string `json:"wa_id"`
				} `json:"contacts"`
				Messages []struct {
					From      string `json:"from"`
					ID        string `json:"id"`
					Timestamp string `json:"timestamp"`
					Type      string `json:"type"`
					Text      *struct {
						Body string `json:"body"`
					} `json:"text,omitempty"`
					Button *struct {
						Payload string `json:"payload"`
						Text    string `json:"text"`
					} `json:"button,omitempty"`
					Image *struct {
						ID       string `json:"id"`
						MimeType string `json:"mime_type"`
						SHA256   string `json:"sha256"`
						Caption  string `json:"caption,omitempty"`
					} `json:"image,omitempty"`
					Document *struct {
						ID       string `json:"id"`
						MimeType string `json:"mime_type"`
						SHA256   string `json:"sha256"`
						Filename string `json:"filename"`
						Caption  string `json:"caption,omitempty"`
					} `json:"document,omitempty"`
					Audio *struct {
						ID       string `json:"id"`
						MimeType string `json:"mime_type"`
					} `json:"audio,omitempty"`
					Video *struct {
						ID       string `json:"id"`
						MimeType string `json:"mime_type"`
						SHA256   string `json:"sha256"`
						Caption  string `json:"caption,omitempty"`
					} `json:"video,omitempty"`
					Interactive *struct {
						Type        string `json:"type"`
						ButtonReply *struct {
							ID    string `json:"id"`
							Title string `json:"title"`
						} `json:"button_reply,omitempty"`
						ListReply *struct {
							ID          string `json:"id"`
							Title       string `json:"title"`
							Description string `json:"description"`
						} `json:"list_reply,omitempty"`
						NFMReply *struct {
							ResponseJSON string `json:"response_json"`
							Body         string `json:"body"`
							Name         string `json:"name"`
						} `json:"nfm_reply,omitempty"`
						CallPermissionReply *struct {
							Response            string      `json:"response"`
							IsPermanent         bool        `json:"is_permanent"`
							ExpirationTimestamp json.Number `json:"expiration_timestamp,omitempty"`
							ResponseSource      string      `json:"response_source"`
						} `json:"call_permission_reply,omitempty"`
					} `json:"interactive,omitempty"`
					Reaction *struct {
						MessageID string `json:"message_id"`
						Emoji     string `json:"emoji"`
					} `json:"reaction,omitempty"`
					Location *struct {
						Latitude  float64 `json:"latitude"`
						Longitude float64 `json:"longitude"`
						Name      string  `json:"name,omitempty"`
						Address   string  `json:"address,omitempty"`
					} `json:"location,omitempty"`
					Contacts []struct {
						Name struct {
							FormattedName string `json:"formatted_name"`
							FirstName     string `json:"first_name,omitempty"`
							LastName      string `json:"last_name,omitempty"`
						} `json:"name"`
						Phones []struct {
							Phone string `json:"phone"`
							Type  string `json:"type,omitempty"`
						} `json:"phones,omitempty"`
					} `json:"contacts,omitempty"`
					Context *struct {
						From string `json:"from"`
						ID   string `json:"id"`
					} `json:"context,omitempty"`
				} `json:"messages,omitempty"`
				Statuses []WebhookStatus `json:"statuses,omitempty"`
				Calls    []struct {
					ID        string `json:"id"`
					From      string `json:"from"`
					To        string `json:"to"`
					Timestamp string `json:"timestamp"`
					Type      string `json:"type"`
					Event     string `json:"event"`
					Direction string `json:"direction,omitempty"`
					Session   *struct {
						SDPType string `json:"sdp_type"`
						SDP     string `json:"sdp"`
					} `json:"session,omitempty"`
					Error *struct {
						Code    int    `json:"code"`
						Message string `json:"message"`
					} `json:"error,omitempty"`
					// Terminate webhook fields
					Status    json.RawMessage `json:"status,omitempty"`
					StartTime string          `json:"start_time,omitempty"`
					EndTime   string          `json:"end_time,omitempty"`
					Duration  int             `json:"duration,omitempty"`
				} `json:"calls,omitempty"`
			} `json:"value"`
			Field string `json:"field"`
		} `json:"changes"`
	} `json:"entry"`
}

// WebhookHandler processes incoming webhook events from Meta
func (a *App) WebhookHandler(r *fastglue.Request) error {
	body := r.RequestCtx.PostBody()
	signature := r.RequestCtx.Request.Header.Peek("X-Hub-Signature-256")

	var payload WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		a.Log.Error("Failed to parse webhook payload", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid payload", nil, "")
	}

	// Verify the webhook signature up-front, before dispatching ANY event
	// (messages, statuses, template updates, calls). Meta signs every payload
	// with HMAC-SHA256 of the raw body using the Meta app secret. Accounts that
	// have an app secret are always enforced; when no secret is available the
	// require_webhook_signature flag decides between reject and warn-and-process.
	verifyPhoneID := webhookPayloadPhoneNumberID(&payload)
	appSecret := a.resolveWebhookAppSecret(verifyPhoneID)
	switch {
	case appSecret != "":
		if len(signature) == 0 || !verifyWebhookSignature(body, signature, []byte(appSecret)) {
			a.Log.Warn("Rejected webhook: missing or invalid signature", "phone_id", verifyPhoneID)
			return r.SendErrorEnvelope(fasthttp.StatusForbidden, "Invalid signature", nil, "")
		}
	case a.Config.WhatsApp.RequireWebhookSignature:
		a.Log.Warn("Rejected webhook: no app secret configured to verify signature", "phone_id", verifyPhoneID)
		return r.SendErrorEnvelope(fasthttp.StatusForbidden, "Signature verification required", nil, "")
	default:
		a.Log.Warn("Processing webhook without signature verification; configure the Meta app secret to enable it", "phone_id", verifyPhoneID)
	}

	// Process each entry
	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			// Handle template status updates
			if change.Field == "message_template_status_update" {
				a.Log.Info("Received template status update",
					"event", change.Value.Event,
					"template_name", change.Value.MessageTemplateName,
					"template_language", change.Value.MessageTemplateLanguage,
					"waba_id", entry.ID,
				)
				go a.processTemplateStatusUpdate(entry.ID, change.Value.Event, change.Value.MessageTemplateName, change.Value.MessageTemplateLanguage, change.Value.Reason)
				continue
			}

			// Handle voice call events (processed sequentially to preserve event order
			// and avoid race conditions between ringing/connect for the same call)
			if change.Field == "calls" {
				phoneNumberID := change.Value.Metadata.PhoneNumberID
				for _, call := range change.Value.Calls {
					a.Log.Info("Received call event",
						"call_id", call.ID,
						"from", call.From,
						"event", call.Event,
						"direction", call.Direction,
						"has_sdp", call.Session != nil && call.Session.SDP != "",
						"phone_number_id", phoneNumberID,
					)
					a.processCallWebhook(phoneNumberID, call)
				}

				// Business-initiated call status webhooks (RINGING/ACCEPTED/REJECTED)
				// arrive in the statuses array under field="calls"
				for _, status := range change.Value.Statuses {
					if status.Status == "" {
						continue
					}
					a.Log.Info("Received call status event",
						"call_id", status.ID,
						"status", status.Status,
					)
					a.processCallStatusWebhook(status)
				}
				continue
			}

			if change.Field != "messages" {
				continue
			}

			phoneNumberID := change.Value.Metadata.PhoneNumberID

			// Process messages
			for _, msg := range change.Value.Messages {
				a.Log.Info("Received message",
					"from", msg.From,
					"type", msg.Type,
					"phone_number_id", phoneNumberID,
				)

				// Handle call permission replies before regular message processing
				if msg.Type == "interactive" && msg.Interactive != nil &&
					msg.Interactive.Type == "call_permission_reply" &&
					msg.Interactive.CallPermissionReply != nil {
					cpr := msg.Interactive.CallPermissionReply
					expTS, err := cpr.ExpirationTimestamp.Int64()
					if err != nil {
						a.Log.Error("Failed to parse call permission expiration timestamp", "error", err, "from", msg.From)
						continue
					}
					go a.processCallPermissionReply(phoneNumberID, msg.From, &CallPermissionReplyData{
						Response:            cpr.Response,
						IsPermanent:         cpr.IsPermanent,
						ExpirationTimestamp: expTS,
						ResponseSource:      cpr.ResponseSource,
					})
					continue
				}

				// Get contact profile name
				profileName := ""
				for _, contact := range change.Value.Contacts {
					if contact.WaID == msg.From {
						profileName = contact.Profile.Name
						break
					}
				}

				// Process message asynchronously
				go a.processIncomingMessage(phoneNumberID, msg, profileName)
			}

			// Process status updates
			for _, status := range change.Value.Statuses {
				a.Log.Info("Received status update",
					"message_id", status.ID,
					"status", status.Status,
				)

				// Tracked so in-flight status callbacks are awaited on shutdown, and
			// so the callback dispatcher never grows the WaitGroup from zero
			// while WaitForBackgroundTasks is already waiting.
			a.wg.Add(1)
			go func(s WebhookStatus) {
				defer a.wg.Done()
				a.processStatusUpdate(phoneNumberID, s)
			}(status)
			}
		}
	}

	// Always respond with 200 to acknowledge receipt
	return r.SendEnvelope(map[string]string{"status": "ok"})
}

func (a *App) processIncomingMessage(phoneNumberID string, msg interface{}, profileName string) {
	// Convert msg interface to the message struct
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		a.Log.Error("Failed to marshal message", "error", err)
		return
	}

	var textMsg IncomingTextMessage
	if err := json.Unmarshal(msgBytes, &textMsg); err != nil {
		a.Log.Error("Failed to unmarshal message", "error", err)
		return
	}

	// Check for duplicate message - Meta sometimes sends the same message multiple times
	if textMsg.ID != "" {
		var existingMsg models.Message
		if err := a.DB.Where("whats_app_message_id = ?", textMsg.ID).First(&existingMsg).Error; err == nil {
			a.Log.Debug("Duplicate message detected, skipping", "message_id", textMsg.ID)
			return
		}
	}

	// Process the message with chatbot logic
	a.processIncomingMessageFull(phoneNumberID, textMsg, profileName)
}

func (a *App) processStatusUpdate(phoneNumberID string, status WebhookStatus) {
	messageID := status.ID
	statusValue := status.Status

	a.Log.Info("Processing status update", "message_id", messageID, "status", statusValue, "phone_number_id", phoneNumberID)

	// Update messages table - this also handles campaign stats via incrementCampaignStat
	a.updateMessageStatus(messageID, statusValue, status.Errors)
}

// statusPriority returns the priority of a status (higher = more progressed)
func statusPriority(status models.MessageStatus) int {
	switch status {
	case models.MessageStatusPending:
		return 0
	case models.MessageStatusSent:
		return 1
	case models.MessageStatusDelivered:
		return 2
	case models.MessageStatusRead:
		return 3
	case models.MessageStatusFailed:
		return 4 // Failed can override any status
	default:
		return -1
	}
}

// updateMessageStatus updates the status of a regular message in the messages table
func (a *App) updateMessageStatus(whatsappMsgID, statusValue string, errors []WebhookStatusError) {
	// Find the message by WhatsApp message ID
	var message models.Message
	result := a.DB.Where("whats_app_message_id = ?", whatsappMsgID).First(&message)
	if result.Error != nil {
		a.Log.Debug("No message found for status update", "whats_app_message_id", whatsappMsgID)
		return
	}

	newStatus := models.MessageStatus(statusValue)
	previousStatus := message.Status
	currentPriority := statusPriority(message.Status)
	newPriority := statusPriority(newStatus)

	// Only update if new status is a progression (higher priority) or if it's failed
	if newPriority <= currentPriority && newStatus != models.MessageStatusFailed {
		a.Log.Debug("Ignoring status update - not a progression",
			"message_id", message.ID,
			"current_status", message.Status,
			"new_status", statusValue)
		return
	}

	updates := map[string]interface{}{}

	switch newStatus {
	case models.MessageStatusSent:
		updates["status"] = models.MessageStatusSent
	case models.MessageStatusDelivered:
		updates["status"] = models.MessageStatusDelivered
	case models.MessageStatusRead:
		updates["status"] = models.MessageStatusRead
	case models.MessageStatusFailed:
		updates["status"] = models.MessageStatusFailed
		if len(errors) > 0 {
			// Prefer error_data.details (most descriptive), then Message, then Title.
			errText := errors[0].ErrorData.Details
			if errText == "" {
				errText = errors[0].Message
			}
			if errText == "" || errText == errors[0].Title {
				errText = errors[0].Title
			}

			updates["error_message"] = errText
		}
	default:
		a.Log.Debug("Ignoring message status update", "status", statusValue)
		return
	}

	if err := a.DB.Model(&message).Updates(updates).Error; err != nil {
		a.Log.Error("Failed to update message status", "error", err, "message_id", message.ID)
		return
	}

	a.Log.Info("Updated message status", "message_id", message.ID, "status", statusValue)

	// Notify the caller-supplied status callback about delivered / read / failed.
	// "sent" is already reported by the send path, so it is not repeated here.
	// Meta redelivers status webhooks, and the progression guard above lets a
	// repeated "failed" through, so only fire when the status actually changed.
	if newStatus != models.MessageStatusSent && newStatus != previousStatus {
		if callbackURL, _ := messageStatusCallbackTarget(message.Metadata); callbackURL != "" {
			var contact *models.Contact
			var loaded models.Contact
			if err := a.DB.Where("id = ?", message.ContactID).First(&loaded).Error; err == nil {
				contact = &loaded
			}
			errText, _ := updates["error_message"].(string)
			a.dispatchMessageStatusCallback(&message, contact, newStatus, whatsappMsgID, errText)
		}
	}

	// Update campaign stats and recipient status if this is a campaign message
	if message.Metadata != nil {
		if campaignID, ok := message.Metadata["campaign_id"].(string); ok && campaignID != "" {
			a.incrementCampaignStat(campaignID, statusValue)

			// Update the BulkMessageRecipient status and timestamps
			recipientUpdates := map[string]interface{}{
				"status": newStatus,
			}
			switch newStatus {
			case models.MessageStatusDelivered:
				recipientUpdates["delivered_at"] = time.Now()
			case models.MessageStatusRead:
				recipientUpdates["read_at"] = time.Now()
			case models.MessageStatusFailed:
				if errMsg, ok := updates["error_message"].(string); ok && errMsg != "" {
					recipientUpdates["error_message"] = errMsg
				}
			}
			a.DB.Model(&models.BulkMessageRecipient{}).
				Where("whats_app_message_id = ?", whatsappMsgID).
				Updates(recipientUpdates)
		}
	}

	// Broadcast status update via WebSocket
	if a.WSHub != nil {
		wsPayload := map[string]any{
			"message_id": message.ID.String(),
			"status":     statusValue,
		}
		if errMsg, ok := updates["error_message"].(string); ok && errMsg != "" {
			wsPayload["error_message"] = errMsg
		}
		a.WSHub.BroadcastToOrg(message.OrganizationID, websocket.WSMessage{
			Type:    websocket.TypeStatusUpdate,
			Payload: wsPayload,
		})
	}
}

// processTemplateStatusUpdate updates template status when Meta sends a status update webhook
func (a *App) processTemplateStatusUpdate(wabaID, event, templateName, templateLanguage, reason string) {
	if templateName == "" {
		a.Log.Warn("Template status update missing template name")
		return
	}

	// Keep status uppercase to match existing template status format
	// Events: APPROVED, REJECTED, PENDING, DISABLED, PENDING_DELETION, DELETED, REINSTATED, FLAGGED
	status := strings.ToUpper(event)

	// Find WhatsApp accounts that use this WABA ID (business_id field)
	var accounts []models.WhatsAppAccount
	if err := a.DB.Where("business_id = ?", wabaID).Find(&accounts).Error; err != nil {
		a.Log.Error("Failed to find WhatsApp accounts for WABA", "error", err, "waba_id", wabaID)
		return
	}

	if len(accounts) == 0 {
		a.Log.Warn("No WhatsApp accounts found for WABA", "waba_id", wabaID)
		return
	}

	// Update template for each account that has it
	for _, account := range accounts {
		// Find and update the template
		result := a.DB.Model(&models.Template{}).
			Where("whats_app_account = ? AND name = ? AND language = ?", account.Name, templateName, templateLanguage).
			Update("status", status)

		if result.Error != nil {
			a.Log.Error("Failed to update template status",
				"error", result.Error,
				"account", account.Name,
				"template", templateName,
				"language", templateLanguage,
			)
			continue
		}

		if result.RowsAffected > 0 {
			a.Log.Info("Updated template status from webhook",
				"account", account.Name,
				"template", templateName,
				"language", templateLanguage,
				"status", status,
				"reason", reason,
			)
		}
	}
}

// verifyWebhookSignature verifies the X-Hub-Signature-256 header from Meta.
// The signature is HMAC-SHA256 of the request body using the App Secret.
// webhookPayloadPhoneNumberID returns the first business phone number ID found
// in the payload, used to resolve which account's app secret verifies the signature.
func webhookPayloadPhoneNumberID(payload *WebhookPayload) string {
	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			if id := strings.TrimSpace(change.Value.Metadata.PhoneNumberID); id != "" {
				return id
			}
		}
	}
	return ""
}

// resolveWebhookAppSecret returns the app secret used to verify a webhook
// signature: the target account's secret when available, otherwise the
// platform-level Meta app secret (the same secret Meta signs with, shared by all
// numbers under one Meta app). Empty when neither is configured.
func (a *App) resolveWebhookAppSecret(phoneNumberID string) string {
	if phoneNumberID != "" {
		if account, err := a.getWhatsAppAccountCached(phoneNumberID); err == nil {
			if s := strings.TrimSpace(account.AppSecret); s != "" {
				return s
			}
		}
	}
	if stored, err := a.getStoredMetaOnboardingConfig(); err == nil && stored != nil {
		return strings.TrimSpace(stored.MetaAppSecret)
	}
	return ""
}

func verifyWebhookSignature(body, signature, appSecret []byte) bool {
	// Signature format: "sha256=<hex_signature>"
	prefix := []byte("sha256=")
	if !bytes.HasPrefix(signature, prefix) {
		return false
	}

	expectedSig := bytes.TrimPrefix(signature, prefix)

	// Compute HMAC-SHA256
	mac := hmac.New(sha256.New, appSecret)
	mac.Write(body)
	computedSig := make([]byte, hex.EncodedLen(mac.Size()))
	hex.Encode(computedSig, mac.Sum(nil))

	// Constant-time comparison to prevent timing attacks
	return hmac.Equal(expectedSig, computedSig)
}
