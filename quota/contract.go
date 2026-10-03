// Package quota 实现配额/余量数据的采集、归一与缓存。
//
// 本文件是**归一契约**：任何数据源（内置适配器、HTTP 配置、脚本）产出的原始值，
// 都要经过这里变成同一种形状，前端因此只需理解一种结构。
//
// 契约刻意做得宽松：各家供应商的余量接口字段命名毫无共识（used/usedAmount/usage、
// total/totalAmount/limit/quota、remaining/remain/left/balance），且中文供应商
// 直接用「已用/总量/剩余」。与其为每家写一个适配器，不如把宽容度集中在归一这一层。
package quota

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Unit 是归一后的单位。
type Unit string

const (
	UnitPercent Unit = "%"
	UnitCNY     Unit = "CNY"
	UnitUSD     Unit = "USD"
	UnitTokens  Unit = "tokens"
	UnitCredits Unit = "CREDITS"
	UnitCount   Unit = "次"
	UnitUnknown Unit = ""
)

// UnitKind 决定默认的数值格式化方式。
type UnitKind string

const (
	KindPercent UnitKind = "percent"
	KindMoney   UnitKind = "money"
	KindAmount  UnitKind = "amount" // tokens / credits / 次
	KindUnknown UnitKind = ""
)

// Window 是归一后的时间窗口。
type Window string

const (
	Window5h    Window = "5h"
	WindowDay   Window = "day"
	WindowWeek  Window = "week"
	WindowMonth Window = "month"
	WindowTotal Window = "total"
	WindowNone  Window = ""
)

// Status 是条目状态。
type Status string

const (
	StatusOK        Status = "ok"
	StatusWarning   Status = "warning"
	StatusExhausted Status = "exhausted"
	StatusUnknown   Status = "unknown"
)

// statusRank 用于取整源的最差状态。
var statusRank = map[Status]int{
	StatusOK:        0,
	StatusUnknown:   1,
	StatusWarning:   2,
	StatusExhausted: 3,
}

// Item 是归一后的一个余量条目。
//
// Used / Total / Remaining / Percent 用指针：nil 表示"上游没给这个数"，
// 与"给了 0"是两回事。用 0 代替 nil 会让"未提供"显示成"已用光"。
type Item struct {
	ID        string         `json:"id"`
	Label     string         `json:"label"`
	Used      *float64       `json:"used"`
	Total     *float64       `json:"total"`
	Remaining *float64       `json:"remaining"`
	Percent   *float64       `json:"percent"`
	Unit      Unit           `json:"unit"`
	Window    Window         `json:"window"`
	ResetAt   string         `json:"resetAt,omitempty"`
	ExpireAt  string         `json:"expireAt,omitempty"`
	Status    Status         `json:"status"`
	Format    string         `json:"format,omitempty"`
	Extra     map[string]any `json:"extra,omitempty"`
	// Text 是服务端按 Format 渲染好的展示文本。
	//
	// 之所以由服务端算好下发：格式化引擎只有一份实现（就在这里），
	// 前端不再需要一份镜像。原 dashboard 里有前后端各一份、
	// 靠注释要求手工同步——那必然漂移。
	Text string `json:"text"`
}

// ---------------------------------------------------------------------------
// 单位
// ---------------------------------------------------------------------------

// unitAliases 把各家写法映射到归一单位。键统一小写去空格后比较。
var unitAliases = map[string]Unit{
	"%": UnitPercent, "percent": UnitPercent, "percentage": UnitPercent, "百分比": UnitPercent,
	"cny": UnitCNY, "rmb": UnitCNY, "元": UnitCNY, "人民币": UnitCNY, "¥": UnitCNY,
	"usd": UnitUSD, "$": UnitUSD, "美元": UnitUSD,
	"token": UnitTokens, "tokens": UnitTokens, "令牌": UnitTokens,
	"credit": UnitCredits, "credits": UnitCredits, "算力": UnitCredits,
	"time": UnitCount, "count": UnitCount, "次": UnitCount, "times": UnitCount,
}

// NormalizeUnit 归一单位。无法识别时返回 UnitUnknown 并原样保留原文——
// 丢掉原文会让"未知单位"变得无法排查。
func NormalizeUnit(raw string) Unit {
	key := strings.ToLower(strings.TrimSpace(raw))
	if u, ok := unitAliases[key]; ok {
		return u
	}
	return UnitUnknown
}

