import axios, {
  type AxiosInstance,
  type AxiosError,
  type InternalAxiosRequestConfig,
} from "axios";
import { reportApiError } from "@/services/rollbar";

// Get base path from server-injected config or fallback
const basePath = ((window as any).__BASE_PATH__ ?? "").replace(/\/$/, "");
const API_BASE_URL = import.meta.env.VITE_API_URL || `${basePath}/api`;

export const api: AxiosInstance = axios.create({
  baseURL: API_BASE_URL,
  timeout: 30000,
  withCredentials: true,
  headers: {
    "Content-Type": "application/json",
  },
});

// Helper to read a cookie by name
function getCookie(name: string): string | null {
  const match = document.cookie.match(
    new RegExp("(?:^|; )" + name + "=([^;]*)"),
  );
  return match ? decodeURIComponent(match[1]) : null;
}

/**
 * Build standard headers for native fetch() calls.
 * Includes X-Organization-ID (for org switching) and optionally X-CSRF-Token (for mutating requests).
 */
export function getRequestHeaders(opts?: {
  csrf?: boolean;
}): Record<string, string> {
  const headers: Record<string, string> = {};
  const selectedOrgId = localStorage.getItem("selected_organization_id");
  if (selectedOrgId) {
    headers["X-Organization-ID"] = selectedOrgId;
  }
  if (opts?.csrf) {
    const csrfToken = getCookie("whm_csrf");
    if (csrfToken) {
      headers["X-CSRF-Token"] = csrfToken;
    }
  }
  return headers;
}

// Request interceptor to add CSRF token and organization header
api.interceptors.request.use(
  (config: InternalAxiosRequestConfig) => {
    // Add CSRF token on mutating requests (cookie-based auth sends cookies automatically)
    const method = (config.method || "").toUpperCase();
    if (
      method === "POST" ||
      method === "PUT" ||
      method === "DELETE" ||
      method === "PATCH"
    ) {
      const csrfToken = getCookie("whm_csrf");
      if (csrfToken) {
        config.headers["X-CSRF-Token"] = csrfToken;
      }
    }
    // Add organization override header for org switching
    const selectedOrgId = localStorage.getItem("selected_organization_id");
    if (selectedOrgId) {
      config.headers["X-Organization-ID"] = selectedOrgId;
    }
    return config;
  },
  (error: AxiosError) => {
    return Promise.reject(error);
  },
);

// Token refresh mutex — ensures only one refresh runs at a time.
// Without this, multiple concurrent 401s each trigger a refresh, but the
// single-use refresh token (JTI deleted from Redis) causes all but the first
// to fail, which clears auth and logs the user out.
let isRefreshing = false;
let refreshSubscribers: Array<(success: boolean) => void> = [];

function onRefreshComplete(success: boolean) {
  refreshSubscribers.forEach((cb) => cb(success));
  refreshSubscribers = [];
}

// Response interceptor for error handling
api.interceptors.response.use(
  (response) => response,
  async (error: AxiosError) => {
    const originalRequest = error.config as InternalAxiosRequestConfig & {
      _retry?: boolean;
    };
    let refreshAttempted = false;

    // Skip token refresh logic for auth endpoints
    const isAuthEndpoint = originalRequest?.url?.startsWith("/auth/");

    // Handle 401 errors - try to refresh token (but not for auth endpoints)
    if (
      error.response?.status === 401 &&
      !originalRequest._retry &&
      !isAuthEndpoint
    ) {
      refreshAttempted = true;
      originalRequest._retry = true;

      // If a refresh is already in flight, queue this request to wait for it
      if (isRefreshing) {
        return new Promise((resolve, reject) => {
          refreshSubscribers.push((success: boolean) => {
            if (success) {
              resolve(api(originalRequest));
            } else {
              reject(error);
            }
          });
        });
      }

      isRefreshing = true;

      try {
        // Browser sends whm_refresh cookie automatically via withCredentials
        await axios.post(
          `${API_BASE_URL}/auth/refresh`,
          {},
          { withCredentials: true },
        );

        // Cookies are updated by the server response — notify waiting requests
        onRefreshComplete(true);
        isRefreshing = false;

        // Retry the original request
        return api(originalRequest);
      } catch {
        // Refresh failed — notify waiting requests and redirect to login
        onRefreshComplete(false);
        isRefreshing = false;

        localStorage.removeItem("user");
        localStorage.removeItem("auth_token");
        localStorage.removeItem("refresh_token");
        window.location.href = basePath + "/login";
      }
    }

    reportApiError(error, {
      refresh_attempted: refreshAttempted,
      is_auth_endpoint: isAuthEndpoint,
    });

    return Promise.reject(error);
  },
);

// API service methods
export const authService = {
  getWSToken: () => api.get("/auth/ws-token"),
};

export const usersService = {
  list: (params?: { search?: string; page?: number; limit?: number }) =>
    api.get("/users", { params }),
  create: (data: {
    email: string;
    password: string;
    full_name: string;
    role_id?: string;
  }) => api.post("/users", data),
  update: (
    id: string,
    data: {
      email?: string;
      password?: string;
      full_name?: string;
      role_id?: string;
      is_active?: boolean;
    },
  ) => api.put(`/users/${id}`, data),
  delete: (id: string) => api.delete(`/users/${id}`),
  me: () => api.get("/me"),
  updateSettings: (data: {
    email_notifications: boolean;
    new_message_alerts: boolean;
    campaign_updates: boolean;
  }) => api.put("/me/settings", data),
  changePassword: (data: { current_password: string; new_password: string }) =>
    api.put("/me/password", data),
  updateAvailability: (isAvailable: boolean) =>
    api.put("/me/availability", { is_available: isAvailable }),
  listMyOrganizations: () => api.get("/me/organizations"),
};

export const apiKeysService = {
  list: (params?: { search?: string; page?: number; limit?: number }) =>
    api.get<{ api_keys: any[]; total?: number }>("/api-keys", { params }),
  create: (data: { name: string; expires_at?: string; organization_id?: string; is_super_admin_key?: boolean }) =>
    api.post("/api-keys", data),
  delete: (id: string) => api.delete(`/api-keys/${id}`),
};

export interface ChatRepairCandidate {
  sample_messages?: ChatRepairSampleMessage[];
  contact_id: string;
  phone_number: string;
  profile_name: string;
  current_org_id: string;
  current_org_name?: string;
  current_account: string;
  target_org_id: string;
  target_org_name?: string;
  target_account: string;
  action: "move" | "merge_required" | "conflict";
  reason: string;
  affected_message_count: number;
  last_message_at?: string;
  phone_number_id: string;
  target_contact_id?: string;
}

export interface ChatRepairSampleMessage {
  direction: string;
  message_type: string;
  preview: string;
  created_at: string;
}

