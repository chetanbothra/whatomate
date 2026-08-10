package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nikyjain/whatomate/internal/models"
)

// Metadata keys used to persist the per-message status callback configuration.
// They are stored on the outgoing message so that late status updates arriving
// from Meta (delivered/read/failed) can still reach the original caller.
const (
	metaKeyStatusCallbackURL   = "status_callback_url"
	metaKeyStatusCallbackToken = "status_callback_bearer_token"
)

// statusCallbackTimeout bounds how long a single callback POST may take.
const statusCallbackTimeout = 15 * time.Second

// maxConcurrentStatusCallbacks bounds how many callback POSTs may be in flight
// at once. Without it, a bulk send to a slow receiver would open one socket per
// status change and exhaust file descriptors. Excess callbacks queue rather than
// being dropped, so nothing is lost — they are just delivered a little later.
const maxConcurrentStatusCallbacks = 10

var statusCallbackSem = make(chan struct{}, maxConcurrentStatusCallbacks)

// messageStatusCallbackPayload is the JSON body posted to status_callback_url
// whenever the delivery status of an outgoing message changes.
type messageStatusCallbackPayload struct {
	MessageID         string    `json:"message_id"`
	WhatsAppMessageID string    `json:"whatsapp_message_id,omitempty"`
	Status            string    `json:"status"`
	ErrorMessage      string    `json:"error_message,omitempty"`
	ContactID         string    `json:"contact_id,omitempty"`
	ContactPhone      string    `json:"contact_phone,omitempty"`
	ContactName       string    `json:"contact_name,omitempty"`
	WhatsAppAccount   string    `json:"whatsapp_account,omitempty"`
	TemplateName      string    `json:"template_name,omitempty"`
	Timestamp         time.Time `json:"timestamp"`
}

// validateCallbackURL ensures a caller-supplied callback URL is an absolute
// http(s) URL. Anything else (file://, relative paths, empty host) is rejected
// at request time so the caller learns about it immediately.
func validateCallbackURL(field, raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("%s is not a valid URL", field)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%s must start with http:// or https://", field)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%s must include a host", field)
	}
	return nil
}

// messageStatusCallbackTarget returns the callback URL and bearer token stored
// on a message, or an empty URL when the caller did not request callbacks.
func messageStatusCallbackTarget(metadata models.JSONB) (string, string) {
	callbackURL := strings.TrimSpace(stringFromJSONB(metadata, metaKeyStatusCallbackURL))
	if callbackURL == "" {
		return "", ""
	}
	return callbackURL, stringFromJSONB(metadata, metaKeyStatusCallbackToken)
}

// dispatchMessageStatusCallback posts a status change to the caller-supplied
// status_callback_url. It is a no-op when the message carries no callback URL.
//
// The POST runs in a background goroutine so neither the WhatsApp send path nor
// the Meta webhook handler is blocked by a slow receiver. Failures are logged
// and not retried; the WhatsApp message itself is unaffected either way.
func (a *App) dispatchMessageStatusCallback(msg *models.Message, contact *models.Contact, status models.MessageStatus, wamid, errorMessage string) {
	if msg == nil {
		return
	}

	callbackURL, bearerToken := messageStatusCallbackTarget(msg.Metadata)
	if callbackURL == "" {
		return
	}

	if wamid == "" {
		wamid = msg.WhatsAppMessageID
	}

	payload := messageStatusCallbackPayload{
		MessageID:         msg.ID.String(),
		WhatsAppMessageID: wamid,
		Status:            string(status),
		ErrorMessage:      errorMessage,
		WhatsAppAccount:   msg.WhatsAppAccount,
		TemplateName:      msg.TemplateName,
		Timestamp:         time.Now().UTC(),
	}

	if contact != nil {
		payload.ContactID = contact.ID.String()
		payload.ContactPhone = contact.PhoneNumber
		payload.ContactName = contact.ProfileName
	} else if msg.ContactID != uuid.Nil {
		payload.ContactID = msg.ContactID.String()
	}

	msgID := msg.ID

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()

		statusCallbackSem <- struct{}{}
		defer func() { <-statusCallbackSem }()

		result := a.postJSONCallback(callbackURL, bearerToken, payload)
		if successful, _ := result["successful"].(bool); successful {
			a.Log.Info("Message status callback delivered",
				"message_id", msgID, "status", status, "url", callbackURL)
			return
		}
		a.Log.Error("Message status callback failed",
			"message_id", msgID, "status", status, "url", callbackURL,
			"status_code", result["status_code"], "error", result["error"])
	}()
}

// postJSONCallback POSTs a JSON payload to an external callback URL and reports
// the outcome. It never returns an error: callbacks are best-effort, and the
// returned JSONB is used for logging and troubleshooting.
func (a *App) postJSONCallback(callbackURL, bearerToken string, payload any) models.JSONB {
	result := models.JSONB{
		"url":        callbackURL,
		"sent_at":    time.Now().Format(time.RFC3339Nano),
		"successful": false,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		result["error"] = err.Error()
		return result
	}

	client := a.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	ctx, cancel := context.WithTimeout(context.Background(), statusCallbackTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, bytes.NewReader(body))
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	req.Header.Set("Content-Type", "application/json")
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	defer resp.Body.Close()

	result["status_code"] = resp.StatusCode
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result["successful"] = true
		return result
	}

	limitedBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	result["error"] = strings.TrimSpace(string(limitedBody))
	return result
}
