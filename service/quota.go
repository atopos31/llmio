package service

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/quota"
	"gorm.io/gorm"
)

// 本文件是配额子系统的编排层。分三层，与 stats.go 同一约定：
//
//  1. 纯函数（分类、排序、摘要、发现建议）——无 IO，可完整覆盖
//  2. QuotaStore —— 配置读写 + 缓存 + 扇出调度
//  3. 从上游 llmio 供应商发现/导入
//
// 设计约定：对外可见的口径（排序规则、摘要口径、"最差"的定义）都写在注释里，
// 因为它们是行为承诺——前端会直接展示这些结果。

// randomIDHex 是生成 id 后缀的随机源。做成变量而非直接调用，
// 是为了让"随机源不可用"这一分支可注入——用途只是让 id 不撞车，
// 不值得为它引入错误返回。
var randomIDHex = func(n int) string {
	const hex = "0123456789abcdef"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return strings.Repeat("0", n)
	}
	for i := range b {
		b[i] = hex[int(b[i])%16]
	}
	return string(b)
}

// MinCacheTTL 是缓存的最小存活时间。
//
// 即便用户把刷新间隔调到 1 秒，也不该真的每 1 秒去打一遍所有上游：
// 余量接口本身多是分钟级的，打太勤只会被上游限流。
const MinCacheTTL = 10 * time.Second

// 各类数据源的默认超时。
const (
	defaultBuiltinTimeout = 20 * time.Second
	defaultHTTPTimeout    = 20 * time.Second
	defaultScriptTimeout  = 30 * time.Second
)

// ---------------------------------------------------------------------------
// 对外结果类型
// ---------------------------------------------------------------------------

// UpstreamSuggestion 是对一个 llmio 供应商建议的配额源配置。
type UpstreamSuggestion struct {
	Type    quota.DataSourceType `json:"type"`
	Builtin string               `json:"builtin,omitempty"`
	Note    string               `json:"note,omitempty"`
}

// UpstreamCandidate 是从上游发现的一个可导入供应商。
type UpstreamCandidate struct {
	UpstreamID      uint               `json:"upstreamId"`
	Name            string             `json:"name"`
	Type            string             `json:"type"`
	BaseURL         string             `json:"baseUrl"`
	APIKeyMasked    string             `json:"apiKeyMasked"`
	HasAPIKey       bool               `json:"hasApiKey"`
	AlreadyImported bool               `json:"alreadyImported"`
	Suggested       UpstreamSuggestion `json:"suggested"`
}

// SourceResult 是一个数据源的取数结果。
type SourceResult struct {
	ID        string               `json:"id"`
	Name      string               `json:"name"`
	Type      quota.DataSourceType `json:"type"`
	Enabled   bool                 `json:"enabled"`
	Note      string               `json:"note,omitempty"`
	OK        bool                 `json:"ok"`
	Items     []quota.Item         `json:"items"`
	Status    quota.Status         `json:"status"`
	Error     string               `json:"error,omitempty"`
	Warning   string               `json:"warning,omitempty"`
	LatencyMs int64                `json:"latencyMs"`
	UpdatedAt int64                `json:"updatedAt"`
	// Cached 表示这一条来自缓存而非本次实取。
	Cached bool `json:"cached"`
}

// WorstItem 是摘要里的"最紧张的一条"。
type WorstItem struct {
	ID         string       `json:"id"`
	Label      string       `json:"label"`
	Status     quota.Status `json:"status"`
	Percent    *float64     `json:"percent"`
	SourceID   string       `json:"sourceId"`
	SourceName string       `json:"sourceName"`
}

// QuotaSummary 是整体摘要。
type QuotaSummary struct {
	TotalSources int `json:"totalSources"`
	OKSources    int `json:"okSources"`
	FailSources  int `json:"failSources"`
	TotalItems   int `json:"totalItems"`
	// Worst 是所有成功源里状态最差的那一条（没有任何条目时为 null）。
	//
	// 必须带 sourceName：面板要在摘要条上直接显示"哪一条最紧张"，
	// 只说状态不说来源等于没说。
	Worst *WorstItem `json:"worst"`
}