// UnitKindOf 把单位归类，决定默认格式化。
func UnitKindOf(u Unit) UnitKind {
	switch u {
	case UnitPercent:
		return KindPercent
	case UnitCNY, UnitUSD:
		return KindMoney
	case UnitTokens, UnitCredits, UnitCount:
		return KindAmount
	default:
		return KindUnknown
	}
}

// ---------------------------------------------------------------------------
// 窗口
// ---------------------------------------------------------------------------

var windowAliases = map[string]Window{
	"5h": Window5h, "5小时": Window5h, "五小时": Window5h,
	"day": WindowDay, "daily": WindowDay, "日": WindowDay, "天": WindowDay, "每日": WindowDay,
	"week": WindowWeek, "weekly": WindowWeek, "周": WindowWeek, "每周": WindowWeek,
	"month": WindowMonth, "monthly": WindowMonth, "月": WindowMonth, "每月": WindowMonth,
	"total": WindowTotal, "总额": WindowTotal, "余额": WindowTotal, "all": WindowTotal, "总计": WindowTotal,
}

// WindowLabel 把窗口转成中文展示名。界面用中文，因此不返回内部标识。
func WindowLabel(w Window) string {
	switch w {
	case Window5h:
		return "5 小时"
	case WindowDay:
		return "每日"
	case WindowWeek:
		return "每周"
	case WindowMonth:
		return "每月"
	case WindowTotal:
		return "总额"
	default:
		return ""
	}
}

// NormalizeWindow 归一窗口。无法识别返回 WindowNone。
func NormalizeWindow(raw string) Window {
	key := strings.ToLower(strings.TrimSpace(raw))
	if w, ok := windowAliases[key]; ok {
		return w
	}
	return WindowNone
}

// ---------------------------------------------------------------------------
// 时间
// ---------------------------------------------------------------------------

// CoerceTime 把各种时间表示归一成 ISO8601。
//
// 数值按位数判断秒或毫秒（< 1e11 视为秒）——这是唯一可靠的判据：
// 1e11 秒是公元 5138 年，1e11 毫秒是 1973 年，两者的取值范围不重叠。
// 无法解析时返回空串（而不是原样返回），因为调用方会把它当作时间使用。
func CoerceTime(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return ""
		}
		// 纯数字字符串按时间戳处理
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return fromEpoch(n)
		}
		// 先试标准布局，再试几种常见的宽松写法
		if parsed, ok := parseLoose(s); ok {
			return parsed.UTC().Format(time.RFC3339)
		}
		return ""
	case float64:
		return fromEpoch(int64(t))
	case int64:
		return fromEpoch(t)
	default:
		return ""
	}
}

func fromEpoch(n int64) string {
	if n <= 0 {
		return ""
	}
	var t time.Time
	if n < 1e11 {
		t = time.Unix(n, 0)
	} else {
		t = time.UnixMilli(n)
	}
	return t.UTC().Format(time.RFC3339)
}

// looseLayouts 覆盖上游常见的几种非标准写法。
var looseLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
	"2006/01/02 15:04:05",
	"2006/01/02",
}

