package service

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
)

// 本文件是分析聚合引擎。分为三层：
//
//  1. 纯函数原语（分桶、分位、错误归类、计费）——无 IO，可 100% 覆盖
//  2. Aggregate —— 对已加载的日志切片做聚合，同样无 IO
//  3. LoadChatLogs / ComputeStats —— 唯一触碰数据库的薄层
//
// 设计约定：所有统计口径必须在此文件的注释中写明，因为它们是行为承诺，
// 而不是实现细节（前端会直接展示这些数字）。

// ---------------------------------------------------------------------------
// 分桶
// ---------------------------------------------------------------------------

// BucketAuto 表示按时间跨度自动选择分桶宽度。
const BucketAuto = "auto"

// targetBuckets auto 模式的目标桶数。
const targetBuckets = 60

// bucketStep 是分桶阶梯的一档。
type bucketStep struct {
	name string
	d    time.Duration
}

// bucketLadder 分桶阶梯，升序。**这是档位的唯一真相来源**：
// 显式档位与 auto 自动选择共用同一集合，避免出现"某档位只有 auto 能选到"
// 这种口径不一致（dashboard 原实现的阶梯比显式档位多出 2h/12h/7d）。
var bucketLadder = []bucketStep{
	{"5m", 5 * time.Minute},
	{"15m", 15 * time.Minute},
	{"30m", 30 * time.Minute},
	{"1h", time.Hour},
	{"2h", 2 * time.Hour},
	{"6h", 6 * time.Hour},
	{"12h", 12 * time.Hour},
	{"1d", 24 * time.Hour},
	{"7d", 7 * 24 * time.Hour},
}

// bucketByName 由 bucketLadder 派生，不单独维护。
var bucketByName = func() map[string]time.Duration {
	m := make(map[string]time.Duration, len(bucketLadder))
	for _, s := range bucketLadder {
		m[s.name] = s.d
	}
	return m
}()

// ResolveBucket 解析分桶宽度。
//
//   - granularity 为空或 "auto"：取阶梯中首个满足 span/step <= targetBuckets 的档位；
//     span 极大时退化为阶梯最大档位。
//   - 显式档位：查表，非法值返回错误。
func ResolveBucket(granularity string, span time.Duration) (time.Duration, error) {
	if granularity == "" || granularity == BucketAuto {
		want := span / targetBuckets
		for _, s := range bucketLadder {
			if s.d >= want {
				return s.d, nil
			}
		}
		return bucketLadder[len(bucketLadder)-1].d, nil
	}
	d, ok := bucketByName[granularity]
	if !ok {
		return 0, fmt.Errorf("invalid granularity %q", granularity)
	}
	return d, nil
}

// BucketGranularities 返回受支持的档位名（升序），供文档与前端下拉使用。
func BucketGranularities() []string {
	names := make([]string, 0, len(bucketLadder))
	for _, s := range bucketLadder {
		names = append(names, s.name)
	}
	return names
}

// ---------------------------------------------------------------------------
// 分位数
// ---------------------------------------------------------------------------

// Percentile 计算最近秩分位（nearest-rank）。
//
// 定义：idx = floor((n-1)*p)，**非插值**。这与 dashboard 原实现一致，
// 前端需在 UI 上注明，否则与直觉的"线性插值分位"结果不同会被当成 bug。
// 入参必须是已升序排序的切片；空切片返回 0。
func Percentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[n-1]
	}
	idx := int(math.Floor(float64(n-1) * p))
	return sorted[idx]
}

// SortFloats 原地升序排序，便于在聚合中统一用法（避免各处重复 sort.Float64s）。
func SortFloats(v []float64) { sort.Float64s(v) }

// ---------------------------------------------------------------------------
// 错误归类
// ---------------------------------------------------------------------------

// ErrorClass 是错误归类结果。
type ErrorClass struct {
	Type string `json:"type"` // 人类可读类别
	Code string `json:"code"` // 稳定代码，供前端分组与稳定映射，不受文案变化影响
}

// errorRawLimit 保留在归类结果中的原始错误文本长度上限。
const errorRawLimit = 500

// statusCodeRe 匹配 service/chat.go 写入的错误前缀 `status: <code>, body: ...`。
//
// 锚定行首是刻意的：上游响应体的 JSON 里出现的是 `"status":500`（引号在冒号前），
// 不会命中本模式；而正文中若恰好写了散文式的 `status: 500` 也不会误命中。
var statusCodeRe = regexp.MustCompile(`^status:\s*(\d{3})(?:\D|$)`)