export interface ChatRepairSummary {
  scanned_contacts: number;
  affected_external_messages: number;
  move_candidates: number;
  merge_required_candidates: number;
  conflict_candidates: number;
  auto_fixable_candidates: number;
}

export interface ChatRepairApplyResult {
  processed_candidates: number;
  updated_contacts: number;
  updated_messages: number;
  skipped_candidates: number;
}

export interface ApiResponseEnvelope<T> {
  data: T;
}

export type ApiResponseData<T> = T | ApiResponseEnvelope<T>;

export function isApiResponseEnvelope<T>(
  value: ApiResponseData<T>,
): value is ApiResponseEnvelope<T> {
  return typeof value === "object" && value !== null && "data" in value;
}

export function unwrapApiPayload<T>(value: ApiResponseData<T>): T {
  return isApiResponseEnvelope(value) ? value.data : value;
}

export interface ChatRepairPreviewResult {
  summary: ChatRepairSummary;
  candidates: ChatRepairCandidate[];
}

export interface ChatRepairScanResult {
  auto_applied: ChatRepairApplyResult;
  summary: ChatRepairSummary;
  candidates: ChatRepairCandidate[];
}

export const chatRepairService = {
  preview: (limit = 100) =>
    api.get<ApiResponseData<ChatRepairPreviewResult>>(
      "/admin/chat-repair",
      { params: { limit } },
    ),
  scan: () => api.post<ApiResponseData<ChatRepairScanResult>>("/admin/chat-repair/scan", {}),
  apply: (contactIds?: string[], manualMergeContactIds?: string[]) =>
    api.post<ApiResponseData<ChatRepairApplyResult>>("/admin/chat-repair/apply", {
      contact_ids: contactIds ?? [],
      manual_merge_contact_ids: manualMergeContactIds ?? [],
    }),
};

export const accountsService = {
  list: () => api.get("/accounts"),
};

export interface MetaOnboardingConfig {
  meta_app_id: string;
  embedded_signup_config_id: string;
  graph_api_version: string;
  public_webhook_base_url: string;
  required_scopes: string[];
  has_app_secret: boolean;
  is_configured: boolean;
  callback_url: string;
}

export interface OnboardingStepState {
  status: string;
  summary?: string;
  updated_at?: string;
  details?: Record<string, any>;
}

export interface OnboardingCheckpointStatus {
  key: string;
  label: string;
  status: string;
  summary?: string;
}

export interface OnboardingReadinessStatus {
  status: string;
  summary?: string;
  external_checkpoints?: OnboardingCheckpointStatus[];
}

export interface WhatsAppOnboardingSession {
  id: string;
  organization_id: string;
  account_id?: string | null;
  mode: "embedded_signup" | "manual_import";
  status: string;
  current_step: string;
  account_name: string;
  app_id: string;
  phone_id: string;
  business_id: string;
  webhook_verify_token: string;
  api_version: string;
  has_access_token: boolean;
  has_app_secret: boolean;
  step_state: Record<string, OnboardingStepState>;
  readiness: OnboardingReadinessStatus;
  metadata?: Record<string, any>;
  last_error?: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
}

export const metaOnboardingService = {
  getConfig: () =>
    api.get<ApiResponseData<{ config: MetaOnboardingConfig }>>(
      "/settings/meta-onboarding",
    ),
  updateConfig: (data: {
    meta_app_id: string;
    meta_app_secret?: string;
    embedded_signup_config_id: string;
    graph_api_version: string;
    public_webhook_base_url: string;
    required_scopes: string[];
  }) =>
    api.put<ApiResponseData<{ config: MetaOnboardingConfig }>>(
      "/settings/meta-onboarding",
      data,
    ),
};

export const onboardingService = {
  createSession: (data?: {
    mode?: "embedded_signup" | "manual_import";
    account_name?: string;
  }) =>
    api.post<ApiResponseData<{ session: WhatsAppOnboardingSession }>>(
      "/accounts/onboarding/sessions",
      data ?? {},
    ),
  getSession: (id: string) =>
    api.get<ApiResponseData<{ session: WhatsAppOnboardingSession }>>(
      `/accounts/onboarding/sessions/${id}`,
    ),
  completeEmbeddedSignup: (
    id: string,
    data: {
      account_name?: string;
      code?: string;
      access_token?: string;
      app_id?: string;
      phone_id: string;
      business_id: string;
      webhook_verify_token?: string;
      api_version?: string;
      metadata?: Record<string, any>;
    },
  ) =>
    api.post<ApiResponseData<{ session: WhatsAppOnboardingSession }>>(
      `/accounts/onboarding/sessions/${id}/embedded-signup/complete`,
      data,
    ),
  manualImport: (
    id: string,
    data: {
      account_name?: string;
      app_id?: string;
      app_secret?: string;
      phone_id: string;
      business_id: string;
      access_token: string;
      webhook_verify_token?: string;
      api_version?: string;
    },
  ) =>
    api.post<ApiResponseData<{ session: WhatsAppOnboardingSession }>>(
      `/accounts/onboarding/sessions/${id}/manual-import`,
      data,
    ),
  requestCode: (
    id: string,
    data: { code_method: "SMS" | "VOICE"; language?: string },
  ) =>
    api.post<ApiResponseData<{ session: WhatsAppOnboardingSession }>>(
      `/accounts/onboarding/sessions/${id}/request-code`,
      data,
    ),
  verifyCode: (id: string, data: { code: string }) =>
    api.post<ApiResponseData<{ session: WhatsAppOnboardingSession }>>(
      `/accounts/onboarding/sessions/${id}/verify-code`,
      data,
    ),
  registerPhone: (
    id: string,
    data: {
      pin: string;
      backup_password?: string;
      backup_data?: string;
      data_localization_region?: string;
      meta_store_retention_minutes?: number | null;
    },
  ) =>
    api.post<ApiResponseData<{ session: WhatsAppOnboardingSession }>>(
      `/accounts/onboarding/sessions/${id}/register-phone`,
      data,
    ),
  validateWebhook: (id: string) =>
    api.post<ApiResponseData<{ session: WhatsAppOnboardingSession }>>(
      `/accounts/onboarding/sessions/${id}/validate-webhook`,
      {},
    ),
  subscribeWebhooks: (id: string) =>
    api.post<ApiResponseData<{ session: WhatsAppOnboardingSession }>>(
      `/accounts/onboarding/sessions/${id}/subscribe-webhooks`,
      {},
    ),
  finalize: (id: string) =>
    api.post<ApiResponseData<{ session: WhatsAppOnboardingSession }>>(
      `/accounts/onboarding/sessions/${id}/finalize`,
      {},
    ),
};

