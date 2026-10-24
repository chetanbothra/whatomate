package leadutil

import (
	"testing"
	"time"

	"github.com/nikyjain/whatomate/internal/models"
	"github.com/nikyjain/whatomate/pkg/whatsapp"
	"github.com/stretchr/testify/assert"
)

func TestFlattenFieldData(t *testing.T) {
	fields := []whatsapp.LeadFieldData{
		{Name: "full_name", Values: []string{"John Doe"}},
		{Name: "phone_number", Values: []string{"+91 98765 43210"}},
		{Name: "interests", Values: []string{"Coding", "Gaming", "Music"}},
		{Name: "empty_val", Values: []string{}},
	}

	flattened := FlattenFieldData(fields)
	assert.Equal(t, "John Doe", flattened["full_name"])
	assert.Equal(t, "+91 98765 43210", flattened["phone_number"])
	assert.Equal(t, "Coding, Gaming, Music", flattened["interests"])
	assert.Equal(t, "", flattened["empty_val"])
}

func TestFirstWord(t *testing.T) {
	assert.Equal(t, "Deepika", FirstWord("Deepika Jain"))
	assert.Equal(t, "Alice", FirstWord("   Alice   "))
	assert.Equal(t, "Bob", FirstWord("Bob"))
	assert.Equal(t, "", FirstWord(""))
	assert.Equal(t, "", FirstWord("   "))
}

func TestCleanParamValue(t *testing.T) {
	assert.Equal(t, "Hello World", CleanParamValue("  Hello   World  \n\t"))
	assert.Equal(t, "Line 1 Line 2", CleanParamValue("Line 1\nLine 2"))

	longStr := string(make([]byte, 2000))
	cleaned := CleanParamValue(longStr)
	assert.LessOrEqual(t, len(cleaned), 1024)
}

func TestIsAffirmativeConsent(t *testing.T) {
	assert.True(t, IsAffirmativeConsent("Yes"))
	assert.True(t, IsAffirmativeConsent("YES"))
	assert.True(t, IsAffirmativeConsent("1"))
	assert.True(t, IsAffirmativeConsent("true"))
	assert.True(t, IsAffirmativeConsent("I agree"))
	assert.True(t, IsAffirmativeConsent("Agree"))
	assert.True(t, IsAffirmativeConsent("y"))

	assert.False(t, IsAffirmativeConsent("No"))
	assert.False(t, IsAffirmativeConsent("0"))
	assert.False(t, IsAffirmativeConsent("false"))
	assert.False(t, IsAffirmativeConsent(""))
}

func TestBuildTemplateData(t *testing.T) {
	lead := &models.Lead{
		Name:          "Deepika Jain",
		Phone:         "919876543210",
		Email:         "deepika@example.com",
		AdID:          "ad123",
		AdName:        "Summer Ad",
		CampaignName:  "Anchor Bootcamp",
		FormID:        "form999",
		LeadCreatedAt: time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC),
		FieldData: models.JSONB{
			"city": "Bengaluru",
		},
	}
	mapping := &models.LeadFormMapping{
		FormName: "Workshop Form",
	}

	data := BuildTemplateData(lead, mapping, "My Facebook Page")
	leadMap, ok := data["lead"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "Deepika Jain", leadMap["name"])
	assert.Equal(t, "Deepika", leadMap["first_name"])
	assert.Equal(t, "919876543210", leadMap["phone"])
	assert.Equal(t, "deepika@example.com", leadMap["email"])
	assert.NotEmpty(t, leadMap["created_date"])

	adMap := data["ad"].(map[string]interface{})
	assert.Equal(t, "Summer Ad", adMap["name"])

	formMap := data["form"].(map[string]interface{})
	assert.Equal(t, "Workshop Form", formMap["name"])

	pageMap := data["page"].(map[string]interface{})
	assert.Equal(t, "My Facebook Page", pageMap["name"])
}