// statusClass 把可精确解析的 HTTP 状态码映射到类别。
// 仅列出有独立运维含义的码；其余 4xx/5xx 走区间兜底。
var statusClass = map[int]ErrorClass{
	400: {Type: "请求无效", Code: "400"},
	401: {Type: "鉴权失败", Code: "401"},
	402: {Type: "余额不足", Code: "402"},
	403: {Type: "无权限", Code: "403"},
	404: {Type: "上游未找到", Code: "404"},
	408: {Type: "超时", Code: "timeout"},
	429: {Type: "限流", Code: "429"},
	500: {Type: "上游 5xx", Code: "5xx"},
	502: {Type: "上游 5xx", Code: "5xx"},
	503: {Type: "上游 5xx", Code: "5xx"},
	504: {Type: "上游 5xx", Code: "5xx"},
}

// textRule 是无法解析状态码时的正文特征规则。
type textRule struct {
	class    ErrorClass
	patterns []string
}

// textRules **有序**，首个命中者胜出，顺序即优先级。
//
// 顺序上"超时"必须排在"网络错误"之前：Go 的拨号超时错误形如
// `dial tcp 1.2.3.4:443: i/o timeout`，同时含两类特征，而归因为超时对运维更有用。
var textRules = []textRule{
	{class: ErrorClass{Type: "超时", Code: "timeout"}, patterns: []string{"context deadline exceeded", "deadline exceeded", "timeout", "timed out"}},
	{class: ErrorClass{Type: "上游业务错误", Code: "upstream-body"}, patterns: []string{"matched provider error sample"}},
	{class: ErrorClass{Type: "重试耗尽", Code: "exhausted"}, patterns: []string{"all retry failed"}},
	{class: ErrorClass{Type: "网络错误", Code: "network"}, patterns: []string{
		"dial tcp", "connection refused", "connection reset", "no such host",
		"broken pipe", "unexpected eof", "eof",
	}},
}

var (
	classOther = ErrorClass{Type: "其他错误", Code: "other"}
)

// ParseStatusCode 从错误文本中提取可解析的 HTTP 状态码。
// 提取不到时返回 0,false。
//
// 状态码按位累加而非 strconv：正则已保证恰好 3 位数字，因此不存在解析失败的分支，
// 少一条不可能被测试覆盖的错误路径。
func ParseStatusCode(raw string) (int, bool) {
	m := statusCodeRe.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return 0, false
	}
	d := m[1]
	return int(d[0]-'0')*100 + int(d[1]-'0')*10 + int(d[2]-'0'), true
}

// ClassifyError 归类一条错误文本，并返回截断后的样本。
//
// 优先级：可精确解析的 HTTP 状态码 > 正文特征规则 > 其他。
// 先解状态码是因为 `status: <code>` 是结构化且确定的；在自由文本里子串匹配
// 数字（如 dashboard 原实现匹配 "402"）会误命中 trace ID、token 数等无关数字。
func ClassifyError(raw string) (ErrorClass, string) {
	sample := Truncate(raw, errorRawLimit)

	if code, ok := ParseStatusCode(raw); ok {
		if c, hit := statusClass[code]; hit {
			return c, sample
		}
		if code >= 500 {
			return ErrorClass{"上游 5xx", "5xx"}, sample
		}
		if code >= 400 {
			return ErrorClass{"上游 4xx", "4xx"}, sample
		}
	}

	lower := strings.ToLower(raw)
	for _, rule := range textRules {
		for _, p := range rule.patterns {
			if strings.Contains(lower, p) {
				return rule.class, sample
			}
		}
	}
	return classOther, sample
}

// Truncate 按 rune 截断字符串并追加省略号；max <= 0 时返回空串。
// 按 rune 而非字节截断，避免把多字节字符切成无效 UTF-8。
func Truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// ---------------------------------------------------------------------------
// 计费
// ---------------------------------------------------------------------------

// tokensPerMillion 价格按每百万 token 计。
const tokensPerMillion = 1e6

// Cost 计算单条日志的费用（单位由 Currency 决定）。
//
// 口径：非缓存输入 × input_price + 缓存读 × cache_read_price + 输出 × output_price，
// 均按每百万 token 计价。三档单价是**请求时的快照**，已随行落库，无需 join 关联表。
//
// 注意：dashboard 原实现把 Σ(input_price) + Σ(output_price) 当作"成本"，
// 累加的是单价而非费用，量纲错误，此处不沿用。
func Cost(l models.ChatLog) float64 {
	nonCached := l.PromptTokens - l.PromptTokensDetails.CachedTokens
	if nonCached < 0 {
		nonCached = 0
	}
	return (float64(nonCached)*l.InputPrice +
		float64(l.PromptTokensDetails.CachedTokens)*l.CacheReadPrice +
		float64(l.CompletionTokens)*l.OutputPrice) / tokensPerMillion
}