export interface OrgMismatchRecord {
  contact_id: string;
  phone_number: string;
  profile_name: string;
  current_org_id: string;
  current_org_name: string;
  correct_org_id: string;
  correct_org_name: string;
  phone_number_id: string;
  message_count: number;
}

export interface OrgMismatchPreviewResult {
  count: number;
  records: OrgMismatchRecord[];
}

export interface OrgMismatchApplyResult {
  fixed: number;
  errors?: string[];
}

export const orgMismatchService = {
  preview: () => api.get<ApiResponseData<OrgMismatchPreviewResult>>("/admin/org-mismatch"),
  apply: () => api.post<ApiResponseData<OrgMismatchApplyResult>>("/admin/org-mismatch/apply", {}),
};

export const contactsService = {
  list: (params?: {
    search?: string;
    page?: number;
    limit?: number;
    tags?: string;
    whatsapp_account?: string;
    unread_only?: boolean;
    is_active?: boolean;
  }) => api.get("/contacts", { params }),
  get: (id: string) => api.get(`/contacts/${id}`),
  create: (data: any) => api.post("/contacts", data),
  update: (id: string, data: any) => api.put(`/contacts/${id}`, data),
  delete: (id: string, options?: { include_messages?: boolean }) =>
    api.delete(`/contacts/${id}`, { params: options }),
  assign: (id: string, userId: string | null) =>
    api.put(`/contacts/${id}/assign`, { user_id: userId }),
  updateTags: (id: string, tags: string[]) =>
    api.put(`/contacts/${id}/tags`, { tags }),
  getSessionData: (id: string) => api.get(`/contacts/${id}/session-data`),
};

// Generic Import/Export Service
export interface ExportColumn {
  key: string;
  label: string;
}

export interface ExportConfig {
  table: string;
  columns: ExportColumn[];
  default_columns: string[];
}

export interface ImportConfig {
  table: string;
  required_columns: ExportColumn[];
  optional_columns: ExportColumn[];
  unique_column: string;
}

export interface ImportResult {
  created: number;
  updated: number;
  skipped: number;
  errors: number;
  messages: string[];
}

export const dataService = {
  // Get export configuration for a table
  getExportConfig: (table: string) =>
    api.get<ExportConfig>(`/export/${table}/config`),

  // Get import configuration for a table
  getImportConfig: (table: string) =>
    api.get<ImportConfig>(`/import/${table}/config`),

  // Export data - returns CSV blob
  exportData: async (
    table: string,
    columns?: string[],
    filters?: Record<string, string>,
  ) => {
    const response = await api.post(
      "/export",
      { table, columns, filters },
      {
        responseType: "blob",
      },
    );
    return response;
  },

  // Import data from CSV file
  importData: (
    table: string,
    file: File,
    updateOnDuplicate?: boolean,
    columnMapping?: Record<string, string>,
  ) => {
    const formData = new FormData();
    formData.append("file", file);
    formData.append("table", table);
    if (updateOnDuplicate) {
      formData.append("update_on_duplicate", "true");
    }
    if (columnMapping) {
      formData.append("column_mapping", JSON.stringify(columnMapping));
    }
    return api.post<ImportResult>("/import", formData, {
      headers: { "Content-Type": "multipart/form-data" },
      timeout: 120000,
    });
  },
};

export const messagesService = {
  list: (
    contactId: string,
    params?: {
      page?: number;
      limit?: number;
      before_id?: string;
      account?: string;
    },
  ) => api.get(`/contacts/${contactId}/messages`, { params }),
  reprocess: (messageId: string) =>
    api.post(`/chat/messages/${messageId}/reprocess`),
  send: (
    contactId: string,
    data: {
      type: string;
      content: any;
      reply_to_message_id?: string;
      whatsapp_account?: string;
    },
  ) => api.post(`/contacts/${contactId}/messages`, data),
  sendTemplate: (
    contactId: string,
    data: {
      template_name: string;
      template_params?: Record<string, string>;
      account_name?: string;
    },
    headerFile?: File,
  ) => {
    if (headerFile) {
      const formData = new FormData();
      formData.append("contact_id", contactId);
      formData.append("template_name", data.template_name);
      if (data.template_params) {
        formData.append(
          "template_params",
          JSON.stringify(data.template_params),
        );
      }
      if (data.account_name) {
        formData.append("account_name", data.account_name);
      }
      formData.append("header_file", headerFile);
      return api.post("/messages/template", formData, {
        headers: { "Content-Type": "multipart/form-data" },
      });
    }
    return api.post("/messages/template", { contact_id: contactId, ...data });
  },
  sendReaction: (contactId: string, messageId: string, emoji: string) =>
    api.post(`/contacts/${contactId}/messages/${messageId}/reaction`, {
      emoji,
    }),
  delete: (contactId: string, messageId: string) =>
    api.delete(`/contacts/${contactId}/messages/${messageId}`),
};

export const templatesService = {
  list: (params?: {
    status?: string;
    category?: string;
    account?: string;
    search?: string;
    page?: number;
    limit?: number;
  }) => api.get<{ templates: any[]; total?: number }>("/templates", { params }),
  get: (id: string) => api.get(`/templates/${id}`),
  uploadMedia: (accountName: string, file: File) => {
    const formData = new FormData();
    formData.append("file", file);
    formData.append("account", accountName);
    return api.post("/templates/upload-media", formData, {
      headers: { "Content-Type": "multipart/form-data" },
    });
  },
};

export const flowsService = {
  list: (params?: {
    account?: string;
    search?: string;
    page?: number;
    limit?: number;
  }) => api.get<{ flows: any[]; total?: number }>("/flows", { params }),
  create: (data: any) => api.post("/flows", data),
  update: (id: string, data: any) => api.put(`/flows/${id}`, data),
  delete: (id: string) => api.delete(`/flows/${id}`),
  saveToMeta: (id: string) => api.post(`/flows/${id}/save-to-meta`),
  publish: (id: string) => api.post(`/flows/${id}/publish`),
  duplicate: (id: string) => api.post(`/flows/${id}/duplicate`),
  sync: (whatsappAccount: string) =>
    api.post("/flows/sync", { whatsapp_account: whatsappAccount }),
};