// RunAllResult 是一次整体刷新的结果。
type RunAllResult struct {
	GeneratedAt     int64          `json:"generatedAt"`
	RefreshInterval int            `json:"refreshInterval"`
	WarningAt       float64        `json:"warningAt"`
	Sources         []SourceResult `json:"sources"`
	Summary         QuotaSummary   `json:"summary"`
}

// TestResult 是试跑一个未保存数据源的结果。
type TestResult struct {
	OK      bool         `json:"ok"`
	Items   []quota.Item `json:"items"`
	Status  quota.Status `json:"status"`
	Error   string       `json:"error,omitempty"`
	Warning string       `json:"warning,omitempty"`
	// RawValue 是适配器产出的原始值（**未归一**），供试跑面板回显。
	// 面板已有 JsonTree 组件可展示它，不必在这里预先摊平。
	RawValue   any   `json:"rawValue,omitempty"`
	DurationMs int64 `json:"durationMs"`
}

// ---------------------------------------------------------------------------
// 纯函数：排序、摘要、发现建议
// ---------------------------------------------------------------------------

// SortSourceResults 排序取数结果。
//
// 规则（行为承诺）：**成功的在前**，同组内按名称升序。
// 失败源沉底让"哪个源挂了"一眼可见；名称升序保证同一份配置每次顺序稳定。
func SortSourceResults(rs []SourceResult) {
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].OK != rs[j].OK {
			return rs[i].OK
		}
		return rs[i].Name < rs[j].Name
	})
}

// BuildSummary 汇总所有结果。
//
// 口径（行为承诺）：
//
//   - 只统计**成功**源里的条目。失败源没有可信数据，算进去会稀释最差的判断。
//   - "最差"先比状态序（quota.StatusRank），状态相同再比已用百分比（高者更差）；
//     都没有百分比时取先遇到的（结果已按名称排序，因此稳定）。
func BuildSummary(results []SourceResult) QuotaSummary {
	sum := QuotaSummary{TotalSources: len(results)}

	var worst *WorstItem
	var worstPct float64 = -1
	for _, r := range results {
		if !r.OK {
			continue
		}
		sum.OKSources++
		sum.TotalItems += len(r.Items)
		for _, it := range r.Items {
			if worst != nil {
				cmp := quota.StatusRank(it.Status) - quota.StatusRank(worst.Status)
				if cmp < 0 {
					continue
				}
				if cmp == 0 {
					pct := -1.0
					if it.Percent != nil {
						pct = *it.Percent
					}
					if pct <= worstPct {
						continue
					}
				}
			}
			pct := -1.0
			if it.Percent != nil {
				pct = *it.Percent
			}
			worstPct = pct
			worst = &WorstItem{
				ID: it.ID, Label: it.Label, Status: it.Status, Percent: it.Percent,
				SourceID: r.ID, SourceName: r.Name,
			}
		}
	}
	sum.FailSources = sum.TotalSources - sum.OKSources
	sum.Worst = worst
	return sum
}

// SuggestForUpstream 按供应商名与地址建议配额源类型。
//
// 启发式（与原实现一致）：命中已知供应商给内置适配器，否则给 HTTP 类型让用户自填。
func SuggestForUpstream(name, baseURL string) UpstreamSuggestion {
	hay := strings.ToLower(name + " " + baseURL)
	switch {
	case strings.Contains(hay, "deepseek"):
		return UpstreamSuggestion{
			Type: quota.TypeBuiltin, Builtin: "deepseek",
			Note: "已实测的官方余额接口",
		}
	case strings.Contains(hay, "moonshot") || strings.Contains(hay, "kimi"):
		return UpstreamSuggestion{
			Type: quota.TypeBuiltin, Builtin: "moonshot",
			Note: "Moonshot / Kimi 官方余额接口",
		}
	case strings.Contains(hay, "scnet") || strings.Contains(hay, "超算"):
		return UpstreamSuggestion{
			Type: quota.TypeBuiltin, Builtin: "scnet",
			Note: "官方 API 无用量端点，内置适配器走控制台会话（需填账号口令）",
		}
	case strings.Contains(hay, "opencode"):
		return UpstreamSuggestion{
			Type: quota.TypeBuiltin, Builtin: "opencode",
			Note: "内置适配器走控制台会话（需填会话 Cookie 与 x-org-id）",
		}
	default:
		return UpstreamSuggestion{
			Type: quota.TypeHTTP,
			Note: "未识别的供应商，请用 HTTP 类型手动配置接口地址与字段映射",
		}
	}
}

