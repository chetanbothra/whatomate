package whatsapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// LeadFieldData represents a single field answer in Meta Lead Ads
type LeadFieldData struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// LeadDetails represents the full lead object fetched from Graph API
type LeadDetails struct {
	ID           string          `json:"id"`
	CreatedTime  string          `json:"created_time"`
	AdID         string          `json:"ad_id"`
	AdName       string          `json:"ad_name"`
	AdsetName    string          `json:"adset_name"`
	CampaignName string          `json:"campaign_name"`
	FormID       string          `json:"form_id"`
	Platform     string          `json:"platform"`
	IsOrganic    bool            `json:"is_organic"`
	FieldData    []LeadFieldData `json:"field_data"`
}

// LeadForm represents a Leadgen form on a Facebook Page
type LeadForm struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	Status     string                 `json:"status"`
	Questions  []LeadFormQuestion     `json:"questions"`
	LeadsCount int                    `json:"leads_count"`
}

// LeadFormQuestion represents a question in a leadgen form
type LeadFormQuestion struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Type  string `json:"type"`
}

// LeadgenFormsResponse is the paged response from GET /{page_id}/leadgen_forms
type LeadgenFormsResponse struct {
	Data   []LeadForm `json:"data"`
	Paging struct {
		Cursors struct {
			Before string `json:"before"`
			After  string `json:"after"`
		} `json:"cursors"`
		Next string `json:"next"`
	} `json:"paging"`
}

// LeadListResponse is the response from GET /{form_id}/leads
type LeadListResponse struct {
	Data   []LeadDetails `json:"data"`
	Paging struct {
		Cursors struct {
			Before string `json:"before"`
			After  string `json:"after"`
		} `json:"cursors"`
		Next string `json:"next"`
	} `json:"paging"`
}

// PageProfile represents basic Facebook Page information
type PageProfile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AccessToken string `json:"access_token,omitempty"`
}

// SubscribedAppsResponse represents response from subscribed_apps calls
type SubscribedAppsResponse struct {
	Success bool `json:"success"`
}

// GetLead fetches lead details from Meta Graph API
func (c *Client) GetLead(ctx context.Context, apiVersion, leadgenID, pageAccessToken string) (*LeadDetails, error) {
	if apiVersion == "" {
		apiVersion = "v21.0"
	}
	fields := "id,created_time,ad_id,ad_name,adset_name,campaign_name,form_id,platform,is_organic,field_data"
	reqURL := fmt.Sprintf("%s/%s/%s?fields=%s", c.getBaseURL(), apiVersion, url.PathEscape(leadgenID), url.QueryEscape(fields))

	respBytes, err := c.doRequest(ctx, "GET", reqURL, nil, pageAccessToken)
	if err != nil {
		return nil, err
	}

	var lead LeadDetails
	if err := json.Unmarshal(respBytes, &lead); err != nil {
		return nil, fmt.Errorf("failed to parse lead response: %w", err)
	}

	return &lead, nil
}

// GetPage gets Facebook Page basic info (e.g. name, access_token)
func (c *Client) GetPage(ctx context.Context, apiVersion, pageID, pageAccessToken string) (*PageProfile, error) {
	if apiVersion == "" {
		apiVersion = "v21.0"
	}
	reqURL := fmt.Sprintf("%s/%s/%s?fields=id,name,access_token", c.getBaseURL(), apiVersion, url.PathEscape(pageID))

	respBytes, err := c.doRequest(ctx, "GET", reqURL, nil, pageAccessToken)
	if err != nil {
		return nil, err
	}

	var page PageProfile
	if err := json.Unmarshal(respBytes, &page); err != nil {
		return nil, fmt.Errorf("failed to parse page response: %w", err)
	}

	return &page, nil
}

// GetPageForms lists leadgen forms for a page
func (c *Client) GetPageForms(ctx context.Context, apiVersion, pageID, pageAccessToken string) ([]LeadForm, error) {
	if apiVersion == "" {
		apiVersion = "v21.0"
	}
	fields := "id,name,status,questions,leads_count"
	reqURL := fmt.Sprintf("%s/%s/%s/leadgen_forms?fields=%s&limit=100", c.getBaseURL(), apiVersion, url.PathEscape(pageID), url.QueryEscape(fields))

	respBytes, err := c.doRequest(ctx, "GET", reqURL, nil, pageAccessToken)
	if err != nil {
		return nil, err
	}

	var formsResp LeadgenFormsResponse
	if err := json.Unmarshal(respBytes, &formsResp); err != nil {
		return nil, fmt.Errorf("failed to parse leadgen forms: %w", err)
	}

	return formsResp.Data, nil
}

// GetFormLeads fetches leads for a form with optional time filtering
func (c *Client) GetFormLeads(ctx context.Context, apiVersion, formID, pageAccessToken string, sinceUnix int64, limit int) (*LeadListResponse, error) {
	if apiVersion == "" {
		apiVersion = "v21.0"
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	fields := "id,created_time,ad_id,ad_name,adset_name,campaign_name,form_id,platform,is_organic,field_data"
	reqURL := fmt.Sprintf("%s/%s/%s/leads?fields=%s&limit=%d", c.getBaseURL(), apiVersion, url.PathEscape(formID), url.QueryEscape(fields), limit)

	if sinceUnix > 0 {
		filterJSON := fmt.Sprintf(`[{"field":"time_created","operator":"GREATER_THAN","value":%d}]`, sinceUnix)
		reqURL += "&filtering=" + url.QueryEscape(filterJSON)
	}

	respBytes, err := c.doRequest(ctx, "GET", reqURL, nil, pageAccessToken)
	if err != nil {
		return nil, err
	}

	var listResp LeadListResponse
	if err := json.Unmarshal(respBytes, &listResp); err != nil {
		return nil, fmt.Errorf("failed to parse form leads: %w", err)
	}

	return &listResp, nil
}

// SubscribePage installs the app on the Page to receive leadgen webhook events
func (c *Client) SubscribePage(ctx context.Context, apiVersion, pageID, pageAccessToken string) error {
	if apiVersion == "" {
		apiVersion = "v21.0"
	}
	reqURL := fmt.Sprintf("%s/%s/%s/subscribed_apps?subscribed_fields=leadgen", c.getBaseURL(), apiVersion, url.PathEscape(pageID))

	_, err := c.doRequest(ctx, "POST", reqURL, nil, pageAccessToken)
	return err
}

// UnsubscribePage removes the app subscription from the Page
func (c *Client) UnsubscribePage(ctx context.Context, apiVersion, pageID, pageAccessToken string) error {
	if apiVersion == "" {
		apiVersion = "v21.0"
	}
	reqURL := fmt.Sprintf("%s/%s/%s/subscribed_apps", c.getBaseURL(), apiVersion, url.PathEscape(pageID))

	_, err := c.doRequest(ctx, "DELETE", reqURL, nil, pageAccessToken)
	return err
}