export const campaignsService = {
  list: (params?: {
    status?: string;
    from?: string;
    to?: string;
    search?: string;
    page?: number;
    limit?: number;
  }) => api.get("/campaigns", { params }),
  create: (data: any) => api.post("/campaigns", data),
  update: (id: string, data: any) => api.put(`/campaigns/${id}`, data),
  delete: (id: string) => api.delete(`/campaigns/${id}`),
  start: (id: string) => api.post(`/campaigns/${id}/start`),
  pause: (id: string) => api.post(`/campaigns/${id}/pause`),
  cancel: (id: string) => api.post(`/campaigns/${id}/cancel`),
  retryFailed: (id: string) => api.post(`/campaigns/${id}/retry-failed`),
  resend: (id: string) => api.post(`/campaigns/${id}/resend`),
  exportReport: (id: string) =>
    api.get(`/campaigns/${id}/report.xlsx`, { responseType: "blob" }),
  // Recipients
  getRecipients: (id: string) => api.get(`/campaigns/${id}/recipients`),
  addRecipients: (
    id: string,
    data: {
      recipients?: Array<{
        phone_number: string;
        recipient_name?: string;
        template_params?: Record<string, any>;
      }>;
      contact_ids?: string[];
      tag_names?: string[];
    },
  ) => api.post(`/campaigns/${id}/recipients/import`, data, { timeout: 120000 }),
  deleteRecipient: (campaignId: string, recipientId: string) =>
    api.delete(`/campaigns/${campaignId}/recipients/${recipientId}`),
  // Media
  uploadMedia: (campaignId: string, file: File) => {
    const formData = new FormData();
    formData.append("file", file);
    return api.post(`/campaigns/${campaignId}/media`, formData, {
      headers: { "Content-Type": "multipart/form-data" },
    });
  },
  getMedia: (campaignId: string) =>
    api.get(`/campaigns/${campaignId}/media`, { responseType: "arraybuffer" }),
};

export const chatbotService = {
  // Settings
  getSettings: () => api.get("/chatbot/settings"),
  updateSettings: (data: any) => api.put("/chatbot/settings", data),

  // Keywords
  listKeywords: (params?: { search?: string; page?: number; limit?: number }) =>
    api.get<{ rules: any[]; total?: number }>("/chatbot/keywords", { params }),
  createKeyword: (data: any) => api.post("/chatbot/keywords", data),
  updateKeyword: (id: string, data: any) =>
    api.put(`/chatbot/keywords/${id}`, data),
  deleteKeyword: (id: string) => api.delete(`/chatbot/keywords/${id}`),

  // Flows
  listFlows: (params?: { search?: string; page?: number; limit?: number }) =>
    api.get<{ flows: any[]; total?: number }>("/chatbot/flows", { params }),
  getFlow: (id: string) => api.get(`/chatbot/flows/${id}`),
  createFlow: (data: any) => api.post("/chatbot/flows", data),
  updateFlow: (id: string, data: any) => api.put(`/chatbot/flows/${id}`, data),
  uploadFlowMedia: (file: File) => {
    const form = new FormData();
    form.append("file", file);
    return api.post("/flow-media/upload", form, {
      headers: { "Content-Type": "multipart/form-data" },
    });
  },
  deleteFlow: (id: string) => api.delete(`/chatbot/flows/${id}`),

  // AI Contexts
  listAIContexts: (params?: {
    search?: string;
    page?: number;
    limit?: number;
  }) =>
    api.get<{ contexts: any[]; total?: number }>("/chatbot/ai-contexts", {
      params,
    }),
  createAIContext: (data: any) => api.post("/chatbot/ai-contexts", data),
  updateAIContext: (id: string, data: any) =>
    api.put(`/chatbot/ai-contexts/${id}`, data),
  deleteAIContext: (id: string) => api.delete(`/chatbot/ai-contexts/${id}`),

  // Agent Transfers
  listTransfers: (params?: {
    status?: string;
    agent_id?: string;
    team_id?: string;
    limit?: number;
    offset?: number;
    include?: string; // 'all' | 'contact,agent,team' etc.
  }) => api.get("/chatbot/transfers", { params }),
  createTransfer: (data: {
    contact_id: string;
    whatsapp_account: string;
    agent_id?: string;
    notes?: string;
    source?: string;
  }) => api.post("/chatbot/transfers", data),
  pickNextTransfer: () => api.post("/chatbot/transfers/pick"),
  resumeTransfer: (id: string) => api.put(`/chatbot/transfers/${id}/resume`),
  assignTransfer: (
    id: string,
    agentId: string | null,
    teamId?: string | null,
  ) =>
    api.put(`/chatbot/transfers/${id}/assign`, {
      agent_id: agentId,
      team_id: teamId,
    }),
};

export interface CannedResponse {
  id: string;
  name: string;
  shortcut: string;
  content: string;
  category: string;
  is_active: boolean;
  usage_count: number;
  created_at: string;
  updated_at: string;
}

export const cannedResponsesService = {
  list: (params?: {
    category?: string;
    search?: string;
    active_only?: string;
    page?: number;
    limit?: number;
  }) =>
    api.get<{ canned_responses: CannedResponse[]; total?: number }>(
      "/canned-responses",
      { params },
    ),
  create: (data: {
    name: string;
    shortcut?: string;
    content: string;
    category?: string;
  }) => api.post("/canned-responses", data),
  update: (
    id: string,
    data: {
      name?: string;
      shortcut?: string;
      content?: string;
      category?: string;
      is_active?: boolean;
    },
  ) => api.put(`/canned-responses/${id}`, data),
  delete: (id: string) => api.delete(`/canned-responses/${id}`),
  use: (id: string) => api.post(`/canned-responses/${id}/use`),
};

export const agentAnalyticsService = {
  getSummary: (params?: { from?: string; to?: string; agent_id?: string }) =>
    api.get("/analytics/agents", { params }),
};

// Meta WhatsApp Analytics Types
export type MetaAnalyticsType =
  | "analytics"
  | "conversation_analytics"
  | "pricing_analytics"
  | "template_analytics"
  | "call_analytics";

export type MetaGranularity = "HALF_HOUR" | "DAY" | "MONTH";

export interface MetaAnalyticsAccount {
  id: string;
  name: string;
  phone_id: string;
}

export interface MetaMessagingDataPoint {
  start: number;
  end: number;
  sent: number;
  delivered: number;
}

interface MetaConversationDataPoint {
  start: number;
  end: number;
  conversation: number;
  conversation_type: string;
  conversation_direction: string;
  conversation_category: string;
  cost: number;
}

export interface MetaPricingDataPoint {
  start: number;
  end: number;
  volume: number;
  cost: number;
  currency?: string; // Currency code when provided by Meta
  country?: string; // Country code (IN, US, etc.)
  pricing_type?: string; // FREE_CUSTOMER_SERVICE, FREE_ENTRY_POINT, REGULAR
  pricing_category?: string; // MARKETING, UTILITY, AUTHENTICATION, SERVICE, etc.
  tier?: string; // Pricing tier
}

interface MetaTemplateCostItem {
  type: string; // amount_spent, cost_per_delivered, cost_per_url_button_click
  value?: number; // The cost value
  currency?: string; // Currency code when provided by Meta
}