// upstreamProviderConfig 是上游 llmio 里 Provider.Config 的形状。
//
// 只取发现/导入需要的两个字段。键名沿用 providers 包（base_url / api_key），
// 不在这里另立一套解读。
type upstreamProviderConfig struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
}

// ParseUpstreamProviderConfig 从 Provider.Config JSON 里取地址与密钥。
//
// 解析失败返回空值而不是报错：上游某个供应商配置坏了，
// 不该让整张发现表都打不开。
func ParseUpstreamProviderConfig(raw string) (baseURL, apiKey string) {
	var cfg upstreamProviderConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return "", ""
	}
	return cfg.BaseURL, cfg.APIKey
}

// ImportedSourceID 是从某个上游供应商导入的数据源 id。
//
// 统一前缀是为了让「已导入」判定稳定——重复导入同一个上游不该产生第二个源。
func ImportedSourceID(upstreamID uint) string {
	return fmt.Sprintf("llmio-%d", upstreamID)
}

// BuildCandidates 由上游供应商列表构造发现表。
func BuildCandidates(providers []models.Provider, existingIDs map[string]bool) []UpstreamCandidate {
	out := make([]UpstreamCandidate, 0, len(providers))
	for _, p := range providers {
		baseURL, apiKey := ParseUpstreamProviderConfig(p.Config)
		out = append(out, UpstreamCandidate{
			UpstreamID:      p.ID,
			Name:            p.Name,
			Type:            p.Type,
			BaseURL:         baseURL,
			APIKeyMasked:    quota.MaskSecret(apiKey),
			HasAPIKey:       strings.TrimSpace(apiKey) != "",
			AlreadyImported: existingIDs[ImportedSourceID(p.ID)],
			Suggested:       SuggestForUpstream(p.Name, baseURL),
		})
	}
	return out
}

// BuildImportedSource 按建议构造要导入的数据源。
//
// **密钥由服务端从上游配置直接取，不经过浏览器**（计划 §3.3）。
// 这条在同源之后依然要显式保持：前端只提交 upstreamId。
func BuildImportedSource(p models.Provider) (quota.Source, error) {
	baseURL, apiKey := ParseUpstreamProviderConfig(p.Config)
	sup := SuggestForUpstream(p.Name, baseURL)

	src := quota.Source{
		ID:      ImportedSourceID(p.ID),
		Name:    p.Name,
		Type:    sup.Type,
		Note:    sup.Note,
		BaseURL: baseURL,
		APIKey:  apiKey,
	}

	switch sup.Type {
	case quota.TypeBuiltin:
		src.Builtin = sup.Builtin
		envKeys, needsLogin := builtinLoginEnvKeys(sup.Builtin)
		if needsLogin {
			// 登录型适配器导入时先**禁用**并留出待填的 env 键：
			// 直接启用会立刻报"缺少账号口令"，标成禁用更贴合"还需你补一步"的事实。
			src.Enabled = false
			src.Env = map[string]string{}
			for _, k := range envKeys {
				src.Env[k] = ""
			}
		} else {
			src.Enabled = true
		}
	case quota.TypeHTTP:
		// 未识别的供应商：给一个占位地址并禁用，等用户补全接口与字段映射。
		// 启用一个必然失败的源只会让面板一直报错。
		src.Enabled = false
		if baseURL != "" {
			src.URL = strings.TrimSuffix(baseURL, "/") + "/user/balance"
		}
		src.Method = http.MethodGet
		src.ItemsPath = ""
		src.Map = map[string]any{}
	default:
		return quota.Source{}, fmt.Errorf("无法为供应商 %q 推导出配额源类型", p.Name)
	}
	return src, nil
}

// builtinLoginEnvKeys 返回需要登录的适配器所要求的 env 键。
func builtinLoginEnvKeys(id string) ([]string, bool) {
	for _, b := range quota.ListBuiltins() {
		if b.ID == id {
			return b.EnvKeys, b.NeedsLogin
		}
	}
	return nil, false
}