// ---------------------------------------------------------------------------
// 筛选
// ---------------------------------------------------------------------------

// StatsFilter 是聚合的筛选条件。多值字段为空表示不筛选。
//
// 匹配口径沿用 dashboard 既有行为（避免静默的行为回归）：
// 状态、AuthKey 为**精确**匹配；供应商、模型、请求名为**子串**匹配；
// UserAgent 为子串匹配。子串语义在名字互为前缀时才会与精确匹配产生差异，
// 而前端下拉提供的都是完整名字，因此实战中两者等价。
type StatsFilter struct {
	From        time.Time
	To          time.Time
	Providers   []string
	Models      []string
	Names       []string
	Statuses    []string
	KeyIDs      []uint
	UserAgents  []string
	Granularity string
}

// match 判断单条日志是否落在筛选窗口内。窗口为左闭右开 [From, To)。
func (f StatsFilter) match(l models.ChatLog) bool {
	ts := l.CreatedAt
	if !f.From.IsZero() && ts.Before(f.From) {
		return false
	}
	if !f.To.IsZero() && !ts.Before(f.To) {
		return false
	}
	if len(f.Statuses) > 0 && !containsExact(f.Statuses, l.Status) {
		return false
	}
	if len(f.Providers) > 0 && !containsSubstr(f.Providers, l.ProviderName) {
		return false
	}
	if len(f.Models) > 0 && !containsSubstr(f.Models, l.ProviderModel) {
		return false
	}
	if len(f.Names) > 0 && !containsSubstr(f.Names, l.Name) {
		return false
	}
	if len(f.UserAgents) > 0 && !containsSubstr(f.UserAgents, l.UserAgent) {
		return false
	}
	if len(f.KeyIDs) > 0 {
		found := false
		for _, id := range f.KeyIDs {
			if id == l.AuthKeyID {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func containsExact(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

func containsSubstr(hay []string, needle string) bool {
	for _, s := range hay {
		if strings.Contains(needle, s) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 结果结构
// ---------------------------------------------------------------------------

// KPI 是概览指标。
type KPI struct {
	Total            int64   `json:"total"`
	Success          int64   `json:"success"`
	Failed           int64   `json:"failed"`
	Running          int64   `json:"running"`
	Finished         int64   `json:"finished"` // success + failed
	SuccessRate      float64 `json:"successRate"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	TotalTokens      int64   `json:"totalTokens"`
	CachedTokens     int64   `json:"cachedTokens"`
	CacheHitRate     float64 `json:"cacheHitRate"`
	Cost             float64 `json:"cost"`
	Currency         string  `json:"currency"`
	TotalRetries     int64   `json:"totalRetries"`
	RetryRate        float64 `json:"retryRate"`
}

// TrendPoint 是时间序列的一个桶。
type TrendPoint struct {
	Ts              int64   `json:"ts"` // 桶起点，Unix 毫秒
	Total           int64   `json:"total"`
	Success         int64   `json:"success"`
	Error           int64   `json:"error"`
	Running         int64   `json:"running"`
	Tokens          int64   `json:"tokens"`
	Prompt          int64   `json:"prompt"`
	Completion      int64   `json:"completion"`
	AvgTps          float64 `json:"avgTps"`
	AvgFirstChunkMs float64 `json:"avgFirstChunkMs"`
}

// GroupStat 是单个分组的统计。
type GroupStat struct {
	Name             string  `json:"name"`
	Total            int64   `json:"total"`
	Success          int64   `json:"success"`
	Error            int64   `json:"error"`
	Running          int64   `json:"running"`
	SuccessRate      float64 `json:"successRate"`
	Prompt           int64   `json:"prompt"`
	Completion       int64   `json:"completion"`
	TotalTokens      int64   `json:"totalTokens"`
	Cached           int64   `json:"cached"`
	CacheHitRate     float64 `json:"cacheHitRate"`
	AvgTps           float64 `json:"avgTps"`
	MaxTps           float64 `json:"maxTps"`
	AvgFirstChunkMs  float64 `json:"avgFirstChunkMs"`
	P95FirstChunkMs  float64 `json:"p95FirstChunkMs"`
	AvgProxyMs       float64 `json:"avgProxyMs"`
	Retries          int64   `json:"retries"`
	Cost             float64 `json:"cost"`
}

// ErrorSample 是一条可点击的错误样本。
type ErrorSample struct {
	ID        uint   `json:"id"`
	CreatedAt int64  `json:"createdAt"`
	Error     string `json:"error"`
}

// ErrorGroup 是按类别聚拢的错误。
type ErrorGroup struct {
	Type      string        `json:"type"`
	Code      string        `json:"code"`
	Count     int64         `json:"count"`
	Providers []CountItem   `json:"providers"`
	Models    []CountItem   `json:"models"`
	Samples   []ErrorSample `json:"samples"`
}

// CountItem 是"名称 + 计数"对。
type CountItem struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

// LatencyStat 是一组延迟分布。
type LatencyStat struct {
	P50  float64   `json:"p50"`
	P90  float64   `json:"p90"`
	P95  float64   `json:"p95"`
	P99  float64   `json:"p99"`
	Avg  float64   `json:"avg"`
	Max  float64   `json:"max"`
	List []float64 `json:"list"`
}

// LogRow 是排行榜里的一行。
type LogRow struct {
	ID              uint    `json:"id"`
	CreatedAt       int64   `json:"createdAt"`
	Model           string  `json:"model"`
	Provider        string  `json:"provider"`
	KeyName         string  `json:"keyName"`
	Tps             float64 `json:"tps"`
	FirstChunkMs    float64 `json:"firstChunkMs"`
	CompletionToken int64   `json:"completionTokens"`
	PromptToken     int64   `json:"promptTokens"`
	Error           string  `json:"error,omitempty"`
	Retry           int     `json:"retry"`
}

// StatsResult 是聚合输出的完整结果。
type StatsResult struct {
	GeneratedAt int64                  `json:"generatedAt"`
	BucketMs    int64                  `json:"bucketMs"`
	Truncated   bool                   `json:"truncated"`
	Range       RangeInfo              `json:"range"`
	KPI         KPI                    `json:"kpi"`
	Trend       []TrendPoint           `json:"trend"`
	ByModel     []GroupStat            `json:"byModel"`
	ByProvider  []GroupStat            `json:"byProvider"`
	ByKey       []GroupStat            `json:"byKey"`
	ByName      []GroupStat            `json:"byName"`
	ByUserAgent []GroupStat            `json:"byUa"`
	Errors      []ErrorGroup           `json:"errors"`
	ErrorTrend  []TrendPoint           `json:"errorTrend"`
	Latency     LatencyBreakdown       `json:"latency"`
	TopTps      []LogRow               `json:"topTps"`
	Slowest     []LogRow               `json:"slowest"`
	RecentError []LogRow               `json:"recentErrors"`
}

// RangeInfo 是本次聚合实际覆盖的时间范围。
type RangeInfo struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

// unixMilliOrZero 把零值时间表示为 0 而非 time.Time{}.UnixMilli() 的巨负值，
// 避免未指定范围时前端拿到无意义的数字。
func unixMilliOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// LatencyBreakdown 汇总各类延迟。
type LatencyBreakdown struct {
	FirstChunk LatencyStat `json:"firstChunk"`
	Tps        LatencyStat `json:"tps"`
	Proxy      LatencyStat `json:"proxyMs"`
}

// 排行榜与错误样本的容量上限。
const (
	leaderboardLimit = 10
	maxErrorSamples  = 3
	topGroupLimit    = 5 // 每个错误类别的"受影响供应商/模型"条数上限，与 dashboard 一致
)

// ---------------------------------------------------------------------------
// 聚合
// ---------------------------------------------------------------------------

// Aggregate 对一批日志做完整聚合。纯函数，无 IO。
//
// 关键口径：
//   - 成功率分母 = success + failed，**排除 running**（在途请求不应拉低成功率）
//   - 缓存命中率 = cached / prompt（prompt 为 0 时为 0）
//   - 分组内延迟分位用最近秩法（Percentile）
//   - 每个分组与时间序列的时间字段统一用**毫秒**，纳秒按 1e6 换算
func Aggregate(logs []models.ChatLog, f StatsFilter) StatsResult {
	bucket, _ := ResolveBucket(f.Granularity, f.To.Sub(f.From))
	return AggregateWithBucket(logs, f, bucket)
}

// AggregateWithBucket 与 Aggregate 相同，但显式指定分桶宽度，
// 便于测试与复用（调用方已解析过档位时避免重复解析）。
func AggregateWithBucket(logs []models.ChatLog, f StatsFilter, bucket time.Duration) StatsResult {
	res := StatsResult{
		GeneratedAt: time.Now().UnixMilli(),
		BucketMs:    bucket.Milliseconds(),
		Range:       RangeInfo{From: unixMilliOrZero(f.From), To: unixMilliOrZero(f.To)},
		Trend:       []TrendPoint{},
		ByModel:     []GroupStat{},
		ByProvider:  []GroupStat{},
		ByKey:       []GroupStat{},
		ByName:      []GroupStat{},
		ByUserAgent: []GroupStat{},
		Errors:      []ErrorGroup{},
		ErrorTrend:  []TrendPoint{},
		TopTps:      []LogRow{},
		Slowest:     []LogRow{},
		RecentError: []LogRow{},
	}

	// 只需一帧快照，避免多处重复计算 now。
	var (
		firstChunkSecs  []float64
		tpsVals         []float64
		proxyMsVals     []float64
		buckets         = map[int64]*TrendPoint{}
		bucketOrder     []int64
		models          = newGroupAcc()
		providers       = newGroupAcc()
		keys            = newGroupAcc()
		names           = newGroupAcc()
		uas             = newGroupAcc()
		errGroups       = map[string]*ErrorGroup{}
		errOrder        []string
		errBuckets      = map[int64]int64{}
		errBucketOrder  []int64
		leaderTps       []LogRow
		leaderSlow      []LogRow
		leaderRecentErr []LogRow
	)

	for _, l := range logs {
		if !f.match(l) {
			continue
		}

		// ---- KPI ----
		res.KPI.Total++
		switch l.Status {
		case consts.StatusSuccess:
			res.KPI.Success++
		case consts.StatusError:
			res.KPI.Failed++
		case consts.StatusRunning:
			res.KPI.Running++
		}
		res.KPI.PromptTokens += l.PromptTokens
		res.KPI.CompletionTokens += l.CompletionTokens
		res.KPI.TotalTokens += l.TotalTokens
		res.KPI.CachedTokens += l.PromptTokensDetails.CachedTokens
		res.KPI.TotalRetries += int64(l.Retry)
		res.KPI.Cost += Cost(l)
		// 币种取首条非空值：单价是快照，同一批数据通常同一币种。
		if res.KPI.Currency == "" && l.Currency != "" {
			res.KPI.Currency = l.Currency
		}

		// ---- 延迟样本 ----
		fcMs := durationToMs(l.FirstChunkTime)
		pxMs := durationToMs(l.ProxyTime)
		if l.Status == consts.StatusSuccess {
			// 只在成功请求上统计延迟与 TPS，失败/在途的耗时没有比较意义
			firstChunkSecs = append(firstChunkSecs, fcMs/1000)
			tpsVals = append(tpsVals, l.Tps)
			proxyMsVals = append(proxyMsVals, pxMs)
		}

		// ---- 时间序列 ----
		tsKey := bucketStart(l.CreatedAt, bucket).UnixMilli()
		p, ok := buckets[tsKey]
		if !ok {
			p = &TrendPoint{Ts: tsKey}
			buckets[tsKey] = p
			bucketOrder = append(bucketOrder, tsKey)
		}
		p.Total++
		switch l.Status {
		case consts.StatusSuccess:
			p.Success++
		case consts.StatusError:
			p.Error++
		case consts.StatusRunning:
			p.Running++
		}
		p.Tokens += l.TotalTokens
		p.Prompt += l.PromptTokens
		p.Completion += l.CompletionTokens
		p.AvgTps += l.Tps
		p.AvgFirstChunkMs += fcMs

		// ---- 分组 ----
		models.add(l.Name, l)
		providers.add(l.ProviderName, l)
		keys.add(keyLabel(l.AuthKeyID), l)
		names.add(l.Name, l)
		uas.add(l.UserAgent, l)

		// ---- 错误 ----
		if l.Status == consts.StatusError {
			class, sample := ClassifyError(l.Error)
			g, ok := errGroups[class.Code]
			if !ok {
				g = &ErrorGroup{Type: class.Type, Code: class.Code}
				errGroups[class.Code] = g
				errOrder = append(errOrder, class.Code)
			}
			g.Count++
			g.Providers = bump(g.Providers, l.ProviderName)
			g.Models = bump(g.Models, l.Name)
			if len(g.Samples) < maxErrorSamples {
				g.Samples = append(g.Samples, ErrorSample{
					ID:        l.ID,
					CreatedAt: l.CreatedAt.UnixMilli(),
					Error:     sample,
				})
			}
			eb := bucketStart(l.CreatedAt, bucket).UnixMilli()
			if _, ok := errBuckets[eb]; !ok {
				errBuckets[eb] = 0
				errBucketOrder = append(errBucketOrder, eb)
			}
			errBuckets[eb]++
		}

		// ---- 排行榜 ----
		row := LogRow{
			ID:              l.ID,
			CreatedAt:       l.CreatedAt.UnixMilli(),
			Model:           l.Name,
			Provider:        l.ProviderName,
			KeyName:         keyLabel(l.AuthKeyID),
			Tps:             l.Tps,
			FirstChunkMs:    fcMs,
			CompletionToken: l.CompletionTokens,
			PromptToken:     l.PromptTokens,
			Error:           l.Error,
			Retry:           l.Retry,
		}
		if l.Status == consts.StatusSuccess {
			leaderTps = append(leaderTps, row)
			leaderSlow = append(leaderSlow, row)
		}
		if l.Status == consts.StatusError {
			leaderRecentErr = append(leaderRecentErr, row)
		}
	}

	// ---- 派生率值 ----
	res.KPI.Finished = res.KPI.Success + res.KPI.Failed
	res.KPI.SuccessRate = ratio(res.KPI.Success, res.KPI.Finished) * 100
	res.KPI.CacheHitRate = ratio(res.KPI.CachedTokens, res.KPI.PromptTokens) * 100
	res.KPI.RetryRate = ratio(res.KPI.TotalRetries, res.KPI.Total) * 100

	// ---- 时间序列收尾：均值与排序 ----
	for _, ts := range bucketOrder {
		p := buckets[ts]
		if p.Total > 0 {
			p.AvgTps = p.AvgTps / float64(p.Total)
			p.AvgFirstChunkMs = p.AvgFirstChunkMs / float64(p.Total)
		}
		res.Trend = append(res.Trend, *p)
	}
	sort.Slice(res.Trend, func(i, j int) bool { return res.Trend[i].Ts < res.Trend[j].Ts })
	res.Trend = normalizeTrendSeries(res.Trend, bucket)

	// ---- 错误时间序列 ----
	sort.Slice(errBucketOrder, func(i, j int) bool { return errBucketOrder[i] < errBucketOrder[j] })
	for _, ts := range errBucketOrder {
		res.ErrorTrend = append(res.ErrorTrend, TrendPoint{Ts: ts, Error: errBuckets[ts]})
	}
	res.ErrorTrend = normalizeTrendSeries(res.ErrorTrend, bucket)

	// ---- 错误类别：按数量降序，同数量按 code 稳定排序 ----
	res.Errors = make([]ErrorGroup, 0, len(errOrder))
	for _, code := range errOrder {
		g := errGroups[code]
		g.Providers = topN(g.Providers, topGroupLimit)
		g.Models = topN(g.Models, topGroupLimit)
		res.Errors = append(res.Errors, *g)
	}
	sort.SliceStable(res.Errors, func(i, j int) bool {
		if res.Errors[i].Count != res.Errors[j].Count {
			return res.Errors[i].Count > res.Errors[j].Count
		}
		return res.Errors[i].Code < res.Errors[j].Code
	})

	// ---- 分组收尾 ----
	res.ByModel = models.result()
	res.ByProvider = providers.result()
	res.ByKey = keys.result()
	res.ByName = names.result()
	res.ByUserAgent = uas.result()

	// ---- 延迟分布 ----
	SortFloats(firstChunkSecs)
	SortFloats(tpsVals)
	SortFloats(proxyMsVals)
	res.Latency = LatencyBreakdown{
		FirstChunk: LatencyStat{
			P50: Percentile(firstChunkSecs, 0.50), P90: Percentile(firstChunkSecs, 0.90),
			P95: Percentile(firstChunkSecs, 0.95), P99: Percentile(firstChunkSecs, 0.99),
			Avg: mean(firstChunkSecs), Max: maxOf(firstChunkSecs), List: nonNilFloats(firstChunkSecs),
		},
		Tps: LatencyStat{
			P50: Percentile(tpsVals, 0.50), P90: Percentile(tpsVals, 0.90),
			P95: Percentile(tpsVals, 0.95), P99: Percentile(tpsVals, 0.99),
			Avg: mean(tpsVals), Max: maxOf(tpsVals), List: nonNilFloats(tpsVals),
		},
		Proxy: LatencyStat{
			P50: Percentile(proxyMsVals, 0.50), P90: Percentile(proxyMsVals, 0.90),
			P95: Percentile(proxyMsVals, 0.95), P99: Percentile(proxyMsVals, 0.99),
			Avg: mean(proxyMsVals), Max: maxOf(proxyMsVals), List: nonNilFloats(proxyMsVals),
		},
	}

	// ---- 排行榜：TPS 降序 / 首包耗时降序 / 时间倒序 ----
	sort.SliceStable(leaderTps, func(i, j int) bool { return leaderTps[i].Tps > leaderTps[j].Tps })
	res.TopTps = head(leaderTps, leaderboardLimit)
	sort.SliceStable(leaderSlow, func(i, j int) bool {
		return leaderSlow[i].FirstChunkMs > leaderSlow[j].FirstChunkMs
	})
	res.Slowest = head(leaderSlow, leaderboardLimit)
	sort.SliceStable(leaderRecentErr, func(i, j int) bool {
		return leaderRecentErr[i].CreatedAt > leaderRecentErr[j].CreatedAt
	})
	res.RecentError = head(leaderRecentErr, leaderboardLimit)

	return res
}

// ---------------------------------------------------------------------------
// 聚合内部工具
// ---------------------------------------------------------------------------

// durationToMs 把 time.Duration（纳秒）换算为毫秒。
//
// dashboard 原实现用 `>1e6 视为纳秒否则视为毫秒` 的启发式猜测，
// 这里不沿用：llmio 侧 ProxyTime/FirstChunkTime/ChunkTime 均为 time.Duration，
// 单位恒为纳秒，猜测分支只会引入不确定性。
func durationToMs(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

// bucketStart 返回 ts 所在桶的起点。
func bucketStart(ts time.Time, bucket time.Duration) time.Time {
	if bucket <= 0 {
		return ts
	}
	return ts.Truncate(bucket)
}

// normalizeTrendSeries 保证时间序列在两个端点之间连续补齐空桶。
//
// 补齐是刻意的：折线图上跳过空桶会让人误读为"那段时间没有请求"与
// "那段时间数据缺失"是同一件事；补齐为 0 后语义统一为"没有请求"。
func normalizeTrendSeries(points []TrendPoint, bucket time.Duration) []TrendPoint {
	if len(points) == 0 || bucket <= 0 {
		return points
	}
	byTs := make(map[int64]TrendPoint, len(points))
	for _, p := range points {
		byTs[p.Ts] = p
	}
	stepMs := bucket.Milliseconds()
	start, end := points[0].Ts, points[len(points)-1].Ts

	out := make([]TrendPoint, 0, (end-start)/stepMs+1)
	for ts := start; ts <= end; ts += stepMs {
		if p, ok := byTs[ts]; ok {
			out = append(out, p)
			continue
		}
		out = append(out, TrendPoint{Ts: ts})
	}
	return out
}

// keyLabel 把 AuthKeyID 转成展示名。0 代表管理台 TOKEN 直连，其余由调用方补名。
func keyLabel(id uint) string {
	if id == 0 {
		return "admin"
	}
	return strconv.FormatUint(uint64(id), 10)
}

// groupAcc 是分组的累加器。
type groupAcc struct {
	items map[string]*groupItem
}

type groupItem struct {
	stat        GroupStat
	tpsSum      float64
	firstChunk  []float64
	proxySum    float64
	firstSum    float64
	successSeen int64
}

func newGroupAcc() *groupAcc { return &groupAcc{items: map[string]*groupItem{}} }

func (a *groupAcc) add(name string, l models.ChatLog) {
	if name == "" {
		name = "-"
	}
	it, ok := a.items[name]
	if !ok {
		it = &groupItem{stat: GroupStat{Name: name}}
		a.items[name] = it
	}
	s := &it.stat
	s.Total++
	switch l.Status {
	case consts.StatusSuccess:
		s.Success++
	case consts.StatusError:
		s.Error++
	case consts.StatusRunning:
		s.Running++
	}
	s.Prompt += l.PromptTokens
	s.Completion += l.CompletionTokens
	s.TotalTokens += l.TotalTokens
	s.Cached += l.PromptTokensDetails.CachedTokens
	s.Retries += int64(l.Retry)
	s.Cost += Cost(l)
	if l.Tps > s.MaxTps {
		s.MaxTps = l.Tps
	}
	if l.Status == consts.StatusSuccess {
		it.successSeen++
		it.tpsSum += l.Tps
		fc := durationToMs(l.FirstChunkTime)
		it.firstSum += fc
		it.firstChunk = append(it.firstChunk, fc)
		it.proxySum += durationToMs(l.ProxyTime)
	}
}

// result 收尾并排序（按总量降序，同量按名称稳定排序）。
func (a *groupAcc) result() []GroupStat {
	out := make([]GroupStat, 0, len(a.items))
	for _, it := range a.items {
		s := it.stat
		s.SuccessRate = ratio(s.Success, s.Success+s.Error) * 100
		s.CacheHitRate = ratio(s.Cached, s.Prompt) * 100
		if it.successSeen > 0 {
			s.AvgTps = it.tpsSum / float64(it.successSeen)
			s.AvgFirstChunkMs = it.firstSum / float64(it.successSeen)
			s.AvgProxyMs = it.proxySum / float64(it.successSeen)
		}
		SortFloats(it.firstChunk)
		s.P95FirstChunkMs = Percentile(it.firstChunk, 0.95)
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// bump 在 CountItem 列表中加一，保持升序插入以便后续截断。
func bump(items []CountItem, name string) []CountItem {
	if name == "" {
		name = "-"
	}
	for i := range items {
		if items[i].Name == name {
			items[i].Count++
			return items
		}
	}
	return append(items, CountItem{Name: name, Count: 1})
}

// topN 取计数最高的 n 项（同数量按名称稳定排序）。
func topN(items []CountItem, n int) []CountItem {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return items[i].Name < items[j].Name
	})
	return head(items, n)
}

// head 取前 n 项。nil 入参返回非 nil 空切片。
//
// "非 nil" 是刻意的：这些切片会直接序列化给前端，nil 会变成 JSON `null`，
// 迫使前端对每个集合都做空值判断；统一为 `[]` 可以让前端只判断长度。
func head[T any](s []T, n int) []T {
	if len(s) <= n {
		if s == nil {
			return []T{}
		}
		return s
	}
	out := make([]T, n)
	copy(out, s[:n])
	return out
}

// nonNilFloats 同 head，保证浮点切片序列化为 `[]` 而非 `null`。
func nonNilFloats(v []float64) []float64 {
	if v == nil {
		return []float64{}
	}
	return v
}

// ratio 返回 a/b；b <= 0 时返回 0（而非 NaN/Inf，避免前端拿到不可序列化的值）。
func ratio(a, b int64) float64 {
	if b <= 0 {
		return 0
	}
	return float64(a) / float64(b)
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	var sum float64
	for _, x := range v {
		sum += x
	}
	return sum / float64(len(v))
}

func maxOf(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	return v[len(v)-1] // 调用前已升序排序
}

// ---------------------------------------------------------------------------
// 数据库接入
// ---------------------------------------------------------------------------

// StatsScanLimit 单次聚合最多加载的日志条数。
//
// 设上限是必要的：聚合在内存中完成，无上限时一个长期运行的实例可能把
// 整张 chat_logs 读进内存。超出部分会被截断（取最新的 N 条），
// 因此极长时间窗的统计可能不完整——前端应在命中上限时提示收窄时间范围。
//
// 声明为变量而非常量，以便测试把上限调低来覆盖截断路径。
var StatsScanLimit = 200_000

// LoadChatLogs 按筛选条件加载日志。这是本文件唯一触碰数据库的函数。
func LoadChatLogs(ctx context.Context, f StatsFilter) ([]models.ChatLog, bool, error) {
	var (
		logs      []models.ChatLog
		truncated bool
	)
	q := models.DB.WithContext(ctx).Model(&models.ChatLog{})

	if !f.From.IsZero() {
		q = q.Where("created_at >= ?", f.From)
	}
	if !f.To.IsZero() {
		q = q.Where("created_at < ?", f.To)
	}
	if len(f.Statuses) > 0 {
		q = q.Where("status IN ?", f.Statuses)
	}
	if len(f.KeyIDs) > 0 {
		q = q.Where("auth_key_id IN ?", f.KeyIDs)
	}
	// 子串匹配与 StatsFilter.match 的口径保持一致，避免"先宽后窄"两套语义。
	for _, cond := range []struct {
		col  string
		vals []string
	}{
		{"provider_name", f.Providers},
		{"provider_model", f.Models},
		{"name", f.Names},
		{"user_agent", f.UserAgents},
	} {
		if len(cond.vals) == 0 {
			continue
		}
		parts := make([]string, 0, len(cond.vals))
		args := make([]any, 0, len(cond.vals))
		for _, v := range cond.vals {
			parts = append(parts, cond.col+" LIKE ?")
			args = append(args, "%"+v+"%")
		}
		q = q.Where("("+strings.Join(parts, " OR ")+")", args...)
	}

	if err := q.Order("created_at DESC").Limit(StatsScanLimit + 1).Find(&logs).Error; err != nil {
		return nil, false, err
	}
	if len(logs) > StatsScanLimit {
		truncated = true
		logs = logs[:StatsScanLimit]
	}
	return logs, truncated, nil
}

// ComputeStats 加载并聚合。供 handler 直接调用。
func ComputeStats(ctx context.Context, f StatsFilter) (*StatsResult, bool, error) {
	logs, truncated, err := LoadChatLogs(ctx, f)
	if err != nil {
		return nil, false, err
	}
	res := Aggregate(logs, f)
	res.Truncated = truncated
	return &res, truncated, nil
}
