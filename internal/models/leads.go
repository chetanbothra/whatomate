package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/nikyjain/whatomate/internal/crypto"
)

// LeadSource represents one connected Facebook Page
type LeadSource struct {
	BaseModel
	OrganizationID     uuid.UUID  `gorm:"type:uuid;index;not null" json:"organization_id"`
	Name               string     `gorm:"size:100;not null" json:"name"`
	PageID             string     `gorm:"size:50;not null;uniqueIndex:idx_lead_src_org_page" json:"page_id"`
	PageName           string     `gorm:"size:255" json:"page_name"`
	AppID              string     `gorm:"size:100" json:"app_id"`
	AppSecret          string     `gorm:"size:255" json:"-"` // encrypted; HMAC verify
	PageAccessToken    string     `gorm:"type:text;not null" json:"-"` // encrypted
	WebhookVerifyToken string     `gorm:"size:64;uniqueIndex" json:"webhook_verify_token"`
	APIVersion         string     `gorm:"size:20;default:'v21.0'" json:"api_version"`
	DefaultCountryCode string     `gorm:"size:4;default:'91'" json:"default_country_code"`
	IsActive           bool       `gorm:"default:true" json:"is_active"`
	Subscribed         bool       `gorm:"default:false" json:"subscribed"`
	LastEventAt        *time.Time `json:"last_event_at,omitempty"`
	LastSyncedAt       *time.Time `json:"last_synced_at,omitempty"`
	CreatedByID        *uuid.UUID `gorm:"type:uuid" json:"created_by_id,omitempty"`
}

func (LeadSource) TableName() string {
	return "lead_sources"
}

// DecryptSecrets decrypts the encrypted PageAccessToken and AppSecret fields.
func (s *LeadSource) DecryptSecrets(encryptionKey string) {
	crypto.DecryptFields(encryptionKey, &s.PageAccessToken, &s.AppSecret)
}

// EncryptSecrets encrypts the plain PageAccessToken and AppSecret fields.
func (s *LeadSource) EncryptSecrets(encryptionKey string) {
	crypto.EncryptFields(encryptionKey, &s.PageAccessToken, &s.AppSecret)
}

// LeadFormMapping defines what to do for leads from one form (FormID "*" = page default)
type LeadFormMapping struct {
	BaseModel
	OrganizationID   uuid.UUID   `gorm:"type:uuid;index;not null" json:"organization_id"`
	LeadSourceID     uuid.UUID   `gorm:"type:uuid;not null;uniqueIndex:idx_lfm_src_form" json:"lead_source_id"`
	FormID           string      `gorm:"size:50;not null;uniqueIndex:idx_lfm_src_form" json:"form_id"`
	FormName         string      `gorm:"size:255" json:"form_name"`
	IsActive         bool        `gorm:"default:true" json:"is_active"`
	WhatsAppAccount  string      `gorm:"size:100" json:"whatsapp_account"` // WhatsAppAccount.Name; "" = default outgoing
	TemplateID       *uuid.UUID  `gorm:"type:uuid" json:"template_id"`     // nil = store lead, don't send
	TemplateParams   JSONB       `gorm:"type:jsonb;default:'{}'" json:"template_params"` // {"1":"{{lead.first_name}}"}
	HeaderParams     JSONB       `gorm:"type:jsonb;default:'{}'" json:"header_params"`
	ButtonParams     JSONB       `gorm:"type:jsonb;default:'{}'" json:"button_params"`
	HeaderMediaID    string      `gorm:"size:100" json:"header_media_id"` // for IMAGE/VIDEO/DOC headers
	PhoneField       string      `gorm:"size:100;default:'phone_number'" json:"phone_field"`
	NameField        string      `gorm:"size:100;default:'full_name'" json:"name_field"`
	ConsentField     string      `gorm:"size:100" json:"consent_field"` // "" = not enforced
	Tags             StringArray `gorm:"type:jsonb;default:'[]'" json:"tags"`
	AssignUserID     *uuid.UUID  `gorm:"type:uuid" json:"assign_user_id,omitempty"`
	AssignTeamID     *uuid.UUID  `gorm:"type:uuid" json:"assign_team_id,omitempty"`
	SendDelaySeconds int         `gorm:"default:0" json:"send_delay_seconds"`
	DedupeHours      int         `gorm:"default:24" json:"dedupe_hours"` // skip resend to same phone+form
	SkipIfOptedOut   bool        `gorm:"default:true" json:"skip_if_opted_out"`

	// Relations
	Template *Template `gorm:"foreignKey:TemplateID" json:"template,omitempty"`
}

func (LeadFormMapping) TableName() string {
	return "lead_form_mappings"
}

// Lead represents one leadgen event; also acts as the work queue (status + NextAttemptAt)
type Lead struct {
	BaseModel
	OrganizationID uuid.UUID  `gorm:"type:uuid;index:idx_lead_org_created;not null" json:"organization_id"`
	LeadSourceID   uuid.UUID  `gorm:"type:uuid;index;not null" json:"lead_source_id"`
	MappingID      *uuid.UUID `gorm:"type:uuid" json:"mapping_id,omitempty"`
	LeadgenID      string     `gorm:"size:50;not null;uniqueIndex" json:"leadgen_id"` // idempotency key
	PageID         string     `gorm:"size:50" json:"page_id"`
	FormID         string     `gorm:"size:50;index" json:"form_id"`
	AdID           string     `gorm:"size:50" json:"ad_id"`
	AdName         string     `gorm:"size:255" json:"ad_name"`
	CampaignName   string     `gorm:"size:255" json:"campaign_name"`
	Platform       string     `gorm:"size:20" json:"platform"` // fb | ig
	IsOrganic      bool       `json:"is_organic"`
	FieldData      JSONB      `gorm:"type:jsonb;default:'{}'" json:"field_data"` // flattened name->value
	Name           string     `gorm:"size:255" json:"name"`
	PhoneRaw       string     `gorm:"size:50" json:"phone_raw"`
	Phone          string     `gorm:"size:20;index" json:"phone"` // normalised, digits only
	Email          string     `gorm:"size:255" json:"email"`
	ContactID      *uuid.UUID `gorm:"type:uuid;index" json:"contact_id,omitempty"`
	MessageID      *uuid.UUID `gorm:"type:uuid" json:"message_id,omitempty"`
	Status         string     `gorm:"size:30;index:idx_lead_status_next;not null" json:"status"`
	NextAttemptAt  *time.Time `gorm:"index:idx_lead_status_next" json:"next_attempt_at,omitempty"`
	Attempts       int        `gorm:"default:0" json:"attempts"`
	LastError      string     `gorm:"type:text" json:"last_error,omitempty"`
	LeadCreatedAt  time.Time  `gorm:"index:idx_lead_org_created" json:"lead_created_at"` // Meta created_time
	SentAt         *time.Time `json:"sent_at,omitempty"`

	// Relations
	LeadSource *LeadSource      `gorm:"foreignKey:LeadSourceID" json:"lead_source,omitempty"`
	Mapping    *LeadFormMapping `gorm:"foreignKey:MappingID" json:"mapping,omitempty"`
	Contact    *Contact         `gorm:"foreignKey:ContactID" json:"contact,omitempty"`
	Message    *Message         `gorm:"foreignKey:MessageID" json:"message,omitempty"`
}

func (Lead) TableName() string {
	return "leads"
}