// builtinExists 判断内置适配器是否存在。
func builtinExists(id string) bool {
	for _, b := range quota.ListBuiltins() {
		if b.ID == id {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// QuotaStore：配置 + 缓存 + 扇出
// ---------------------------------------------------------------------------

// QuotaStore 持有配置与缓存。
//
// 缓存按数据源 id 存，进程内。不做持久化：余量是实时量，
// 重启后从空缓存开始重新取一遍才是正确行为。
type QuotaStore struct {
	path string

	mu    sync.Mutex
	cache map[string]cachedResult
}

type cachedResult struct {
	ts   time.Time
	data SourceResult
}

// NewQuotaStore 建一个 Store。path 为空时用 quota.ConfigPath()。
func NewQuotaStore(path string) *QuotaStore {
	if strings.TrimSpace(path) == "" {
		path = quota.ConfigPath()
	}
	return &QuotaStore{path: path, cache: map[string]cachedResult{}}
}

// Path 返回配置文件路径。
func (s *QuotaStore) Path() string { return s.path }

// Load 读配置。
func (s *QuotaStore) Load() (*quota.Config, error) {
	return quota.LoadConfig(s.path)
}

// SafeConfig 返回脱敏后的配置，供下发前端。
func (s *QuotaStore) SafeConfig() (*quota.Config, error) {
	cfg, err := s.Load()
	if err != nil {
		return nil, err
	}
	return quota.MaskConfig(cfg), nil
}

// Save 校验后写配置，并使缓存整体失效。
func (s *QuotaStore) Save(cfg *quota.Config) error {
	if err := quota.ValidateConfig(cfg); err != nil {
		return err
	}
	if err := quota.SaveConfig(s.path, cfg); err != nil {
		return err
	}
	// 配置变了，缓存里的结果可能已与新配置不符（改了地址、超时、映射），整体清掉
	s.Invalidate("")
	return nil
}

// Invalidate 清缓存。id 为空时清空全部。
func (s *QuotaStore) Invalidate(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		s.cache = map[string]cachedResult{}
		return
	}
	delete(s.cache, id)
}

// cacheTTL 由刷新间隔算出，但不低于 MinCacheTTL。
func (s *QuotaStore) cacheTTL(cfg *quota.Config) time.Duration {
	d := time.Duration(cfg.RefreshInterval) * time.Second
	if d < MinCacheTTL {
		d = MinCacheTTL
	}
	return d
}

// UpsertSource 新增或更新一个数据源，返回脱敏后的结果。
//
// 密钥回填在这里做：前端读到的是掩码，回传时（未改动）需要还原成已保存的真实值。
//
// **先把 id 归一，再落盘**：不这么做的话，不带 id 的新增会带着空 id 走到
// MaskSource 与 Invalidate —— 前者会把空字符串打码成"****"写进配置，
// 后者（id 为空 = 清空全部）会把整份缓存白清一遍。
func (s *QuotaStore) UpsertSource(incoming quota.Source, isNew bool) (quota.Source, error) {
	cfg, err := s.Load()
	if err != nil {
		return quota.Source{}, err
	}

	incoming.ID = strings.TrimSpace(incoming.ID)
	if incoming.ID == "" {
		incoming.ID = quota.NewSourceID(randomIDHex(8))
	}

	idx := -1
	for i := range cfg.Sources {
		if cfg.Sources[i].ID == incoming.ID {
			idx = i
			break
		}
	}
	if isNew && idx >= 0 {
		return quota.Source{}, fmt.Errorf("数据源 id 已存在：%s", incoming.ID)
	}

	var saved *quota.Source
	if idx >= 0 {
		saved = &cfg.Sources[idx]
	}
	merged := quota.ResolveSourceSecrets(incoming, saved)
	if err := quota.ValidateSource(merged); err != nil {
		return quota.Source{}, err
	}

	if idx >= 0 {
		cfg.Sources[idx] = merged
	} else {
		cfg.Sources = append(cfg.Sources, merged)
	}
	if err := quota.SaveConfig(s.path, cfg); err != nil {
		return quota.Source{}, err
	}
	s.Invalidate(merged.ID)
	return quota.MaskSource(merged), nil
}

// RemoveSource 删除一个数据源。
func (s *QuotaStore) RemoveSource(id string) (bool, error) {
	cfg, err := s.Load()
	if err != nil {
		return false, err
	}
	kept := make([]quota.Source, 0, len(cfg.Sources))
	found := false
	for _, src := range cfg.Sources {
		if src.ID == id {
			found = true
			continue
		}
		kept = append(kept, src)
	}
	if !found {
		return false, nil
	}
	cfg.Sources = kept
	if err := quota.SaveConfig(s.path, cfg); err != nil {
		return false, err
	}
	s.Invalidate(id)
	return true, nil
}

// RunAll 跑全部启用源（或指定 id），带缓存。
//
// 并发跑所有目标源：一个慢源不该拖住其他源（计划 §1.4 第 20 条）。
// 非 force 时复用未过期的缓存条目。
func (s *QuotaStore) RunAll(ctx context.Context, force bool, ids []string) (*RunAllResult, error) {
	cfg, err := s.Load()
	if err != nil {
		return nil, err
	}
	ttl := s.cacheTTL(cfg)
	now := time.Now()

	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	targets := make([]quota.Source, 0, len(cfg.Sources))
	for _, src := range cfg.Sources {
		if !src.Enabled {
			continue
		}
		if len(ids) > 0 && !want[src.ID] {
			continue
		}
		targets = append(targets, src)
	}

	results := make([]SourceResult, len(targets))
	var wg sync.WaitGroup
	for i, src := range targets {
		wg.Add(1)
		go func(i int, src quota.Source) {
			defer wg.Done()
			if hit, ok := s.cached(src.ID, ttl, now, force); ok {
				results[i] = hit
				return
			}
			res := s.runSource(ctx, src, cfg.WarningAt)
			s.store(src.ID, res)
			results[i] = res
		}(i, src)
	}
	wg.Wait()

	SortSourceResults(results)
	return &RunAllResult{
		GeneratedAt:     now.UnixMilli(),
		RefreshInterval: cfg.RefreshInterval,
		WarningAt:       cfg.WarningAt,
		Sources:         results,
		Summary:         BuildSummary(results),
	}, nil
}

// cached 取未过期的缓存条目。
func (s *QuotaStore) cached(id string, ttl time.Duration, now time.Time, force bool) (SourceResult, bool) {
	if force {
		return SourceResult{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	hit, ok := s.cache[id]
	if !ok || now.Sub(hit.ts) >= ttl {
		return SourceResult{}, false
	}
	out := hit.data
	out.Cached = true
	return out, true
}

// store 写缓存。**只缓存成功的结果**：失败常是瞬时的（网络抖动、上游 5xx），
// 缓存失败会把一次抖动放大成整个 TTL 窗口的持续报错。
func (s *QuotaStore) store(id string, res SourceResult) {
	if !res.OK {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[id] = cachedResult{ts: time.Now(), data: res}
}

// RefreshSource 只刷一个源，其余走缓存。
//
// 这是"每源独立刷新"的实现技巧（计划 §3.3）：先 force 单源，
// 再取整体（其余源命中缓存，不会真的重跑）。面板上点某个慢源的刷新时用它。
func (s *QuotaStore) RefreshSource(ctx context.Context, id string) (*RunAllResult, error) {
	if _, err := s.RunAll(ctx, true, []string{id}); err != nil {
		return nil, err
	}
	return s.RunAll(ctx, false, nil)
}

// runSource 跑一个源并归一。
func (s *QuotaStore) runSource(ctx context.Context, src quota.Source, warningAt float64) SourceResult {
	started := time.Now()
	res := SourceResult{
		ID: src.ID, Name: src.Name, Type: src.Type, Enabled: src.Enabled, Note: src.Note,
		UpdatedAt: started.UnixMilli(),
	}

	warn := warningAt
	if src.WarningAt > 0 {
		warn = src.WarningAt
	}

	raw, warning, err := s.fetchRows(ctx, src)
	res.LatencyMs = time.Since(started).Milliseconds()
	if err != nil {
		res.Status = quota.StatusUnknown
		res.Error = err.Error()
		return res
	}
	res.Warning = warning

	items, err := quota.Normalize(raw, quota.NormalizeOptions{WarningAt: warn, IDPrefix: src.ID + ":"})
	if err != nil {
		res.Status = quota.StatusUnknown
		res.Error = err.Error()
		return res
	}

	res.OK = true
	res.Items = items
	res.Status = quota.WorstStatus(items)
	res.UpdatedAt = time.Now().UnixMilli()
	return res
}

// fetchRows 按类型取原始产出，返回 (原始值, 警告, 错误)。
//
// 返回 any 而不是先摊成行：契约的 Normalize 本就接受任意形状
// （{items:[]} / 裸数组 / {data:[]} / 对象映射 / 单条对象），
// 在这里先摊一层是多余的，而且会把"原始产出"丢掉——
// 试跑面板正需要原样回显它。归一只在 runSource / TestSource 里做一次。
func (s *QuotaStore) fetchRows(ctx context.Context, src quota.Source) (any, string, error) {
	switch src.Type {
	case quota.TypeBuiltin:
		res, err := quota.RunBuiltin(toBuiltinConfig(src), nil)
		if err != nil {
			return nil, "", err
		}
		return res.Items, "", nil

	case quota.TypeHTTP:
		client := &http.Client{Timeout: sourceTimeout(src, defaultHTTPTimeout)}
		res, err := quota.RunHTTP(toHTTPConfig(src), quota.HTTPAdapterVars{
			APIKey: src.APIKey, BaseURL: src.BaseURL, ID: src.ID, Name: src.Name,
		}, client)
		if err != nil {
			return nil, "", err
		}
		return res.Items, "", nil

	case quota.TypeScript:
		result, err := quota.RunScript(ctx, quota.ScriptRequest{
			Source:         src.ScriptSource,
			Env:            src.Env,
			TimeoutMs:      int(sourceTimeout(src, defaultScriptTimeout) / time.Millisecond),
			AllowFetch:     src.AllowFetch,
			MaxOutputBytes: quota.DefaultMaxOutputBytes,
		})
		if err != nil {
			return nil, "", err
		}
		warning := ""
		if result.Source != "" {
			warning = "取值来自 " + result.Source
		}
		return result.Value, warning, nil

	default:
		return nil, "", fmt.Errorf("未知数据源类型：%s", src.Type)
	}
}

// sourceTimeout 取该源的实际超时。
func sourceTimeout(src quota.Source, fallback time.Duration) time.Duration {
	if src.Timeout > 0 {
		return time.Duration(src.Timeout) * time.Second
	}
	return fallback
}

// toBuiltinConfig 把数据源配置转成内置适配器参数。
func toBuiltinConfig(src quota.Source) quota.BuiltinConfig {
	return quota.BuiltinConfig{
		BuiltinID: src.Builtin,
		BaseURL:   src.BaseURL,
		APIKey:    src.APIKey,
		Path:      src.Path,
		Method:    src.Method,
		Query:     src.Query,
		Headers:   src.Headers,
		ItemsPath: src.ItemsPath,
		Map:       src.Map,
		TimeoutMs: src.Timeout * 1000,
		Env:       src.Env,
		ID:        src.ID,
		Name:      src.Name,
	}
}

// toHTTPConfig 把数据源配置转成 HTTP 适配器参数。
func toHTTPConfig(src quota.Source) quota.HTTPAdapterConfig {
	cfg := quota.HTTPAdapterConfig{
		ID:        src.ID,
		Name:      src.Name,
		URL:       src.URL,
		Method:    src.Method,
		Headers:   src.Headers,
		Body:      src.Body,
		ItemsPath: src.ItemsPath,
		Map:       src.Map,
		Constants: src.Constants,
		TimeoutMs: src.Timeout * 1000,
	}
	if src.Auth != nil {
		cfg.Auth = *src.Auth
	}
	return cfg
}

// ---------------------------------------------------------------------------
// 试跑
// ---------------------------------------------------------------------------

// TestSource 试跑一个**未保存**的数据源。
//
// 不落盘、不写缓存：这是配额功能里最有价值的作者循环（计划 §4.3）——
// 用户要能反复调整脚本/映射并立刻看到解析结果与原始输出。
// 脚本源码直接从请求里取，不需要临时文件（goja 替换 spawn 之后的简化）。
func (s *QuotaStore) TestSource(ctx context.Context, src quota.Source) *TestResult {
	started := time.Now()
	fail := func(msg string) *TestResult {
		return &TestResult{Status: quota.StatusUnknown, Error: msg,
			DurationMs: time.Since(started).Milliseconds()}
	}

	// 试跑时若密钥是掩码或留空，回填已保存的值——否则用户不动密钥就没法试跑
	if saved := s.findSaved(src.ID); saved != nil {
		src = quota.ResolveSourceSecrets(src, saved)
	}
	// 登录型适配器可能尚未保存、env 还空着，试跑前补齐默认键以便校验给对提示
	if src.Type == quota.TypeBuiltin {
		if keys, needsLogin := builtinLoginEnvKeys(src.Builtin); needsLogin && src.Env == nil {
			src.Env = map[string]string{}
			for _, k := range keys {
				src.Env[k] = ""
			}
		}
	}

	if err := quota.ValidateSource(src); err != nil {
		return fail(err.Error())
	}

	cfg, err := s.Load()
	if err != nil {
		return fail(err.Error())
	}
	warn := cfg.WarningAt
	if src.WarningAt > 0 {
		warn = src.WarningAt
	}

	raw, warning, err := s.fetchRows(ctx, src)
	if err != nil {
		return fail(err.Error())
	}
	items, err := quota.Normalize(raw, quota.NormalizeOptions{WarningAt: warn, IDPrefix: src.ID + ":"})
	if err != nil {
		res := fail(err.Error())
		res.Warning = warning
		res.RawValue = raw
		return res
	}
	return &TestResult{
		OK: true, Items: items, Status: quota.WorstStatus(items), Warning: warning,
		RawValue: raw, DurationMs: time.Since(started).Milliseconds(),
	}
}

// findSaved 找已保存的同 id 数据源。
func (s *QuotaStore) findSaved(id string) *quota.Source {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	cfg, err := s.Load()
	if err != nil {
		return nil
	}
	for i := range cfg.Sources {
		if cfg.Sources[i].ID == id {
			return &cfg.Sources[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 发现与导入
// ---------------------------------------------------------------------------

// Discover 列出上游供应商并给出导入建议。
func (s *QuotaStore) Discover(providers []models.Provider) ([]UpstreamCandidate, error) {
	cfg, err := s.Load()
	if err != nil {
		return nil, err
	}
	existing := map[string]bool{}
	for _, src := range cfg.Sources {
		existing[src.ID] = true
	}
	return BuildCandidates(providers, existing), nil
}

// ImportFromUpstream 从上游供应商导入一个数据源。
//
// 密钥在服务端直接取自上游配置并写入配额配置，**不经过浏览器**。
// 导入时**不做完整校验**：登录型适配器的 env 还是空的，用户随后才补；
// 但类型与地址必须能推导出来，否则存下去也是个坏配置。
func (s *QuotaStore) ImportFromUpstream(p models.Provider) (quota.Source, error) {
	cfg, err := s.Load()
	if err != nil {
		return quota.Source{}, err
	}
	id := ImportedSourceID(p.ID)
	for _, src := range cfg.Sources {
		if src.ID == id {
			return quota.Source{}, fmt.Errorf("该供应商已导入为数据源 %s", id)
		}
	}

	src, err := BuildImportedSource(p)
	if err != nil {
		return quota.Source{}, err
	}
	switch src.Type {
	case quota.TypeBuiltin:
		if !builtinExists(src.Builtin) {
			return quota.Source{}, fmt.Errorf("未知内置适配器：%s", src.Builtin)
		}
	case quota.TypeHTTP:
		if strings.TrimSpace(src.URL) == "" {
			return quota.Source{}, fmt.Errorf("无法从上游配置推导出接口地址，请手动配置后再导入")
		}
	}

	cfg.Sources = append(cfg.Sources, src)
	if err := quota.SaveConfig(s.path, cfg); err != nil {
		return quota.Source{}, err
	}
	s.Invalidate(src.ID)
	return quota.MaskSource(src), nil
}

// ---------------------------------------------------------------------------
// 数据库薄层：给 handler 用的供应商查询
// ---------------------------------------------------------------------------

// GetProvidersForQuota 列出上游供应商，供配额发现使用。
//
// 放在这里而不是让 handler 直接查库：handler 不应知道 GORM 的存在
// （与 stats.go 的 LoadChatLogs 同一约定）。
func GetProvidersForQuota(ctx context.Context) ([]models.Provider, error) {
	return gorm.G[models.Provider](models.DB).Find(ctx)
}

// FindProviderForQuota 按 id 取一个供应商，供配额导入使用。
func FindProviderForQuota(ctx context.Context, id uint) (models.Provider, error) {
	return gorm.G[models.Provider](models.DB).Where("id = ?", id).First(ctx)
}
