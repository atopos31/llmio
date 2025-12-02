// API client for interacting with the backend

const API_BASE = '/api';

export interface Provider {
  ID: number;
  Name: string;
  Type: string;
  Config: string;
  Console: string;
}

export interface Model {
  ID: number;
  Name: string;
  Remark: string;
  MaxRetry: number;
  TimeOut: number;
  IOLog: boolean;
  provider_name: string;
}

export interface ModelWithProvider {
  ID: number;
  ModelID: number;
  ProviderModel: string;
  ProviderID: number;
  ToolCall: boolean;
  StructuredOutput: boolean;
  Image: boolean;
  WithHeader: boolean;
  CustomerHeaders: Record<string, string> | null;
  Status: boolean | null;
  Weight: number;
}

export interface SystemConfig {
  enable_smart_routing: boolean;
  success_rate_weight: number;
  response_time_weight: number;
  decay_threshold_hours: number;
  min_weight: number;
}

export interface SystemStatus {
  total_providers: number;
  total_models: number;
  active_requests: number;
  uptime: string;
  version: string;
}

export interface ProviderMetric {
  provider_id: number;
  provider_name: string;
  success_rate: number;
  avg_response_time: number;
  total_requests: number;
  success_count: number;
  failure_count: number;
}

// Generic API request function
async function apiRequest<T>(endpoint: string, options: RequestInit = {}): Promise<T> {
  const url = `${API_BASE}${endpoint}`;

  // Get token from localStorage
  const token = localStorage.getItem("authToken");

  const response = await fetch(url, {
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { 'Authorization': `Bearer ${token}` } : {}),
      ...options.headers,
    },
    ...options,
  });

  // Handle 401 Unauthorized response
  if (response.status === 401) {
    // Redirect to login page
    window.location.href = '/login';
    throw new Error('Unauthorized');
  }

  if (!response.ok) {
    throw new Error(`API request failed: ${response.status} ${response.statusText}`);
  }

  const data = await response.json();
  if (data.code !== 200) {
    throw new Error(`${data.message}`);
  }
  return data.data as T;
}

// Provider API functions
export async function getProviders(filters: {
  name?: string;
  type?: string;
} = {}): Promise<Provider[]> {
  const params = new URLSearchParams();

  if (filters.name) params.append("name", filters.name);
  if (filters.type) params.append("type", filters.type);

  const queryString = params.toString();
  const endpoint = queryString ? `/providers?${queryString}` : '/providers';

  return apiRequest<Provider[]>(endpoint);
}

export async function createProvider(provider: {
  name: string;
  type: string;
  config: string;
  console: string;
}): Promise<Provider> {
  return apiRequest<Provider>('/providers', {
    method: 'POST',
    body: JSON.stringify(provider),
  });
}

export async function updateProvider(id: number, provider: {
  name?: string;
  type?: string;
  config?: string;
  console?: string;
}): Promise<Provider> {
  return apiRequest<Provider>(`/providers/${id}`, {
    method: 'PUT',
    body: JSON.stringify(provider),
  });
}

export async function deleteProvider(id: number): Promise<void> {
  await apiRequest<void>(`/providers/${id}`, {
    method: 'DELETE',
  });
}

// Model API functions
export async function getModels(): Promise<Model[]> {
  return apiRequest<Model[]>('/models');
}

export async function createModel(model: {
  name: string;
  remark: string;
  max_retry: number;
  time_out: number;
  io_log: boolean;
}): Promise<Model> {
  return apiRequest<Model>('/models', {
    method: 'POST',
    body: JSON.stringify(model),
  });
}

export async function updateModel(id: number, model: {
  name?: string;
  remark?: string;
  max_retry?: number;
  time_out?: number;
  io_log?: boolean;
}): Promise<Model> {
  return apiRequest<Model>(`/models/${id}`, {
    method: 'PUT',
    body: JSON.stringify(model),
  });
}

export async function deleteModel(id: number): Promise<void> {
  await apiRequest<void>(`/models/${id}`, {
    method: 'DELETE',
  });
}

// Model-Provider API functions
export async function getModelProviders(modelId: number): Promise<ModelWithProvider[]> {
  return apiRequest<ModelWithProvider[]>(`/model-providers?model_id=${modelId}`);
}

export async function getModelProviderStatus(providerId: number, modelName: string, providerModel: string): Promise<boolean[]> {
  const params = new URLSearchParams({
    provider_id: providerId.toString(),
    model_name: modelName,
    provider_model: providerModel
  });
  return apiRequest<boolean[]>(`/model-providers/status?${params.toString()}`);
}

export async function createModelProvider(association: {
  model_id: number;
  provider_name: string;
  provider_id: number;
  tool_call: boolean;
  structured_output: boolean;
  image: boolean;
  with_header: boolean;
  customer_headers: Record<string, string>;
  weight: number;
}): Promise<ModelWithProvider> {
  return apiRequest<ModelWithProvider>('/model-providers', {
    method: 'POST',
    body: JSON.stringify(association),
  });
}

