package leadutil

import (
	"errors"
	"fmt"
	"strings"

	"github.com/nikyjain/whatomate/pkg/whatsapp"
)

// Classify classifies errors from Meta Graph API / WhatsApp Cloud API into retryable vs permanent failures
func Classify(err error) (retryable bool, reason string) {
	if err == nil {
		return false, ""
	}

	errMsg := err.Error()

	// Check if it's a typed MetaAPIError
	var metaErr *whatsapp.MetaAPIError
	if errors.As(err, &metaErr) {
		code := metaErr.Error.Code
		subcode := metaErr.Error.ErrorSubcode

		// Permanent token invalidation
		if code == 190 {
			return false, fmt.Sprintf("Graph API token expired or invalid (code %d)", code)
		}
		// Leads retrieval permissions / Leads Access Manager
		if code == 200 || code == 10 {
			return false, fmt.Sprintf("Missing leads_retrieval permission or Leads Access Manager restriction (code %d)", code)
		}
		// Lead deleted / not found
		if code == 100 {
			return false, fmt.Sprintf("Lead or object not found (code %d)", code)
		}

		// Rate limiting (retryable)
		if code == 4 || code == 17 || code == 32 || code == 613 {
			return true, fmt.Sprintf("Graph API rate limit (code %d)", code)
		}

		// WhatsApp Cloud API errors
		switch code {
		case 131056, 130429:
			// Pair rate limit or throughput limit
			return true, fmt.Sprintf("WhatsApp rate/throughput limit (code %d)", code)
		case 131026:
			// Not on WhatsApp / undeliverable
			return false, fmt.Sprintf("Recipient not on WhatsApp (code %d)", code)
		case 132000, 132001, 132018:
			// Template params mismatch or formatting error
			return false, fmt.Sprintf("Template parameters or name invalid (code %d: %s)", code, metaErr.Error.Message)
		case 131049:
			// Marketing frequency cap
			return false, fmt.Sprintf("Marketing frequency cap reached (code %d)", code)
		case 131050:
			// User stopped marketing messages
			return false, fmt.Sprintf("User opted out of marketing (code %d)", code)
		}

		if subcode > 0 {
			errMsg = fmt.Sprintf("Meta error %d (%d): %s", code, subcode, metaErr.Error.Message)
		} else {
			errMsg = fmt.Sprintf("Meta error %d: %s", code, metaErr.Error.Message)
		}
	}

	lower := strings.ToLower(errMsg)

	// Check standard HTTP / network errors
	if strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "deadline exceeded") ||
		strings.Contains(lower, "500") ||
		strings.Contains(lower, "502") ||
		strings.Contains(lower, "503") ||
		strings.Contains(lower, "504") {
		return true, errMsg
	}

	// Meta codes inside error message string if not unmarshaled into MetaAPIError
	if strings.Contains(lower, "190") {
		return false, "Meta access token expired or invalid"
	}
	if strings.Contains(lower, "131026") {
		return false, "Recipient not on WhatsApp"
	}
	if strings.Contains(lower, "131049") {
		return false, "Marketing frequency cap reached"
	}
	if strings.Contains(lower, "131050") {
		return false, "User opted out of marketing"
	}
	if strings.Contains(lower, "132000") || strings.Contains(lower, "132001") || strings.Contains(lower, "132018") {
		return false, "Template parameter error"
	}

	// Default fallback: non-retryable if unknown permanent error
	return false, errMsg
}
