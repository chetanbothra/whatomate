package leadutil

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/nikyjain/whatomate/internal/models"
	"github.com/nikyjain/whatomate/pkg/whatsapp"
)

var (
	// Multi-space, newline, tab regex to collapse into single space
	whitespaceRegex = regexp.MustCompile(`[\r\n\t]+|\s{2,}`)
)

// FlattenFieldData converts Graph API field_data array into map[name]value,
// joining multi-values with ", "
func FlattenFieldData(fields []whatsapp.LeadFieldData) map[string]interface{} {
	m := make(map[string]interface{})
	for _, f := range fields {
		if len(f.Values) == 0 {
			m[f.Name] = ""
		} else if len(f.Values) == 1 {
			m[f.Name] = f.Values[0]
		} else {
			m[f.Name] = strings.Join(f.Values, ", ")
		}
	}
	return m
}

// FirstWord returns the first whitespace-separated word in s
func FirstWord(s string) string {
	parts := strings.Fields(strings.TrimSpace(s))
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

// BuildTemplateData constructs the standard evaluation context for rendering Lead Ads template variables
func BuildTemplateData(lead *models.Lead, mapping *models.LeadFormMapping, pageName string) map[string]interface{} {
	ist, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		ist = time.FixedZone("IST", 5*3600+30*60)
	}

	createdTime := lead.LeadCreatedAt
	if createdTime.IsZero() {
		createdTime = time.Now()
	}

	formName := ""
	if mapping != nil {
		formName = mapping.FormName
	}

	fieldDataMap := map[string]interface{}{}
	if lead.FieldData != nil {
		for k, v := range lead.FieldData {
			fieldDataMap[k] = v
		}
	}

	return map[string]interface{}{
		"lead": map[string]interface{}{
			"name":         lead.Name,
			"first_name":   FirstWord(lead.Name),
			"phone":        lead.Phone,
			"email":        lead.Email,
			"created_date": createdTime.In(ist).Format("02 Jan 2006"),
		},
		"field":    fieldDataMap,
		"ad":       map[string]interface{}{"id": lead.AdID, "name": lead.AdName},
		"campaign": map[string]interface{}{"name": lead.CampaignName},
		"form":     map[string]interface{}{"id": lead.FormID, "name": formName},
		"page":     map[string]interface{}{"name": pageName},
	}
}

// CleanParamValue trims, collapses whitespace, and limits length to 1024 chars
func CleanParamValue(val string) string {
	s := strings.TrimSpace(val)
	s = whitespaceRegex.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	if len(s) > 1024 {
		s = s[:1024]
	}
	return s
}

// IsAffirmativeConsent checks if value is an affirmative consent (yes, true, 1, i agree)
func IsAffirmativeConsent(val string) bool {
	v := strings.ToLower(strings.TrimSpace(val))
	switch v {
	case "yes", "true", "1", "i agree", "agree", "y":
		return true
	default:
		return false
	}
}
