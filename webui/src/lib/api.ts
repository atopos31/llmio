// API client for interacting with the backend

import type {
  QuotaConfig,
  QuotaConfigResponse,
  QuotaRunResult,
  QuotaSource,
  QuotaTestResult,
  QuotaUpstreamCandidate,
} from "@/lib/quota"

const API_BASE = '/api';

export interface Provider {
  ID: number;
  Name: string;
  Type: string;
  Config: string;
  Console: string;
  Proxy: string;
  ErrorMatcher: string;
}

export interface Model {
  ID: number;
  Name: string;
  Remark: string;
  MaxRetry: number;
  TimeOut: number;
  Strategy: string;
  Breaker?: boolean | null;
  DisplayOrder?: number;
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
  ExtraBody: Record<string, unknown> | null;
  Status: boolean | null;
  Weight: number;
  InputPrice: number;
  CacheReadPrice: number;
  OutputPrice: number;
  Currency: string;
}

export interface PaginatedResponse<T> {
  data: T[];
  total: number;
  page: number;
  page_size: number;
  pages: number;
}

export interface AuthKey {
  ID: number;
  CreatedAt: string;
  UpdatedAt: string;
  DeletedAt?: string | null;
  Name: string;
  Key: string;
  Status: boolean;
  IOLog: boolean;
  AllowAll: boolean;
  Models: string[] | null;
  ExpiresAt: string | null;
  UsageCount: number;
  LastUsedAt: string | null;
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

export async function getVersion(): Promise<string> {
  return apiRequest<string>('/version');
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
  proxy: string;
  error_matcher: string;
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
  proxy?: string;
  error_matcher?: string;
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
export type ModelQuery = {
  page?: number;
  page_size?: number;
  search?: string;
  strategy?: string;
};

export async function getModels(params: ModelQuery = {}): Promise<PaginatedResponse<Model>> {
  const searchParams = new URLSearchParams();
  if (params.page) searchParams.append('page', params.page.toString());
  if (params.page_size) searchParams.append('page_size', params.page_size.toString());
  if (params.search) searchParams.append('search', params.search);
  if (params.strategy) searchParams.append('strategy', params.strategy);
  const query = searchParams.toString();
  return apiRequest<PaginatedResponse<Model>>(query ? `/models?${query}` : '/models');
}

export async function getModelOptions(): Promise<Model[]> {
  return apiRequest<Model[]>('/models/select');
}

export async function createModel(model: {
  name: string;
  remark: string;
  max_retry: number;
  time_out: number;
  strategy: string;
  breaker: boolean;
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
  strategy?: string;
  breaker?: boolean;
}): Promise<Model> {
  return apiRequest<Model>(`/models/${id}`, {
    method: 'PUT',
    body: JSON.stringify(model),
  });
}

export async function updateModelOrder(modelIds: number[]): Promise<{ updated: number }> {
  return apiRequest<{ updated: number }>('/models/order', {
    method: 'PATCH',
    body: JSON.stringify({ model_ids: modelIds }),
  });
}

export async function deleteModel(id: number): Promise<void> {
  await apiRequest<void>(`/models/${id}`, {
    method: 'DELETE',
  });
}

// Auth key API
export type AuthKeyPayload = {
  name: string;
  key?: string;
  status: boolean;
  io_log: boolean;
  allow_all: boolean;
  models: string[];
  expires_at?: string | null;
};

export async function getAuthKeys(params: {
  page?: number;
  page_size?: number;
  status?: "active" | "inactive";
  allow_all?: "true" | "false";
  search?: string;
} = {}): Promise<PaginatedResponse<AuthKey>> {
  const searchParams = new URLSearchParams();

  if (params.page) searchParams.append("page", params.page.toString());
  if (params.page_size) searchParams.append("page_size", params.page_size.toString());
  if (params.status) searchParams.append("status", params.status);
  if (params.allow_all) searchParams.append("allow_all", params.allow_all);
  if (params.search) searchParams.append("search", params.search);

  const queryString = searchParams.toString();
  return apiRequest<PaginatedResponse<AuthKey>>(queryString ? `/auth-keys?${queryString}` : "/auth-keys");
}

export interface AuthKeyItem {
  id: number;
  name: string;
}

export async function getAuthKeysList(): Promise<AuthKeyItem[]> {
  return apiRequest<AuthKeyItem[]>("/auth-keys/list");
}

export async function createAuthKey(payload: AuthKeyPayload): Promise<AuthKey> {
  return apiRequest<AuthKey>("/auth-keys", {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export async function updateAuthKey(id: number, payload: AuthKeyPayload): Promise<AuthKey> {
  return apiRequest<AuthKey>(`/auth-keys/${id}`, {
    method: "PUT",
    body: JSON.stringify(payload),
  });
}

export async function deleteAuthKey(id: number): Promise<void> {
  await apiRequest<void>(`/auth-keys/${id}`, {
    method: "DELETE",
  });
}

export async function toggleAuthKeyStatus(id: number): Promise<AuthKey> {
  return apiRequest<AuthKey>(`/auth-keys/${id}/status`, {
    method: "PATCH",
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
  extra_body: Record<string, unknown>;
  weight: number;
  input_price: number;
  cache_read_price: number;
  output_price: number;
  currency: string;
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
  extra_body?: Record<string, unknown>;
  weight?: number;
  input_price?: number;
  cache_read_price?: number;
  output_price?: number;
  currency?: string;
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

// Metrics API functions
export interface MetricsData {
  reqs: number;
  tokens: number;
}

export interface ModelCount {
  model: string;
  calls: number;
}

export interface ProjectCount {
  project: string;
  calls: number;
}

export async function getMetrics(days: number): Promise<MetricsData> {
  return apiRequest<MetricsData>(`/metrics/use/${days}`);
}

// ---------------------------------------------------------------------------
// 分析聚合（GET /api/metrics/stats）
//
// 单端点返回一个时间切片的全部视图：趋势、五维下钻、延迟分布、错误分析、
// 排行榜。之所以不按维度拆成多个端点，是为了让一次请求对应一个切片——
// 前端「筛选行作用于其下所有图表」的约定因此天然成立，不会出现各图之间
// 因分别请求而产生的时间窗漂移。
//
// 字段名与后端 service/stats.go 的 json tag 一一对应，改后端务必同步这里。
// ---------------------------------------------------------------------------

export interface StatsKPI {
  total: number;
  success: number;
  failed: number;
  running: number;
  /** success + failed，**不含 running**（在途请求不该拉低成功率） */
  finished: number;
  successRate: number;
  promptTokens: number;
  completionTokens: number;
  totalTokens: number;
  cachedTokens: number;
  cacheHitRate: number;
  cost: number;
  currency: string;
  totalRetries: number;
  /** 平均每请求重试次数。不是比率——该值可以大于 1 */
  avgRetries: number;
}

export interface TrendPoint {
  /** 桶起点，Unix 毫秒 */
  ts: number;
  total: number;
  success: number;
  error: number;
  running: number;
  tokens: number;
  prompt: number;
  completion: number;
  /** prompt 中命中缓存的部分，供 Token 构成图拆出"非缓存输入" */
  cached: number;
  avgTps: number;
  avgFirstChunkMs: number;
}

export interface GroupStat {
  name: string;
  total: number;
  success: number;
  error: number;
  running: number;
  successRate: number;
  prompt: number;
  completion: number;
  totalTokens: number;
  cached: number;
  cacheHitRate: number;
  avgTps: number;
  maxTps: number;
  avgFirstChunkMs: number;
  /** 最近秩分位，非插值 */
  p95FirstChunkMs: number;
  avgProxyMs: number;
  retries: number;
  cost: number;
}

export interface ErrorSample {
  id: number;
  createdAt: number;
  error: string;
}

export interface ErrorGroup {
  type: string;
  code: string;
  count: number;
  providers: { name: string; count: number }[];
  models: { name: string; count: number }[];
  samples: ErrorSample[];
}

export interface LatencyStat {
  p50: number;
  p90: number;
  p95: number;
  p99: number;
  avg: number;
  max: number;
  /** 已升序排序的原始样本，可直接画分布 */
  list: number[];
}

export interface StatsLogRow {
  id: number;
  createdAt: number;
  model: string;
  provider: string;
  keyName: string;
  tps: number;
  firstChunkMs: number;
  completionTokens: number;
  promptTokens: number;
  error?: string;
  retry: number;
}

export interface StatsResult {
  generatedAt: number;
  bucketMs: number;
  /** 命中单次扫描上限时为 true，说明时间窗太宽，数字可能不完整 */
  truncated: boolean;
  range: { from: number; to: number };
  kpi: StatsKPI;
  trend: TrendPoint[];
  byModel: GroupStat[];
  byProvider: GroupStat[];
  byKey: GroupStat[];
  byName: GroupStat[];
  byUa: GroupStat[];
  errors: ErrorGroup[];
  errorTrend: TrendPoint[];
  latency: { firstChunk: LatencyStat; tps: LatencyStat; proxyMs: LatencyStat };
  topTps: StatsLogRow[];
  slowest: StatsLogRow[];
  recentErrors: StatsLogRow[];
}

export interface StatsQuery {
  /** Unix 秒/毫秒、RFC3339 或 YYYY-MM-DD，后端都接受 */
  from?: string;
  to?: string;
  /** 分桶档位，缺省 auto */
  granularity?: string;
  provider?: string;
  model?: string;
  name?: string;
  status?: string;
  ua?: string;
  key_id?: string;
}

export async function getStats(query: StatsQuery = {}): Promise<StatsResult> {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== null && value !== "") params.append(key, value);
  }
  const qs = params.toString();
  return apiRequest<StatsResult>(`/metrics/stats${qs ? `?${qs}` : ""}`);
}

export async function getStatsGranularities(): Promise<string[]> {
  return apiRequest<string[]>("/metrics/granularities");
}

export async function getModelCounts(): Promise<ModelCount[]> {
  return apiRequest<ModelCount[]>('/metrics/counts');
}

export async function getProjectCounts(): Promise<ProjectCount[]> {
  return apiRequest<ProjectCount[]>('/metrics/projects');
}

// Test API functions
export async function testModelProvider(id: number): Promise<unknown> {
  return apiRequest<unknown>(`/test/${id}`);
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

// Config API functions
export interface AnthropicCountTokens {
  base_url: string;
  api_key: string;
  version: string;
}

export interface LogCleanupPolicy {
  enabled: boolean;
  retention_days: number;
}

export interface ConfigResponse {
  key: string;
  value: string;
}

export const configAPI = {
  getConfig: (key: string) =>
    apiRequest<ConfigResponse>(`/config/${key}`),

  updateConfig: (key: string, data: unknown) =>
    apiRequest<ConfigResponse>(`/config/${key}`, {
      method: 'PUT',
      body: JSON.stringify({ value: JSON.stringify(data) }),
    }),
};

// Logs API functions
export interface ChatLog {
  ID: number;
  CreatedAt: string;
  Name: string;
  TraceID: string;
  SessionID?: string;
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
  Size: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  prompt_tokens_details: PromptTokensDetails;
  key_name: string;
  input_price: number;
  cache_read_price: number;
  output_price: number;
  currency: string;
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
  Style?: string;
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
    authKeyId?: string;
    traceId?: string;
    sessionId?: string;
    id?: string;
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
  if (filters.authKeyId) params.append("auth_key_id", filters.authKeyId);
  if (filters.traceId) params.append("trace_id", filters.traceId);
  if (filters.sessionId) params.append("session_id", filters.sessionId);
  if (filters.id) params.append("id", filters.id);

  return apiRequest<LogsResponse>(`/logs?${params.toString()}`);
}

/**
 * 取单条日志。
 *
 * 复用列表端点的 `id` 过滤（后端已支持），而不是新增单条端点——
 * 对比页一次最多取 6 条，代价可接受，换来的是后端接口面不再扩大。
 * 查不到时返回 null，由调用方决定如何呈现（对比页会明确列出"该条已不存在"）。
 */
export async function getLogById(id: number): Promise<ChatLog | null> {
  const res = await getLogs(1, 1, { id: String(id) });
  return res.data[0] ?? null;
}

export async function getChatIO(logId: number): Promise<ChatIO> {
  return apiRequest<ChatIO>(`/logs/${logId}/chat-io`);
}

// Clean logs API
export interface CleanLogsResult {
  deleted_count: number;
}

export async function cleanLogs(params: {
  type: 'count' | 'days';
  value: number;
}): Promise<CleanLogsResult> {
  return apiRequest<CleanLogsResult>('/logs/cleanup', {
    method: 'POST',
    body: JSON.stringify(params),
  });
}

export interface LogCleanupRecord {
  ID: number;
  CreatedAt: string;
  RetentionDays: number;
  DeletedCount: number;
  DurationMs: number;
  Source: string;
  Type: string;
}

export async function getCleanupHistory(params: {
  page?: number;
  page_size?: number;
} = {}): Promise<PaginatedResponse<LogCleanupRecord>> {
  const searchParams = new URLSearchParams();
  if (params.page) searchParams.append('page', params.page.toString());
  if (params.page_size) searchParams.append('page_size', params.page_size.toString());
  const query = searchParams.toString();
  return apiRequest<PaginatedResponse<LogCleanupRecord>>(
    query ? `/logs/cleanup/history?${query}` : '/logs/cleanup/history'
  );
}

// Test API functions
export async function testCountTokens(): Promise<void> {
  return apiRequest<void>('/test/count_tokens');
}

// GitHub Release API
export interface GitHubRelease {
  tag_name: string;
  name: string;
  published_at: string;
  html_url: string;
  body: string;
}

export async function checkLatestRelease(owner: string, repo: string): Promise<GitHubRelease | null> {
  try {
    const response = await fetch(
      `https://api.github.com/repos/${owner}/${repo}/releases/latest`,
      {
        headers: {
          'Accept': 'application/vnd.github+json',
        },
      }
    );

    if (!response.ok) {
      console.warn('Failed to fetch latest release:', response.status);
      return null;
    }

    const data = await response.json();
    return {
      tag_name: data.tag_name,
      name: data.name,
      published_at: data.published_at,
      html_url: data.html_url,
      body: data.body,
    };
  } catch (error) {
    console.error('Error checking for updates:', error);
    return null;
  }
}

// ---------------------------------------------------------------------------
// 配额（余量）
// ---------------------------------------------------------------------------
//
// 类型定义在 @/lib/quota，与配额页共用一个真相来源；这里只做端点包装。
// 八个端点里只有两个在**未落盘**的数据上工作（run / test），它们不会改变
// 任何状态——因此只读模式下 test 依然开放（否则用户没法调脚本）。

/** 读配置（含内置适配器清单、配置文件路径、写权限开关）。 */
export async function getQuotaConfig(): Promise<QuotaConfigResponse> {
  return apiRequest<QuotaConfigResponse>("/quota/config");
}

/**
 * 改配置级字段（缓存时长、告警阈值）。
 *
 * 与数据源的保存分开：这两个字段不属于任何源，混在一起会让
 * "我只想改个阈值"变��一次数据源写操作。
 */
export async function updateQuotaConfig(cfg: {
  refreshInterval: number
  warningAt: number
}): Promise<QuotaConfig> {
  return apiRequest<QuotaConfig>("/quota/config", {
    method: "PUT",
    body: JSON.stringify(cfg),
  })
}

/** 新增数据源。不带 id 时由服务端生成。 */
export async function createQuotaSource(source: QuotaSource): Promise<QuotaSource> {
  return apiRequest<QuotaSource>("/quota/sources", {
    method: "POST",
    body: JSON.stringify(source),
  });
}

/** 更新数据源。按 id 命中已有项。 */
export async function updateQuotaSource(source: QuotaSource): Promise<QuotaSource> {
  return apiRequest<QuotaSource>("/quota/sources", {
    method: "PUT",
    body: JSON.stringify(source),
  });
}

export async function deleteQuotaSource(id: string): Promise<void> {
  return apiRequest<void>(`/quota/sources/${encodeURIComponent(id)}`, { method: "DELETE" });
}

/**
 * 跑全部启用源（或指定 id）。
 *
 * force=true 忽略缓存重新取数。ids 为空时不加该参数——
 * 服务端把空列表解释为"全部"，显式发一个空的 ids= 反而会被当成
 * "一个源都不跑"（splitIDs 会返回 nil，语义上没有区别，但不发更清楚）。
 */
export async function runQuotaSources(
  opts: { force?: boolean; ids?: string[] } = {}
): Promise<QuotaRunResult> {
  const params = new URLSearchParams();
  if (opts.force) params.append("force", "true");
  if (opts.ids?.length) params.append("ids", opts.ids.join(","));
  const qs = params.toString();
  return apiRequest<QuotaRunResult>(`/quota/run${qs ? `?${qs}` : ""}`, { method: "POST" });
}

/** 只刷一个数据源，其余走缓存。 */
export async function refreshQuotaSource(id: string): Promise<QuotaRunResult> {
  return apiRequest<QuotaRunResult>(`/quota/sources/${encodeURIComponent(id)}/refresh`, {
    method: "POST",
  });
}

/**
 * 试跑一个**未保存**的数据源。不落盘、不写缓存，只读模式下也开放。
 */
export async function testQuotaSource(source: QuotaSource): Promise<QuotaTestResult> {
  return apiRequest<QuotaTestResult>("/quota/test", {
    method: "POST",
    body: JSON.stringify(source),
  });
}

/** 列出上游 llmio 供应商并给出导入建议（密钥已掩码）。 */
export async function discoverQuotaSources(): Promise<QuotaUpstreamCandidate[]> {
  return apiRequest<QuotaUpstreamCandidate[]>("/quota/discover");
}

/**
 * 从上游供应商导入一个数据源。
 *
 * **只提交 upstreamId**：密钥由服务端直接从上游配置取，不经过浏览器。
 * 传别的字段不会生效，也不要在这里加密钥——那正是这条设计要避免的。
 */
export async function importQuotaSource(upstreamId: number): Promise<QuotaSource> {
  return apiRequest<QuotaSource>("/quota/import", {
    method: "POST",
    body: JSON.stringify({ upstreamId }),
  });
}