interface MetaTemplateClickItem {
  type: string; // quick_reply_button, unique_url_button
  button_content: string; // The button text
  count: number; // Number of clicks
}

export interface MetaTemplateDataPoint {
  start: number;
  end: number;
  template_id: string;
  sent: number;
  delivered: number;
  read: number;
  replied?: number;
  clicked?: MetaTemplateClickItem[]; // Array of button click details
  cost?: MetaTemplateCostItem[];
}

export interface MetaCallDataPoint {
  start: number;
  end: number;
  count: number;
  cost: number;
  currency?: string; // Currency code when provided by Meta
  average_duration: number;
  direction?: string; // USER_INITIATED or BUSINESS_INITIATED
}

interface MetaAnalyticsData {
  id: string;
  analytics?: {
    granularity: string;
    data_points: MetaMessagingDataPoint[];
  };
  conversation_analytics?: {
    granularity: string;
    data_points: MetaConversationDataPoint[];
  };
  pricing_analytics?: {
    granularity: string;
    currency?: string;
    data_points: MetaPricingDataPoint[];
  };
  template_analytics?: {
    granularity: string;
    currency?: string;
    data_points: MetaTemplateDataPoint[];
  };
  call_analytics?: {
    granularity: string;
    currency?: string;
    data_points: MetaCallDataPoint[];
  };
}

export interface MetaAnalyticsResponse {
  account_id: string;
  account_name: string;
  data: MetaAnalyticsData | null;
  template_names?: Record<string, string>; // meta_template_id -> template name
}

export const metaAnalyticsService = {
  get: (params: {
    account_id?: string;
    analytics_type: MetaAnalyticsType;
    start: string;
    end: string;
    granularity?: MetaGranularity;
    template_ids?: string;
  }) =>
    api.get<{ accounts: MetaAnalyticsResponse[]; cached: boolean }>(
      "/analytics/meta",
      { params },
    ),

  getAccounts: () =>
    api.get<{ accounts: MetaAnalyticsAccount[] }>("/analytics/meta/accounts"),

  refresh: () => api.post("/analytics/meta/refresh"),
};