func parseLoose(s string) (time.Time, bool) {
	for _, layout := range looseLayouts {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ---------------------------------------------------------------------------
// 归一
// ---------------------------------------------------------------------------

// NormalizeOptions 影响状态推导与展示。
type NormalizeOptions struct {
	// WarningAt 百分比达到多少算告警。
	WarningAt float64
	// IDPrefix 给自动生成的条目 ID 加前缀，避免不同源的条目撞 ID。
	IDPrefix string
}

// Normalize 把任意形状的原始值归一成条目列表。
//
// 接受的输入形状（原 dashboard 的宽容度必须保留，用户脚本依赖它）：
//   - {"items": [...]}
//   - 裸数组 [...]
//   - {"data": [...]}
//   - {"items": {"key": value}}  → 每个键变成一条
//   - 单个条目对象 {...}
//
// 一条都无法识别时返回错误——**不能默默返回空列表**：
// "接口返回了 200 但内容看不懂" 与 "真的没有余量了" 必须区分开，
// 否则面板会显示"一切正常"而实际是取数坏了。
func Normalize(raw any, opts NormalizeOptions) ([]Item, error) {
	rows, err := extractRows(raw)
	if err != nil {
		return nil, err
	}

	items := make([]Item, 0, len(rows))
	for i, row := range rows {
		item, ok := normItem(row, opts)
		if !ok {
			continue
		}
		if item.ID == "" {
			item.ID = opts.IDPrefix + "item-" + strconv.Itoa(i+1)
		}
		items = append(items, item)
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("数据源返回了 JSON，但没有任何可识别的余量数值")
	}
	return items, nil
}

// extractRows 把各种外层容器摊平成一列条目对象。
//
// 同时接受 []any 与 []map[string]any：JSON 反序列化得到前者，
// 而 Go 侧适配器（HTTP / 内置）构造出的行天然是后者。
// 只认 []any 会让「上游 JSON 能归一、适配器产出反而不能」这种怪事发生。
func extractRows(raw any) ([]map[string]any, error) {
	switch v := raw.(type) {
	case nil:
		return nil, fmt.Errorf("数据源没有返回任何内容")
	case []any:
		return filterRecords(v), nil
	case []map[string]any:
		return v, nil
	case map[string]any:
		// 先看有无 items / data 容器
		for _, key := range []string{"items", "data", "list", "result", "results"} {
			inner, ok := v[key]
			if !ok {
				continue
			}
			if arr, ok := inner.([]any); ok {
				if rows := filterRecords(arr); len(rows) > 0 {
					return rows, nil
				}
				continue
			}
			if rows, ok := inner.([]map[string]any); ok && len(rows) > 0 {
				return rows, nil
			}
			// {"items": {"k": v}} 形式：每个键一条，键作为 id
			if m, ok := inner.(map[string]any); ok {
				return rowsFromMap(m), nil
			}
		}
		// 不是容器，那就当成单条
		return []map[string]any{v}, nil
	default:
		return nil, fmt.Errorf("数据源返回的 JSON 既不是对象也不是数组")
	}
}

func filterRecords(arr []any) []map[string]any {
	out := make([]map[string]any, 0, len(arr))
	for _, e := range arr {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// rowsFromMap 把对象映射摊成条目。键序不稳定，因此排序后输出——
// 否则同一个数据源每次刷新条目顺序都可能不同，界面上会跳。
func rowsFromMap(m map[string]any) []map[string]any {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		switch val := m[k].(type) {
		case map[string]any:
			row := make(map[string]any, len(val)+1)
			for kk, vv := range val {
				row[kk] = vv
			}
			if _, ok := row["id"]; !ok {
				row["id"] = k
			}
			if _, ok := row["label"]; !ok {
				row["label"] = k
			}
			out = append(out, row)
		default:
			// 键 → 标量：当成一条"该键的值就是剩余量"的条目
			out = append(out, map[string]any{"id": k, "label": k, "remaining": val})
		}
	}
	return out
}

// fieldAliases 各语义字段的别名。
var fieldAliases = map[string][]string{
	"used":      {"used", "usedAmount", "used_amount", "usage", "已用", "使用量", "consumed"},
	"total":     {"total", "totalAmount", "total_amount", "limit", "quota", "总量", "额度"},
	"remaining": {"remaining", "remain", "left", "balance", "剩余", "余额"},
	"unit":      {"unit", "单位", "currency"},
	"window":    {"window", "period", "周期", "窗口"},
	"resetAt":   {"resetAt", "reset_at", "resetTime", "reset_time", "resetsAt", "reset", "重置时间"},
	"expireAt":  {"expireAt", "expire_at", "expiredAt", "expiry", "expireTime", "到期时间", "有效期"},
	"status":    {"status", "state", "状态"},
	"format":    {"format", "template", "格式", "模板"},
	"label":     {"label", "name", "title", "名称", "标题"},
	"id":        {"id", "key", "code", "标识"},
	"extra":     {"extra", "detail", "details", "额外"},
}

// lookup 按别名顺序取第一个存在的字段。
func lookup(row map[string]any, kind string) (any, bool) {
	for _, alias := range fieldAliases[kind] {
		if v, ok := row[alias]; ok && v != nil {
			return v, true
		}
	}
	return nil, false
}

// toFloat 把字段值转成浮点。非数值返回 nil。
func toFloat(v any) *float64 {
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return nil
		}
		return &n
	case int64:
		f := float64(n)
		return &f
	case int:
		f := float64(n)
		return &f
	case string:
		s := strings.TrimSpace(strings.TrimSuffix(n, "%"))
		if s == "" {
			return nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil
		}
		return &f
	default:
		return nil
	}
}

func toStr(v any) string {
	switch s := v.(type) {
	case string:
		return strings.TrimSpace(s)
	case float64:
		// 整数值不要显示成 1.000000
		if s == math.Trunc(s) && math.Abs(s) < 1e15 {
			return strconv.FormatInt(int64(s), 10)
		}
		return strconv.FormatFloat(s, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(s, 10)
	case int:
		// 与 toFloat 对称：JSON 只会给出 float64，但 Go 侧适配器
		// 构造行时可能用 int。缺这一支会让数值静默变成空串。
		return strconv.Itoa(s)
	case bool:
		if s {
			return "true"
		}
		return "false"
	}
	// 命名 string 类型（Window / Unit 这些）不匹配上面的 `case string`，
	// 原先落到 default 就静默变空串——与 `case int` 那条注释说的同一种病。
	// opencode 与 scnet 的 window 就是这样丢的：适配器明明填了
	// `"window": WindowWeek`，出口却是 `"window": ""`，卡片上既不显示
	// 「每周」也不显示 {window}。按 kind 判而不是逐个列类型，
	// 新加的命名类型才不会再踩一次。
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.String {
		return strings.TrimSpace(rv.String())
	}
	return ""
}

// normItem 归一单条。
//
// 返回 ok=false 表示这条完全没有可识别的数值——按契约应当跳过。
func normItem(row map[string]any, opts NormalizeOptions) (Item, bool) {
	item := Item{Extra: map[string]any{}}

	if v, ok := lookup(row, "id"); ok {
		item.ID = toStr(v)
	}
	if v, ok := lookup(row, "label"); ok {
		item.Label = toStr(v)
	}
	if item.Label == "" {
		item.Label = item.ID
	}

	used, hasUsed := lookupFloat(row, "used")
	total, hasTotal := lookupFloat(row, "total")
	remaining, hasRemaining := lookupFloat(row, "remaining")

	if raw, ok := lookup(row, "unit"); ok {
		item.Unit = NormalizeUnit(toStr(raw))
	}
	// 单位给了 % 但没给总量时，总量按 100 算——否则百分比无从比较
	if item.Unit == UnitPercent && !hasTotal {
		hundred := 100.0
		total, hasTotal = &hundred, true
	}

	// "三者任意两个"补全：余量接口经常只给其中两个
	used, total, remaining, ok := complete(used, hasUsed, total, hasTotal, remaining, hasRemaining)
	if !ok {
		return Item{}, false
	}
	item.Used, item.Total, item.Remaining = used, total, remaining

	// Percent 是**已用**百分比，不是剩余百分比。
	//
	// 这个方向很重要：告警阈值按"用了多少"设定（warningAt=80 意为用掉八成即告警），
	// 状态推导也按已用判断。若算成剩余百分比，100% 剩余会被判成"用尽"，
	// 而 0% 剩余会被判成"健康"——完全反过来。
	if item.Unit == UnitPercent {
		// 单位是 % 时 remaining 本身就是剩余百分比，已用即其补数
		if item.Remaining != nil {
			p := 100 - *item.Remaining
			item.Percent = &p
		}
	} else if item.Used != nil && item.Total != nil && *item.Total > 0 {
		p := (*item.Used / *item.Total) * 100
		item.Percent = &p
	}

	if raw, ok := lookup(row, "window"); ok {
		item.Window = NormalizeWindow(toStr(raw))
	}
	if raw, ok := lookup(row, "resetAt"); ok {
		item.ResetAt = CoerceTime(raw)
	}
	if raw, ok := lookup(row, "expireAt"); ok {
		item.ExpireAt = CoerceTime(raw)
	}
	if raw, ok := lookup(row, "format"); ok {
		item.Format = toStr(raw)
	}
	if raw, ok := lookup(row, "extra"); ok {
		if m, ok := raw.(map[string]any); ok {
			item.Extra = m
		}
	}

	item.Status = deriveStatus(row, item, opts.WarningAt)
	item.Text = RenderItem(item)
	return item, true
}

func lookupFloat(row map[string]any, kind string) (*float64, bool) {
	v, ok := lookup(row, kind)
	if !ok {
		return nil, false
	}
	f := toFloat(v)
	if f == nil {
		return nil, false
	}
	return f, true
}

// complete 做"三者任意两个"补全。
//
// 返回 ok=false 表示三者一个都没有——这条应当被跳过。
func complete(
	used *float64, hasUsed bool,
	total *float64, hasTotal bool,
	remaining *float64, hasRemaining bool,
) (*float64, *float64, *float64, bool) {
	// 借本地变量避免直接改指针指向的值
	var u, t, r *float64
	if hasUsed {
		u = used
	}
	if hasTotal {
		t = total
	}
	if hasRemaining {
		r = remaining
	}

	count := 0
	for _, p := range []*float64{u, t, r} {
		if p != nil {
			count++
		}
	}
	if count == 0 {
		return nil, nil, nil, false
	}

	if u == nil && t != nil && r != nil {
		v := *t - *r
		u = &v
	}
	if t == nil && u != nil && r != nil {
		v := *u + *r
		t = &v
	}
	if r == nil && t != nil && u != nil {
		v := *t - *u
		r = &v
	}
	// 说明：曾经这里有一条"单位是 % 且只给一个数就当作剩余百分比"的兜底，
	// 但它不可达也无意义——unit 为 % 时 total 已被默认成 100，
	// 单值场景走的是上面的 total-remaining 推导；而若唯一的值就是 total，
	// 该兜底只是把 nil 赋给 nil。已删除。
	return u, t, r, true
}

// deriveStatus 推导状态。
//
// 优先用上游显式给的状态（它知道得比我们多），否则按阈值推。
func deriveStatus(row map[string]any, item Item, warningAt float64) Status {
	if raw, ok := lookup(row, "status"); ok {
		if s := NormalizeStatus(toStr(raw)); s != StatusUnknown {
			return s
		}
	}

	// 剩余归零比百分比更直接，优先判定
	if item.Remaining != nil && *item.Remaining <= 0 {
		return StatusExhausted
	}
	if item.Percent != nil {
		switch {
		case *item.Percent >= 100:
			return StatusExhausted
		case warningAt > 0 && *item.Percent >= warningAt:
			return StatusWarning
		default:
			return StatusOK
		}
	}
	if item.Remaining != nil {
		return StatusOK
	}
	// 三个数一个都没有：无从判断
	return StatusUnknown
}

// NormalizeStatus 归一状态。
//
// 无法识别时返回 StatusUnknown（而不是 StatusOK）——把"看不懂"当成"正常"
// 是最危险的默认值。
func NormalizeStatus(raw string) Status {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "ok", "normal", "healthy", "good", "正常":
		return StatusOK
	case "warning", "warn", "low", "告警", "偏低":
		return StatusWarning
	case "exhausted", "depleted", "empty", "used", "已用尽", "耗尽":
		return StatusExhausted
	default:
		return StatusUnknown
	}
}

// StatusRank 返回状态的可比序：ok < unknown < warning < exhausted。
//
// 导出它是因为"取最差"这个判断在编排层也要用（跨数据源比较）。
// 在那里再维护一份顺序表必然与这里漂移——顺序本就是契约的一部分。
func StatusRank(s Status) int {
	if r, ok := statusRank[s]; ok {
		return r
	}
	// 未知状态按 unknown 处理，而不是排到最后：
	// 排最后会让它盖过真实的 exhausted，"最差"就报错了。
	return statusRank[StatusUnknown]
}

// WorstStatus 取一组条目里最差的状态。整源状态由此得出。
func WorstStatus(items []Item) Status {
	worst := StatusOK
	for _, it := range items {
		if statusRank[it.Status] > statusRank[worst] {
			worst = it.Status
		}
	}
	return worst
}

// ---------------------------------------------------------------------------
// 展示文本
// ---------------------------------------------------------------------------

// DefaultFormat 按条目实际拿到的值给出默认格式模板。
//
// **不能只看单位**：额度类的默认模板 `{remaining} / {total} {unit}` 在缺 total
// 时会渲染出 `42 /  tokens` 这样带悬空分隔符与双空格的文本——deepseek 的
// 余额接口只返回 total_balance，正是最常见的"只有剩余"场景，等于是默认路径
// 上的瑕疵。缺分量时退回只报能报的那一部分。
func DefaultFormat(it Item) string {
	kind := UnitKindOf(it.Unit)

	// 百分比自带单位符号，也不能拼 unit（否则会出 "8% %"）；
	// 它在归一阶段就固定由 remaining 推出，因此含义恒为"还剩多少"。
	if kind == KindPercent {
		return "{remaining}%"
	}

	// unit 为空时不拼 {unit}，避免模板里留下一个渲染成空串的尾随占位符
	withUnit := func(base string) string {
		if it.Unit == "" {
			return base
		}
		return base + " {unit}"
	}

	switch {
	case it.Used != nil && it.Total != nil:
		// 与百分比默认同一口径：这是余量面板，主体报"还剩多少"。
		// 剩余量由 used/total 推出（"任二补一"），因此这里必然有值。
		return withUnit("{remaining} / {total}")
	case it.Remaining != nil:
		// 只有剩余：金额类要报出币种（"12.50" 不如 "12.50 CNY" 有用），
		// 其余单位本身就无歧义
		if kind == KindMoney {
			return withUnit("{remaining}")
		}
		return "{remaining}"
	case it.Used != nil:
		if kind == KindMoney {
			return withUnit("{used}")
		}
		return "{used}"
	case it.Total != nil:
		return withUnit("{total}")
	default:
		// 三个分量都没有：交给渲染器产出空串，由调用方隐藏该条目
		return "{remaining}"
	}
}

// RenderItem 按条目自己的 Format（没有则按实际值选默认）渲染展示文本。
//
// 模板语法：{used} {total} {remaining} {percent} {unit} {label} {window}
// 精度用 `{used:2}` 表示保留两位小数；`{window}` 渲染成中文窗口名。
func RenderItem(it Item) string {
	format := it.Format
	if format == "" {
		format = DefaultFormat(it)
	}
	return RenderTemplate(format, it)
}

// TryRenderItem 渲染并回报是否有可展示的内容。
//
// 保留 RenderItem 只返回字符串（`text` 字段的形状不变），把"有没有内容"
// 单独回给调用方：条目三个分量全缺时应被界面隐藏，而不是显示一个像样的
// 假值或一个空白的进度条行。
func TryRenderItem(it Item) (string, bool) {
	text := RenderItem(it)
	return text, text != ""
}

// RenderTemplate 按模板渲染。未知占位符原样保留——
// 静默吞掉会让"模板写错了"看起来像"没数据"。
func RenderTemplate(format string, it Item) string {
	var b strings.Builder
	for i := 0; i < len(format); {
		if format[i] != '{' {
			b.WriteByte(format[i])
			i++
			continue
		}
		end := strings.IndexByte(format[i:], '}')
		if end < 0 {
			b.WriteString(format[i:])
			break
		}
		token := format[i+1 : i+end]
		b.WriteString(renderToken(token, it))
		i += end + 1
	}
	return strings.TrimSpace(b.String())
}

func renderToken(token string, it Item) string {
	name := token
	digits := -1
	if idx := strings.IndexByte(token, ':'); idx >= 0 {
		name = token[:idx]
		if n, err := strconv.Atoi(token[idx+1:]); err == nil && n >= 0 {
			digits = n
		}
	}

	switch name {
	case "used":
		return formatNum(it.Used, it.Unit, digits)
	case "total":
		return formatNum(it.Total, it.Unit, digits)
	case "remaining":
		return formatNum(it.Remaining, it.Unit, digits)
	case "percent":
		if it.Percent == nil {
			return ""
		}
		if digits < 0 {
			digits = 1
		}
		return strconv.FormatFloat(*it.Percent, 'f', digits, 64)
	case "unit":
		return string(it.Unit)
	case "label":
		return it.Label
	case "window":
		return WindowLabel(it.Window)
	default:
		// 未知占位符原样保留
		return "{" + token + "}"
	}
}

// formatNum 格式化数值。
//
// digits 由调用方决定；未指定时金额与百分比给两位，额度类整数化
// （tokens 显示成 1234.5 没有意义），且大数用万/亿简写。
func formatNum(v *float64, u Unit, digits int) string {
	if v == nil {
		return ""
	}
	n := *v
	if digits >= 0 {
		return strconv.FormatFloat(n, 'f', digits, 64)
	}

	switch UnitKindOf(u) {
	case KindMoney:
		return strconv.FormatFloat(n, 'f', 2, 64)
	case KindPercent:
		return strconv.FormatFloat(n, 'f', 1, 64)
	default:
		return humanizeAmount(n)
	}
}

// humanizeAmount 把大额整数简写成万/亿，小数保留两位。
func humanizeAmount(n float64) string {
	abs := math.Abs(n)
	switch {
	case abs >= 1e8:
		return trimZero(n/1e8) + "亿"
	case abs >= 1e4:
		return trimZero(n/1e4) + "万"
	case n == math.Trunc(n):
		return strconv.FormatInt(int64(n), 10)
	default:
		return trimZero(n)
	}
}

func trimZero(n float64) string {
	s := strconv.FormatFloat(n, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}
