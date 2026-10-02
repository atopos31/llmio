// API client for interacting with the backend

import type {
  QuotaConfig,
  QuotaConfigResponse,
  QuotaRunResult,
  QuotaSource,
  QuotaTestResult,
} from "@/lib/quota"
import type { PeakCalendar, PeakHolidaySyncResult, PeakTerms, PreviewResult } from "@/lib/peak"

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
  // 优先匹配相同协议。null/undefined = 没配过（转换功能上线前建的模型），
  // 后端按"开"处理
  PreferDirect?: boolean | null;
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
  /** 这一条关联自己的峰谷条款；null = 没配（按基础价计费）。 */
  Peak: PeakTerms | null;
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
  prefer_direct: boolean;
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
  prefer_direct?: boolean;
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
  /** null 表示这条关联没有峰谷条款；后端会把它落成 NULL。 */
  peak: PeakTerms | null;
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
  /**
   * 省略或传 null 都会把已有条款清掉（后端在结构体更新之外补了一次显式清空，
   * 否则 GORM 会跳过这个 nil 指针、旧条款一直留在行上）。因此想保留就必须传。
   */
  peak?: PeakTerms | null;
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
  /**
   * 模型名与上游名，**只在 `byModelProvider` 这一维有值**。
   *
   * 行名（`name`）是拼好的「模型 · 上游」，但表格要按模型把上游归成一组，
   * 拿拼好的字符串去拆是不行的：模型名里本身就可能带分隔符。
   */
  model?: string;
  provider?: string;
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
  /** 模型×上游的联合分布。两个边际分布推不出它来，见 service/stats.go。 */
  byModelProvider: GroupStat[];
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

// Test API functions
/**
 * 连通性测试的返回体。
 *
 * 后端把上游的响应原样透传：`message` 里是上游的原始文本（可能是一句 ok/error，
 * 也可能是一整段 JSON），形状随渠道而变，这里只声明界面真正读到的字段。
 * 它此前是 `any`，于是"读了一个不存在的字段"这件事没人管得住。
 */
export interface ConnectivityTestResult {
  /** 上游原文，后端 `common.Response.Message` 固定是 string */
  message?: string;
  /** 仅测试请求本身失败时由前端填入，可能包着任意异常对象 */
  error?: unknown;
  [key: string]: unknown;
}