export async function updateModelProvider(id: number, association: {
  model_id?: number;
  provider_name?: string;
  provider_id?: number;
  tool_call?: boolean;
  structured_output?: boolean;
  image?: boolean;
  with_header?: boolean;
  customer_headers?: Record<string, string>;
  weight?: number;
}): Promise<ModelWithProvider> {
  return apiRequest<ModelWithProvider>(`/model-providers/${id}`, {
    method: 'PUT',
    body: JSON.stringify(association),
  });
}

export async function updateModelProviderStatus(id: number, status: boolean): Promise<ModelWithProvider> {
  return apiRequest<ModelWithProvider>(`/model-providers/${id}/status`, {
    method: 'PATCH',
    body: JSON.stringify({ status }),
  });
}

export async function deleteModelProvider(id: number): Promise<void> {
  await apiRequest<void>(`/model-providers/${id}`, {
    method: 'DELETE',
  });
}

// System API functions
export async function getSystemStatus(): Promise<SystemStatus> {
  return apiRequest<SystemStatus>('/status');
}

export async function getProviderMetrics(): Promise<ProviderMetric[]> {
  return apiRequest<ProviderMetric[]>('/metrics/providers');
}

export async function getSystemConfig(): Promise<SystemConfig> {
  return apiRequest<SystemConfig>('/config');
}

export async function updateSystemConfig(config: SystemConfig): Promise<SystemConfig> {
  return apiRequest<SystemConfig>('/config', {
    method: 'PUT',
    body: JSON.stringify(config),
  });
}

// Metrics API functions
export interface MetricsData {
  reqs: number;
  tokens: number;
}

export interface ModelCount {
  model: string;
  calls: number;
}

export async function getMetrics(days: number): Promise<MetricsData> {
  return apiRequest<MetricsData>(`/metrics/use/${days}`);
}

export async function getModelCounts(): Promise<ModelCount[]> {
  return apiRequest<ModelCount[]>('/metrics/counts');
}

// Test API functions
export async function testModelProvider(id: number): Promise<any> {
  return apiRequest<any>(`/test/${id}`);
}

// Provider Templates API functions
export interface ProviderTemplate {
  type: string;
  template: string;
}

export async function getProviderTemplates(): Promise<ProviderTemplate[]> {
  return apiRequest<ProviderTemplate[]>('/providers/template');
}

// Provider Models API functions
export interface ProviderModel {
  id: string;
  object: string;
  created: number;
  owned_by: string;
}

export async function getProviderModels(providerId: number): Promise<ProviderModel[]> {
  return apiRequest<ProviderModel[]>(`/providers/models/${providerId}`);
}

export async function syncProviderModels(providerId: number): Promise<{ synced: number; total: number }> {
  return apiRequest<{ synced: number; total: number }>(`/providers/${providerId}/sync-models`, {
    method: 'POST',
  });
}

// Logs API functions
export interface ChatLog {
  ID: number;
  CreatedAt: string;
  Name: string;
  ProviderModel: string;
  ProviderName: string;
  Status: string;
  Style: string;
  UserAgent: string;
  RemoteIP?: string;
  Error: string;
  Retry: number;
  ProxyTime: number;
  FirstChunkTime: number;
  ChunkTime: number;
  Tps: number;
  ChatIO: boolean;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  prompt_tokens_details: PromptTokensDetails;
}

export interface PromptTokensDetails {
  cached_tokens: number;
}

export interface ChatIO {
  ID: number;
  CreatedAt: string;
  UpdatedAt: string;
  DeletedAt?: unknown;
  LogId: number;
  Input: string;
  OfString?: string | null;
  OfStringArray?: string[] | null;
}

export interface LogsResponse {
  data: ChatLog[];
  total: number;
  page: number;
  page_size: number;
  pages: number;
}

export async function getUserAgents(): Promise<string[]> {
  return apiRequest<string[]>('/user-agents');
}

export async function getLogs(
  page: number = 1,
  pageSize: number = 20,
  filters: {
    name?: string;
    providerModel?: string;
    providerName?: string;
    status?: string;
    style?: string;
    userAgent?: string;
  } = {}
): Promise<LogsResponse> {
  const params = new URLSearchParams();
  params.append("page", page.toString());
  params.append("page_size", pageSize.toString());

  if (filters.name) params.append("name", filters.name);
  if (filters.providerModel) params.append("provider_model", filters.providerModel);
  if (filters.providerName) params.append("provider_name", filters.providerName);
  if (filters.status) params.append("status", filters.status);
  if (filters.style) params.append("style", filters.style);
  if (filters.userAgent) params.append("user_agent", filters.userAgent);

  return apiRequest<LogsResponse>(`/logs?${params.toString()}`);
}

export async function getChatIO(logId: number): Promise<ChatIO> {
  return apiRequest<ChatIO>(`/logs/${logId}/chat-io`);
}