// Dashboard Widgets (customizable analytics)
export interface DashboardWidget {
  id: string;
  name: string;
  description: string;
  data_source: string;
  metric: string;
  field: string;
  filters: Array<{ field: string; operator: string; value: string }>;
  display_type: string;
  chart_type: string;
  group_by_field: string;
  show_change: boolean;
  color: string;
  size: string;
  display_order: number;
  grid_x: number;
  grid_y: number;
  grid_w: number;
  grid_h: number;
  config: Record<string, any>;
  is_shared: boolean;
  is_default: boolean;
  is_owner: boolean;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface WidgetData {
  widget_id: string;
  value: number;
  change: number;
  prev_value: number;
  chart_data: Array<{ label: string; value: number }>;
  data_points: Array<{ label: string; value: number; color?: string }>;
  grouped_series?: {
    labels: string[];
    datasets: Array<{ label: string; data: number[] }>;
  };
  table_rows?: Array<{
    id: string;
    label: string;
    sub_label: string;
    status: string;
    direction?: string;
    created_at: string;
  }>;
}

interface DataSourceInfo {
  name: string;
  label: string;
  fields: string[];
}

export interface LayoutItem {
  id: string;
  grid_x: number;
  grid_y: number;
  grid_w: number;
  grid_h: number;
}

export const widgetsService = {
  list: () => api.get<{ widgets: DashboardWidget[] }>("/widgets"),
  create: (data: {
    name: string;
    description?: string;
    data_source: string;
    metric: string;
    field?: string;
    filters?: Array<{ field: string; operator: string; value: string }>;
    display_type?: string;
    chart_type?: string;
    group_by_field?: string;
    show_change?: boolean;
    color?: string;
    size?: string;
    config?: Record<string, any>;
    is_shared?: boolean;
  }) => api.post<DashboardWidget>("/widgets", data),
  update: (
    id: string,
    data: Partial<{
      name: string;
      description: string;
      data_source: string;
      metric: string;
      field: string;
      filters: Array<{ field: string; operator: string; value: string }>;
      display_type: string;
      chart_type: string;
      group_by_field: string;
      show_change: boolean;
      color: string;
      size: string;
      config: Record<string, any>;
      is_shared: boolean;
    }>,
  ) => api.put<DashboardWidget>(`/widgets/${id}`, data),
  delete: (id: string) => api.delete(`/widgets/${id}`),
  getAllData: (params?: { from?: string; to?: string }) =>
    api.get<{ data: Record<string, WidgetData> }>("/widgets/data", { params }),
  getDataSources: () =>
    api.get<{
      data_sources: DataSourceInfo[];
      metrics: string[];
      display_types: string[];
      operators: Array<{ value: string; label: string }>;
    }>("/widgets/data-sources"),
  saveLayout: (layout: LayoutItem[]) => api.post("/widgets/layout", { layout }),
};

export const organizationService = {
  getSettings: () => api.get("/org/settings"),
  updateSettings: (data: {
    mask_phone_numbers?: boolean;
    timezone?: string;
    date_format?: string;
    name?: string;
    calling_enabled?: boolean;
    max_call_duration?: number;
    transfer_timeout_secs?: number;
    hold_music_file?: string;
    ringback_file?: string;
    message_retention_days?: number;
  }) => api.put("/org/settings", data),
  uploadOrgAudio: (file: File, type: "hold_music" | "ringback") => {
    const formData = new FormData();
    formData.append("file", file);
    return api.post(`/org/audio?type=${type}`, formData, {
      headers: { "Content-Type": "multipart/form-data" },
    });
  },
};

// Organizations
export interface Organization {
  id: string;
  name: string;
  slug?: string;
  created_at: string;
}

export const organizationsService = {
  list: () => api.get<{ organizations: Organization[] }>("/organizations"),
  create: (data: { name: string }) => api.post("/organizations", data),
  // Members
  addMember: (data: { user_id?: string; email?: string; role_id?: string }) =>
    api.post("/organizations/members", data),
};

export interface Webhook {
  id: string;
  name: string;
  url: string;
  events: string[];
  headers: Record<string, string>;
  is_active: boolean;
  has_secret: boolean;
  created_at: string;
  updated_at: string;
}

export interface WebhookEvent {
  value: string;
  label: string;
  description: string;
}

export interface Team {
  id: string;
  name: string;
  description: string;
  assignment_strategy: "round_robin" | "load_balanced" | "manual";
  per_agent_timeout_secs: number;
  is_active: boolean;
  member_count: number;
  created_at: string;
  updated_at: string;
}

export interface TeamMember {
  id: string;
  team_id?: string;
  user_id: string;
  role: "manager" | "agent";
  last_assigned_at: string | null;
  // Flat structure from API
  full_name: string;
  email: string;
  is_available: boolean;
  // Optional nested user for local additions
  user?: {
    id: string;
    full_name: string;
    email: string;
    is_available: boolean;
  };
}

export const teamsService = {
  list: (params?: { search?: string; page?: number; limit?: number }) =>
    api.get<{ teams: Team[] }>("/teams", { params }),
  get: (id: string) => api.get<{ team: Team }>(`/teams/${id}`),
  create: (data: {
    name: string;
    description?: string;
    assignment_strategy?: "round_robin" | "load_balanced" | "manual";
    per_agent_timeout_secs?: number;
  }) => api.post<{ team: Team }>("/teams", data),
  update: (
    id: string,
    data: {
      name?: string;
      description?: string;
      assignment_strategy?: "round_robin" | "load_balanced" | "manual";
      per_agent_timeout_secs?: number;
      is_active?: boolean;
    },
  ) => api.put<{ team: Team }>(`/teams/${id}`, data),
  delete: (id: string) => api.delete(`/teams/${id}`),
  // Members
  listMembers: (teamId: string) =>
    api.get<{ members: TeamMember[] }>(`/teams/${teamId}/members`),
  addMember: (
    teamId: string,
    data: { user_id: string; role?: "manager" | "agent" },
  ) => api.post<{ member: TeamMember }>(`/teams/${teamId}/members`, data),
  removeMember: (teamId: string, userId: string) =>
    api.delete(`/teams/${teamId}/members/${userId}`),
};

export const webhooksService = {
  list: (params?: { search?: string; page?: number; limit?: number }) =>
    api.get<{
      webhooks: Webhook[];
      available_events: WebhookEvent[];
      total?: number;
    }>("/webhooks", { params }),
  get: (id: string) => api.get<Webhook>(`/webhooks/${id}`),
  create: (data: {
    name: string;
    url: string;
    events: string[];
    headers?: Record<string, string>;
    secret?: string;
  }) => api.post<Webhook>("/webhooks", data),
  update: (
    id: string,
    data: {
      name?: string;
      url?: string;
      events?: string[];
      headers?: Record<string, string>;
      secret?: string;
      is_active?: boolean;
    },
  ) => api.put<Webhook>(`/webhooks/${id}`, data),
  delete: (id: string) => api.delete(`/webhooks/${id}`),
  test: (id: string) => api.post(`/webhooks/${id}/test`),
};

export interface CustomAction {
  id: string;
  name: string;
  icon: string;
  action_type: "webhook" | "url" | "javascript";
  config: {
    url?: string;
    method?: string;
    headers?: Record<string, string>;
    body?: string;
    open_in_new_tab?: boolean;
    code?: string;
  };
  is_active: boolean;
  display_order: number;
  created_at: string;
  updated_at: string;
}

export interface ActionResult {
  success: boolean;
  message?: string;
  redirect_url?: string;
  clipboard?: string;
  toast?: {
    message: string;
    type: "success" | "error" | "info" | "warning";
  };
  data?: Record<string, any>;
}

export const customActionsService = {
  list: (params?: { search?: string; page?: number; limit?: number }) =>
    api.get<{ custom_actions: CustomAction[]; total?: number }>(
      "/custom-actions",
      { params },
    ),
  get: (id: string) => api.get<CustomAction>(`/custom-actions/${id}`),
  create: (data: {
    name: string;
    icon?: string;
    action_type: "webhook" | "url" | "javascript";
    config: Record<string, any>;
    is_active?: boolean;
    display_order?: number;
  }) => api.post<CustomAction>("/custom-actions", data),
  update: (
    id: string,
    data: {
      name?: string;
      icon?: string;
      action_type?: "webhook" | "url" | "javascript";
      config?: Record<string, any>;
      is_active?: boolean;
      display_order?: number;
    },
  ) => api.put<CustomAction>(`/custom-actions/${id}`, data),
  delete: (id: string) => api.delete(`/custom-actions/${id}`),
  execute: (id: string, contactId: string) =>
    api.post<ActionResult>(`/custom-actions/${id}/execute`, {
      contact_id: contactId,
    }),
};

// Roles and Permissions
export interface Permission {
  id: string;
  resource: string;
  action: string;
  description: string;
  key: string; // "resource:action"
}

export interface Role {
  id: string;
  name: string;
  description: string;
  is_system: boolean;
  is_default: boolean;
  permissions: string[]; // ["resource:action", ...]
  user_count: number;
  created_at: string;
  updated_at: string;
}

export const rolesService = {
  list: (params?: { search?: string; page?: number; limit?: number }) =>
    api.get<{ roles: Role[] }>("/roles", { params }),
  get: (id: string) => api.get<Role>(`/roles/${id}`),
  create: (data: {
    name: string;
    description?: string;
    is_default?: boolean;
    permissions: string[];
  }) => api.post<Role>("/roles", data),
  update: (
    id: string,
    data: {
      name?: string;
      description?: string;
      is_default?: boolean;
      permissions?: string[];
    },
  ) => api.put<Role>(`/roles/${id}`, data),
  delete: (id: string) => api.delete(`/roles/${id}`),
};

export const permissionsService = {
  list: () => api.get<{ permissions: Permission[] }>("/permissions"),
};

// Tags
export interface Tag {
  name: string;
  color: string;
  created_at: string;
  updated_at: string;
}

export const tagsService = {
  list: (params?: { search?: string; page?: number; limit?: number }) =>
    api.get<{ tags: Tag[]; total?: number; page?: number; limit?: number }>(
      "/tags",
      { params },
    ),
  create: (data: { name: string; color?: string }) =>
    api.post<Tag>("/tags", data),
  update: (name: string, data: { name?: string; color?: string }) =>
    api.put<Tag>(`/tags/${encodeURIComponent(name)}`, data),
  delete: (name: string) => api.delete(`/tags/${encodeURIComponent(name)}`),
};

// Conversation Notes
export interface ConversationNote {
  id: string;
  contact_id: string;
  created_by_id: string;
  created_by_name: string;
  content: string;
  created_at: string;
  updated_at: string;
}

export const notesService = {
  list: (contactId: string, params?: { limit?: number; before?: string }) =>
    api.get<{ notes: ConversationNote[]; total: number; has_more: boolean }>(
      `/contacts/${contactId}/notes`,
      { params },
    ),
  create: (contactId: string, data: { content: string }) =>
    api.post<ConversationNote>(`/contacts/${contactId}/notes`, data),
  update: (contactId: string, noteId: string, data: { content: string }) =>
    api.put<ConversationNote>(`/contacts/${contactId}/notes/${noteId}`, data),
  delete: (contactId: string, noteId: string) =>
    api.delete(`/contacts/${contactId}/notes/${noteId}`),
};

// Calling - Call Logs & IVR Flows
export interface CallLog {
  id: string;
  organization_id: string;
  whatsapp_account: string;
  contact_id: string;
  whatsapp_call_id: string;
  caller_phone: string;
  direction: "incoming" | "outgoing";
  status:
    | "ringing"
    | "answered"
    | "completed"
    | "missed"
    | "rejected"
    | "failed"
    | "initiating"
    | "accepted"
    | "transferring";
  duration: number;
  ivr_flow_id?: string;
  ivr_path?: Record<string, any>;
  agent_id?: string;
  started_at?: string;
  answered_at?: string;
  ended_at?: string;
  disconnected_by?: "client" | "agent" | "system";
  error_message?: string;
  recording_s3_key?: string;
  recording_duration?: number;
  contact?: {
    id: string;
    phone_number: string;
    profile_name: string;
  };
  agent?: {
    id: string;
    full_name: string;
    email: string;
  };
  ivr_flow?: IVRFlow;
  created_at: string;
  updated_at: string;
}

// v2 Node-based IVR Flow types
export type IVRNodeType =
  | "greeting"
  | "menu"
  | "gather"
  | "http_callback"
  | "transfer"
  | "goto_flow"
  | "timing"
  | "hangup";

export interface IVRNodePosition {
  x: number;
  y: number;
}

export interface IVRNode {
  id: string;
  type: IVRNodeType;
  label: string;
  position: IVRNodePosition;
  config: Record<string, any>;
}

export interface IVREdge {
  from: string;
  to: string;
  condition: string;
}

export interface IVRFlowData {
  version: 2;
  nodes: IVRNode[];
  edges: IVREdge[];
  entry_node: string;
}

export interface IVRFlow {
  id: string;
  organization_id: string;
  whatsapp_account: string;
  name: string;
  description: string;
  is_active: boolean;
  is_call_start: boolean;
  is_outgoing_end: boolean;
  menu: IVRFlowData;
  welcome_audio_url: string;
  created_at: string;
  updated_at: string;
}

export interface CallTransfer {
  id: string;
  organization_id: string;
  call_log_id: string;
  whatsapp_call_id: string;
  caller_phone: string;
  contact_id: string;
  whatsapp_account: string;
  status: "waiting" | "connected" | "completed" | "abandoned" | "no_answer";
  team_id?: string;
  agent_id?: string;
  initiating_agent_id?: string;
  transferred_at: string;
  connected_at?: string;
  completed_at?: string;
  hold_duration: number;
  talk_duration: number;
  ivr_path?: Record<string, any>;
  contact?: {
    id: string;
    phone_number: string;
    profile_name: string;
  };
  agent?: {
    id: string;
    full_name: string;
    email: string;
  };
  initiating_agent?: {
    id: string;
    full_name: string;
    email: string;
  };
  team?: {
    id: string;
    name: string;
  };
  call_log?: CallLog;
  created_at: string;
  updated_at: string;
}

// Outgoing Calls
export interface CallPermission {
  id: string;
  contact_id: string;
  whatsapp_account: string;
  status: "pending" | "accepted" | "declined" | "expired";
  message_id?: string;
  requested_at: string;
  responded_at?: string;
  expires_at?: string;
}

export const outgoingCallsService = {
  initiate: (data: {
    contact_id: string;
    whatsapp_account: string;
    sdp_offer: string;
  }) =>
    api.post<{ call_log_id: string; sdp_answer: string }>(
      "/calls/outgoing",
      data,
    ),
  hangup: (callLogId: string) =>
    api.post(`/calls/outgoing/${callLogId}/hangup`),
  requestPermission: (data: { contact_id: string; whatsapp_account: string }) =>
    api.post<{ permission_id: string }>("/calls/permission-request", data),
  getPermission: (contactId: string, whatsappAccount: string) =>
    api.get<CallPermission>(`/calls/permission/${contactId}`, {
      params: { whatsapp_account: whatsappAccount },
    }),
  getICEServers: () =>
    api.get<{
      ice_servers: Array<{
        urls: string[];
        username?: string;
        credential?: string;
      }>;
    }>("/calls/ice-servers"),
};

export const callLogsService = {
  list: (params?: {
    status?: string;
    account?: string;
    contact_id?: string;
    direction?: string;
    ivr_flow_id?: string;
    phone?: string;
    from?: string;
    to?: string;
    page?: number;
    limit?: number;
  }) =>
    api.get<{ call_logs: CallLog[]; total: number }>("/call-logs", { params }),
  get: (id: string) => api.get<CallLog>(`/call-logs/${id}`),
  getRecordingURL: (id: string) =>
    api.get<{ url: string; duration: number }>(`/call-logs/${id}/recording`),
};

export const callTransfersService = {
  list: (params?: { status?: string; page?: number; limit?: number }) =>
    api.get<{ call_transfers: CallTransfer[]; total: number }>(
      "/call-transfers",
      { params },
    ),
  get: (id: string) => api.get<CallTransfer>(`/call-transfers/${id}`),
  connect: (id: string, sdpOffer: string) =>
    api.post<{ sdp_answer: string }>(`/call-transfers/${id}/connect`, {
      sdp_offer: sdpOffer,
    }),
  hangup: (id: string) => api.post(`/call-transfers/${id}/hangup`),
  initiate: (data: {
    call_log_id: string;
    team_id: string;
    agent_id?: string;
  }) => api.post<{ status: string }>("/call-transfers/initiate", data),
};

export const ivrFlowsService = {
  list: (params?: { search?: string; page?: number; limit?: number }) =>
    api.get<{ ivr_flows: IVRFlow[]; total: number }>("/ivr-flows", { params }),
  get: (id: string) => api.get<IVRFlow>(`/ivr-flows/${id}`),
  create: (data: {
    whatsapp_account: string;
    name: string;
    description?: string;
    is_call_start?: boolean;
    menu: IVRFlowData;
    welcome_audio_url?: string;
  }) => api.post<IVRFlow>("/ivr-flows", data),
  update: (
    id: string,
    data: {
      name?: string;
      description?: string;
      is_active?: boolean;
      is_call_start?: boolean;
      is_outgoing_end?: boolean;
      menu?: IVRFlowData;
      welcome_audio_url?: string;
    },
  ) => api.put<IVRFlow>(`/ivr-flows/${id}`, data),
  delete: (id: string) => api.delete(`/ivr-flows/${id}`),
  uploadAudio: (file: File) => {
    const formData = new FormData();
    formData.append("file", file);
    return api.post("/ivr-flows/audio", formData, {
      headers: { "Content-Type": "multipart/form-data" },
    });
  },
  getAudioUrl: (filename: string) =>
    `${api.defaults.baseURL}/ivr-flows/audio/${encodeURIComponent(filename)}`,
};

export const rsvpService = {
  list: (params?: { search?: string; status?: string; page?: number; limit?: number }) =>
    api.get("/rsvp-events", { params }),
  create: (data: Record<string, unknown>) => api.post("/rsvp-events", data),
  get: (id: string) => api.get(`/rsvp-events/${id}`),
  update: (id: string, data: Record<string, unknown>) => api.put(`/rsvp-events/${id}`, data),
  delete: (id: string) => api.delete(`/rsvp-events/${id}`),
  activate: (id: string) => api.post(`/rsvp-events/${id}/activate`),
  close: (id: string) => api.post(`/rsvp-events/${id}/close`),
  responses: (id: string, params?: { attendance?: string; search?: string; page?: number; limit?: number; title_field?: string; title_value?: string; title_field2?: string; title_value2?: string }) =>
    api.get(`/rsvp-events/${id}/responses`, { params }),
  guests: (id: string, params?: { journey_status?: string; attendance?: string; member_status?: string; spouse_status?: string; source?: string; reminded?: string; search?: string; page?: number; limit?: number }) =>
    api.get(`/rsvp-events/${id}/guests`, { params }),
  guestCandidates: (id: string, params?: { search?: string; page?: number; limit?: number }) =>
    api.get(`/rsvp-events/${id}/guest-candidates`, { params }),
  addGuests: (id: string, contactIds: string[]) =>
    api.post(`/rsvp-events/${id}/guests`, { contact_ids: contactIds }),
  importGuests: (id: string, file: File) => {
    const form = new FormData()
    form.append('file', file)
    return api.post(`/rsvp-events/${id}/guests/import`, form, { headers: { 'Content-Type': 'multipart/form-data' } })
  },
  updateResponse: (
    id: string,
    responseId: string,
    data: { attendance?: string; answers?: Record<string, unknown>; notes?: string },
  ) => api.put(`/rsvp-events/${id}/responses/${responseId}`, data),
  deleteResponse: (id: string, responseId: string) =>
    api.delete(`/rsvp-events/${id}/responses/${responseId}`),
  tally: (id: string) => api.get(`/rsvp-events/${id}/tally`),
  generateFlowForm: (id: string) => api.post(`/rsvp-events/${id}/flow-form`),
  repromptPreview: (id: string) => api.get(`/rsvp-events/${id}/reprompt/preview`),
  reprompt: (id: string, phones?: string[]) =>
    api.post(`/rsvp-events/${id}/reprompt`, phones && phones.length ? { phones } : {}),
  recoverPartials: (id: string) => api.post(`/rsvp-events/${id}/recover-partials`),
  sendInvites: (id: string, contactIds: string[]) =>
    api.post(`/rsvp-events/${id}/send-invites`, { contact_ids: contactIds }),
  reminderPreview: (id: string, responseIds?: string[]) =>
    api.get(`/rsvp-events/${id}/reminders/preview`, { params: responseIds?.length ? { response_ids: responseIds.join(',') } : undefined }),
  sendReminders: (id: string, data: { response_ids?: string[]; exclude_response_ids?: string[]; all_not_started?: boolean; template_id?: string; template_params?: Record<string, string>; staging_id?: string; staging_filename?: string }) =>
    api.post(`/rsvp-events/${id}/reminders/send`, data),
  uploadReminderMedia: (id: string, file: File) => {
    const form = new FormData()
    form.append('file', file)
    return api.post(`/rsvp-events/${id}/reminders/media`, form, { headers: { 'Content-Type': 'multipart/form-data' } })
  },
  listReminders: (id: string) => api.get(`/rsvp-events/${id}/reminders`),
  createReminder: (id: string, data: { scheduled_at: string; template_id: string; template_params?: Record<string, string> }) =>
    api.post(`/rsvp-events/${id}/reminders`, data),
  cancelReminder: (id: string, scheduleId: string) =>
    api.delete(`/rsvp-events/${id}/reminders/${scheduleId}`),
  // Follow-ups reach guests who already responded, to ask one extra
  // question. Media re-uses the reminder staging endpoint (uploadReminderMedia
  // above) - there is no separate follow-up upload route.
  followUpPreview: (id: string, audience: string, answerKey?: string) =>
    api.get(`/rsvp-events/${id}/followup/preview`, { params: { audience, answer_key: answerKey || undefined } }),
  sendFollowUp: (id: string, data: { audience: string; answer_key?: string; flow_id: string; template_id: string; template_params?: Record<string, string>; staging_id?: string; staging_filename?: string; response_ids?: string[] }) =>
    api.post(`/rsvp-events/${id}/followup/send`, data),
  exportUrl: (id: string) => `${api.defaults.baseURL}/rsvp-events/${id}/export`,
};

export const leadSourcesService = {
  list: () => api.get('/lead-sources'),
  get: (id: string) => api.get(`/lead-sources/${id}`),
  create: (data: any) => api.post('/lead-sources', data),
  update: (id: string, data: any) => api.put(`/lead-sources/${id}`, data),
  delete: (id: string) => api.delete(`/lead-sources/${id}`),
  subscribe: (id: string) => api.post(`/lead-sources/${id}/subscribe`),
  test: (id: string) => api.post(`/lead-sources/${id}/test`),
  forms: (id: string) => api.get(`/lead-sources/${id}/forms`),
  sync: (id: string, data: { form_id?: string; since?: string; send?: boolean }) =>
    api.post(`/lead-sources/${id}/sync`, data),
  mappings: (id: string) => api.get(`/lead-sources/${id}/mappings`),
  createMapping: (id: string, data: any) => api.post(`/lead-sources/${id}/mappings`, data),
  updateMapping: (mappingId: string, data: any) => api.put(`/lead-mappings/${mappingId}`, data),
  deleteMapping: (mappingId: string) => api.delete(`/lead-mappings/${mappingId}`),
  preview: (mappingId: string, data?: { lead_id?: string }) => api.post(`/lead-mappings/${mappingId}/preview`, data || {}),
};

export const leadsService = {
  list: (params?: {
    source_id?: string;
    form_id?: string;
    status?: string;
    phone?: string;
    from?: string;
    to?: string;
    page?: number;
    limit?: number;
  }) => api.get('/leads', { params }),
  get: (id: string) => api.get(`/leads/${id}`),
  retry: (id: string) => api.post(`/leads/${id}/retry`),
  send: (id: string) => api.post(`/leads/${id}/send`),
  delete: (id: string, purge?: boolean) => api.delete(`/leads/${id}`, { params: { purge: !!purge } }),
  stats: () => api.get('/leads/stats'),
};

export default api;