export async function testModelProvider(id: number): Promise<ConnectivityTestResult> {
  return apiRequest<ConnectivityTestResult>(`/test/${id}`);
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
  // upstream_style 实际使用的上游协议。与 Style 不同即说明这次请求经过了协议转换
  // （见 service/bridge.go）；同协议直连时后端给空值（omitempty）。
  upstream_style?: string;
  // bridge_notes 转换过程中有损的地方，逗号分隔的短码（如 dropped_seed）。
  // 码表见 bridge/bridge.go 的 Note 常量，界面按 logs:bridge.notes.<码> 查译文。
  bridge_notes?: string;
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

// 数据库压缩（历史行的形态迁移）
//
// 与日志清理不同，跑一轮是分钟级的：`runCompression` 与 `rollbackCompression`
// 都是"起个后台任务就返回"，进度只能从 `getCompression()` 轮询。
export interface CompressionPolicy {
  enabled: boolean;
  batch_rows: number;
  batch_bytes: number;
  quiesce_sec: number;
  /** 批与批之间的主动停顿（毫秒），0 表示不停。它调的是迁移的**占用率**，不是快慢。 */
  batch_interval_ms: number;
}

export interface CompressionState {
  status: 'idle' | 'running' | 'paused' | 'done' | 'failed';
  last_id: number;
  max_id: number;
  total_rows: number;
  scanned: number;
  packed: number;
  skipped: number;
  bytes_before: number;
  bytes_after: number;
  /**
   * 全表原文合计，**在「开始迁移」那一次从水位 0 起跑时量一次**。
   *
   * 它是压缩比的分子。`bytes_before` 不能当分子：那个数只覆盖扫过的行，
   * 而分母（真正落库）是全表——跑了一半时它算出来的比值恰好是真值的一半。
   *
   * 0 表示还没量过（从没跑过，或者盘上那份状态是本次改动之前落的）。
   */
  bytes_total: number;
  attempts: number;
  last_error: string;
  started_at: string;
  finished_at: string;
}

export interface CompressionBackup {
  path: string;
  size: number;
  mtime: string;
  at: string;
  // manual / stale / missing / forced —— 见 models.LogCompressBackup
  source: 'manual' | 'stale' | 'missing' | 'forced';
}

export interface CompressionDBStats {
  path: string;
  file_size: number;
  page_size: number;
  page_count: number;
  freelist_count: number;
  auto_vacuum: number;
  rows: number;
  // pending_rows 是"还没迁的行数"，全表扫 typeof（只读记录头，不读载荷）。
  // framed_rows 是已经迁过的行数（含引用帧与逐行帧）。
  pending_rows: number;
  framed_rows: number;
  block_rows: number;
  block_group_rows: number;
  block_group_bytes: number;
  input_column_bytes: number;
  // stats_at 是这组数**量出来的时刻**（unix 毫秒），stale 表示它是上一次的。
  // 后端给这一组数带 5 秒冷却：状态页在迁移期间每 2 秒轮询一次，而其中一项是
  // 全表聚合（真库 185 万页）——每次都真扫，等于自己给自己制造锁竞争。
  stats_at: number;
  stale: boolean;
}

export interface CompressionStatus {
  policy: CompressionPolicy;
  state: CompressionState;
  decompress_state: CompressionState;
  backup: CompressionBackup;
  // db 可能是 null：**量不到库的现状不是错误**。迁移正在写库时这一读会撞
  // SQLITE_BUSY，后端重试后仍失败就如实给 null，而不是把 500 甩给前端
  // （前端一 500 就整卡报错，用户得手动刷新）。进度 state 照常给。
  db: CompressionDBStats | null;
  // reclaim 是**上一次空间回收**的记录（启动期的转换/VACUUM，或手动点的那一下）。
  // 与迁移进度是两件事：迁移把页省出来，回收才把页还给文件系统。
  reclaim: ReclaimState;
  running: boolean;
  // reclaiming 是"此刻有一轮回收在跑"。与 running 分开——它们是两个任务，
  // 可以一个在跑另一个不在。
  reclaiming: boolean;
}

/** 一次空间回收的记录。字段全是后端实测值，不是估算。 */
export interface ReclaimState {
  status: 'idle' | 'running' | 'done' | 'failed';
  /** startup = 启动期做的（转换/VACUUM），manual = 手动点的回收 */
  source: 'startup' | 'manual' | '';
  page_size: number;
  freed_pages: number;
  freed_bytes: number;
  file_size_before: number;
  file_size_after: number;
  freelist_before: number;
  freelist_after: number;
  calls: number;
  duration_ms: number;
  started_at: string;
  finished_at: string;
  /**
   * 为什么停的。**这一栏不是日志，是给用户看的结论**：
   *
   *   empty / budget          —— 正常收工（放完了 / 到点收工，剩下的下次再放）
   *   no_auto_vacuum          —— 这个库没开 auto_vacuum，增量回收是**空操作**。
   *                              点了会"瞬间完成、什么都没变"，不说出来就是骗人。
   *   stalled                 —— 放了一批 freelist 却没少（引擎行为反常），主动收工。
   *                              唯一一个 status=done 却带 last_error 的收工。
   *   converted / vacuumed    —— 启动期做的（转换 / VACUUM）
   *   insufficient_space      —— 启动期预检发现磁盘不够，**跳过了**（服务照常起）
   *   failed                  —— 出错，看 last_error
   */
  stop_reason:
    | 'empty'
    | 'budget'
    | 'no_auto_vacuum'
    | 'stalled'
    | 'converted'
    | 'vacuumed'
    | 'insufficient_space'
    | 'failed'
    | '';
  last_error: string;
}

export async function getCompression(): Promise<CompressionStatus> {
  return apiRequest<CompressionStatus>('/logs/compression');
}

export async function updateCompressionPolicy(
  policy: CompressionPolicy
): Promise<CompressionPolicy> {
  return apiRequest<CompressionPolicy>('/logs/compression/policy', {
    method: 'PUT',
    body: JSON.stringify(policy),
  });
}

/**
 * 起一轮迁移。
 *
 * `full` 从水位 0 重扫全表（已迁过的行会被候选过滤跳过，所以它是
 * "水位不可信了，重扫确认"，不是"再压一遍"）。
 *
 * `acknowledge_no_backup` 是原地迁移的确认：没探到备份又不带它，后端**拒绝开跑**。
 * 别在界面上默认传 true——那道门是唯一的"出事能盖回去"的保证。
 */
export async function runCompression(params: {
  full?: boolean;
  acknowledge_no_backup?: boolean;
} = {}): Promise<{ started: boolean; full: boolean; backup: CompressionBackup }> {
  return apiRequest('/logs/compression/run', {
    method: 'POST',
    body: JSON.stringify({
      full: params.full ?? false,
      acknowledge_no_backup: params.acknowledge_no_backup ?? false,
    }),
  });
}

export async function pauseCompression(): Promise<CompressionState> {
  return apiRequest<CompressionState>('/logs/compression/pause', { method: 'POST' });
}

/**
 * L2 降级：把库里的帧全部还原成明文。
 *
 * 它会让整个库**变大**（帧 → 明文），所以后端要求 confirm 必须是字面量
 * "decompress"，且必须 full=true。调用前请确保有备份：这一趟跑完，
 * 迁移的收益就没了，得重跑一遍才能回来。
 */
export async function rollbackCompression(): Promise<{ started: boolean }> {
  return apiRequest('/logs/compression/decompress', {
    method: 'POST',
    body: JSON.stringify({ full: true, confirm: 'decompress' }),
  });
}

/**
 * 起一轮空间回收：把 freelist 里的页还给文件系统。
 *
 * 它**不动数据**，所以没有 confirm 那道门——但它会**全程持写锁**（读不受影响，
 * 写请求会排队并在 busy_timeout 后失败），所以界面上要标成维护动作、别让人
 * 随手点。后端每次最多占 90 秒就收工，剩下的下次再放。
 *
 * 库没开 auto_vacuum 时这是个空操作，后端会如实回 `no_auto_vacuum`。
 */
export async function reclaimStorage(): Promise<{ started: boolean }> {
  return apiRequest('/logs/compression/reclaim', { method: 'POST' });
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

// ---------------------------------------------------------------------------
// 峰谷计费（分时段 / 工作日定价）
// ---------------------------------------------------------------------------
//
// 四个端点，形状定义在 @/lib/peak（校验、表单映射、预览呈现都在那一层）。
// 端点本身不做任何加工，只做端点包装——除同步那一个，理由见下。
//
// 条款不在这一组里：它挂在「模型 × 上游」关联上，随 model-providers 的增改走
// （见 createModelProvider / updateModelProvider 的 peak 字段）。这里只有全局的
// 工作日日历，以及"拿一份还没保存的条款预览时间轴"。

/** 读全局日历。未配置过时后端直接返回默认日历，不会给 null。 */
export async function getPeakCalendar(): Promise<PeakCalendar> {
  return apiRequest<PeakCalendar>("/peak-calendar");
}

/**
 * 整份覆盖全局日历。
 *
 * 校验失败时后端回的是 HTTP 200 + code 400，message 里点名了是哪一处
 * （"invalid timezone ..."）；apiRequest 会把它原样抛出，界面**必须**直接
 * 显示这句话，不要用"保存失败"盖掉——它是唯一能定位问题的信息。
 */
export async function updatePeakCalendar(cal: PeakCalendar): Promise<PeakCalendar> {
  return apiRequest<PeakCalendar>("/peak-calendar", {
    method: "PUT",
    body: JSON.stringify(cal),
  });
}

/**
 * 回放未来 days 天，返回合并后的时间轴区间与**判定所用的时区**。
 *
 * 传的是**尚未保存**的条款：想先看清效果再决定存不存。日历取的是已保存的
 * 那一份（预览要在关联编辑器里做，那里没有日历的编辑权），因此响应回传时区，
 * 调用方才能按正确的时区渲染时刻。days 由后端限制在 1..31，超界会回 code 400。
 */
export async function previewPeakTerms(terms: PeakTerms, days = 7): Promise<PreviewResult> {
  return apiRequest<PreviewResult>(`/peak-calendar/preview?days=${days}`, {
    method: "POST",
    body: JSON.stringify(terms),
  });
}

/**
 * 同步指定年份的节假日与调休，服务端落盘后返回新日历。
 *
 * 这一个**没有走 apiRequest**：同步要出外网，失败时后端用 HTTP 502 回，
 * 而失败原因（镜像不可达、该年连内置数据都没有……）只写在 body 的 message 里。
 * apiRequest 的 `!response.ok` 分支会在读 body 之前就抛出
 * "API request failed: 502 Bad Gateway"，把唯一有用的那句话丢掉。
 * 因此这里先读 body、再看 code——与本项目"成功失败都是 HTTP 200、
 * 真实状态在 code 里"的约定并不冲突，只是把 502 这个例外也按 message 优先处理。
 */
export async function syncPeakHolidays(year: number): Promise<PeakHolidaySyncResult> {
  const token = localStorage.getItem("authToken");
  const response = await fetch(`${API_BASE}/peak-calendar/holidays/sync?year=${year}`, {
    method: "POST",
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { 'Authorization': `Bearer ${token}` } : {}),
    },
  });

  if (response.status === 401) {
    window.location.href = '/login';
    throw new Error('Unauthorized');
  }

  let body: { code?: number; message?: string; data?: PeakHolidaySyncResult } | null = null;
  try {
    body = await response.json();
  } catch {
    body = null; // 网关之类返回的非 JSON 响应，落到下面的兜底文案
  }

  const fallback = `API request failed: ${response.status} ${response.statusText}`;
  if (!body) {
    throw new Error(fallback);
  }
  if (body.code !== 200) {
    // 这条 message 里写着失败原因（镜像不可达、该年没有数据……），是唯一能据以行动的信息
    throw new Error(body.message || fallback);
  }
  if (!body.data) {
    // code 200 却没带 data 是服务端的 bug。此时 message 通常是 "ok"/"success"，
    // 拿它当错误文案只会把人带偏，宁可回退到状态码
    throw new Error(fallback);
  }
  return body.data;
}
