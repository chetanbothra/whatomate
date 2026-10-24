export interface LeadSource {
  id: string;
  organization_id: string;
  name: string;
  page_id: string;
  page_name: string;
  app_id: string;
  webhook_verify_token: string;
  webhook_url?: string;
  api_version: string;
  default_country_code: string;
  is_active: boolean;
  subscribed: boolean;
  last_event_at?: string;
  last_synced_at?: string;
  created_at: string;
  updated_at: string;
}

export interface LeadFormQuestion {
  key: string;
  label: string;
  type: string;
}

export interface LeadFormMapping {
  id: string;
  organization_id: string;
  lead_source_id: string;
  form_id: string;
  form_name: string;
  is_active: boolean;
  whatsapp_account: string;
  template_id?: string;
  template_params: Record<string, string>;
  header_params: Record<string, string>;
  button_params: Record<string, string>;
  header_media_id: string;
  phone_field: string;
  name_field: string;
  consent_field: string;
  tags: string[];
  assign_user_id?: string;
  assign_team_id?: string;
  send_delay_seconds: number;
  dedupe_hours: number;
  skip_if_opted_out: boolean;
  template?: {
    id: string;
    name: string;
    language: string;
    category: string;
    status: string;
    body: string;
    header_type?: string;
    header_content?: string;
  };
}

export interface FormWithMapping {
  id: string;
  name: string;
  status: string;
  questions: LeadFormQuestion[];
  leads_count: number;
  mapping?: LeadFormMapping;
}

export interface Lead {
  id: string;
  organization_id: string;
  lead_source_id: string;
  mapping_id?: string;
  leadgen_id: string;
  page_id: string;
  form_id: string;
  ad_id: string;
  ad_name: string;
  campaign_name: string;
  platform: string;
  is_organic: boolean;
  field_data: Record<string, any>;
  name: string;
  phone_raw: string;
  phone: string;
  email: string;
  contact_id?: string;
  message_id?: string;
  status: string;
  next_attempt_at?: string;
  attempts: number;
  last_error?: string;
  lead_created_at: string;
  sent_at?: string;
  created_at: string;
  lead_source?: LeadSource;
  mapping?: LeadFormMapping;
  contact?: {
    id: string;
    profile_name: string;
    phone_number: string;
  };
  message?: {
    id: string;
    status: string;
    whatsapp_message_id?: string;
  };
}

export interface LeadStats {
  today: { status: string; count: number }[];
  total: { status: string; count: number }[];
}
