package quota

import (
	"encoding/json"
	"strings"
	"testing"
)

// parseJSON 把 JSON 文本转成 Normalize 接受的 any 形状。
// 用 JSON 往返而不是手写 map，是为了让测试读起来跟真实数据一致。
func parseJSON(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("测试数据不是合法 JSON: %v", err)
	}
	return v
}

func approx(t *testing.T, got *float64, want float64, what string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: 期望 %v，实得 nil", what, want)
	}
	if diff := *got - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("%s: 期望 %v，实得 %v", what, want, *got)
	}
}

// ---------------------------------------------------------------------------
// 单位
// ---------------------------------------------------------------------------

func TestNormalizeUnit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want Unit
	}{
		{"%", UnitPercent}, {"percent", UnitPercent}, {"PERCENTAGE", UnitPercent}, {"百分比", UnitPercent},
		{"cny", UnitCNY}, {"RMB", UnitCNY}, {"元", UnitCNY}, {"人民币", UnitCNY}, {"¥", UnitCNY},
		{"usd", UnitUSD}, {"$", UnitUSD}, {"美元", UnitUSD},
		{"token", UnitTokens}, {"TOKENS", UnitTokens}, {"令牌", UnitTokens},
		{"credit", UnitCredits}, {"credits", UnitCredits}, {"算力", UnitCredits},
		{"time", UnitCount}, {"count", UnitCount}, {"次", UnitCount}, {"times", UnitCount},
		{"  元  ", UnitCNY},
		{"", UnitUnknown}, {"credits2", UnitUnknown}, {"点数", UnitUnknown},
	}
	for _, tc := range tests {
		if got := NormalizeUnit(tc.in); got != tc.want {
			t.Fatalf("NormalizeUnit(%q): 期望 %q，实得 %q", tc.in, tc.want, got)
		}
	}
}

func TestUnitKindOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   Unit
		want UnitKind
	}{
		{UnitPercent, KindPercent},
		{UnitCNY, KindMoney}, {UnitUSD, KindMoney},
		{UnitTokens, KindAmount}, {UnitCredits, KindAmount}, {UnitCount, KindAmount},
		{UnitUnknown, KindUnknown}, {Unit("weird"), KindUnknown},
	}
	for _, tc := range tests {
		if got := UnitKindOf(tc.in); got != tc.want {
			t.Fatalf("UnitKindOf(%q): 期望 %q，实得 %q", tc.in, tc.want, got)
		}
	}
}

// ---------------------------------------------------------------------------
// 窗口
// ---------------------------------------------------------------------------

func TestNormalizeWindow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want Window
	}{
		{"5h", Window5h}, {"5小时", Window5h}, {"五小时", Window5h},
		{"day", WindowDay}, {"DAILY", WindowDay}, {"日", WindowDay}, {"天", WindowDay}, {"每日", WindowDay},
		{"week", WindowWeek}, {"weekly", WindowWeek}, {"周", WindowWeek}, {"每周", WindowWeek},
		{"month", WindowMonth}, {"monthly", WindowMonth}, {"月", WindowMonth}, {"每月", WindowMonth},
		{"total", WindowTotal}, {"总额", WindowTotal}, {"余额", WindowTotal}, {"all", WindowTotal}, {"总计", WindowTotal},
		{" 月 ", WindowMonth},
		{"", WindowNone}, {"季度", WindowNone},
	}
	for _, tc := range tests {
		if got := NormalizeWindow(tc.in); got != tc.want {
			t.Fatalf("NormalizeWindow(%q): 期望 %q，实得 %q", tc.in, tc.want, got)
		}
	}
}

func TestWindowLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   Window
		want string
	}{
		{Window5h, "5 小时"},
		{WindowDay, "每日"},
		{WindowWeek, "每周"},
		{WindowMonth, "每月"},
		{WindowTotal, "总额"},
		{WindowNone, ""},
		{Window("other"), ""},
	}
	for _, tc := range tests {
		if got := WindowLabel(tc.in); got != tc.want {
			t.Fatalf("WindowLabel(%q): 期望 %q，实得 %q", tc.in, tc.want, got)
		}
	}
}

// ---------------------------------------------------------------------------
// 时间
// ---------------------------------------------------------------------------

func TestCoerceTime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   any
		want string
	}{
		{name: "nil", in: nil, want: ""},
		{name: "空串", in: "", want: ""},
		{name: "空白串", in: "   ", want: ""},
		// 秒级时间戳（10 位）
		{name: "秒级时间戳字符串", in: "1735689600", want: "2025-01-01T00:00:00Z"},
		// 毫秒级时间戳（13 位）
		{name: "毫秒级时间戳字符串", in: "1735689600000", want: "2025-01-01T00:00:00Z"},
		{name: "float64 秒", in: float64(1735689600), want: "2025-01-01T00:00:00Z"},
		{name: "float64 毫秒", in: float64(1735689600000), want: "2025-01-01T00:00:00Z"},
		{name: "int64 秒", in: int64(1735689600), want: "2025-01-01T00:00:00Z"},
		{name: "非正数时间戳", in: float64(0), want: ""},
		{name: "负数时间戳", in: int64(-1), want: ""},

		{name: "RFC3339", in: "2025-01-01T00:00:00Z", want: "2025-01-01T00:00:00Z"},
		{name: "T 分隔无时区", in: "2025-01-01T08:30:00", want: "2025-01-01T08:30:00Z"},
		{name: "空格分隔", in: "2025-01-01 08:30:00", want: "2025-01-01T08:30:00Z"},
		{name: "精确到分", in: "2025-01-01 08:30", want: "2025-01-01T08:30:00Z"},
		{name: "仅日期", in: "2025-01-01", want: "2025-01-01T00:00:00Z"},
		{name: "斜杠日期时间", in: "2025/01/01 08:30:00", want: "2025-01-01T08:30:00Z"},
		{name: "斜杠日期", in: "2025/01/01", want: "2025-01-01T00:00:00Z"},
		{name: "前后空白", in: "  2025-01-01  ", want: "2025-01-01T00:00:00Z"},

		{name: "无法解析", in: "not a time", want: ""},
		{name: "不支持的 Go 类型", in: true, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := CoerceTime(tc.in); got != tc.want {
				t.Fatalf("期望 %q，实得 %q", tc.want, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 输入形状
// ---------------------------------------------------------------------------

func TestNormalizeInputShapes(t *testing.T) {
	t.Parallel()

	one := `{"used": 3, "total": 10}`

	tests := []struct {
		name      string
		raw       string
		wantCount int
		wantFirst float64 // 第一条的 used
	}{
		{name: "items 数组", raw: `{"items": [` + one + `]}`, wantCount: 1, wantFirst: 3},
		{name: "裸数组", raw: `[` + one + `]`, wantCount: 1, wantFirst: 3},
		{name: "data 数组", raw: `{"data": [` + one + `]}`, wantCount: 1, wantFirst: 3},
		{name: "list 数组", raw: `{"list": [` + one + `]}`, wantCount: 1, wantFirst: 3},
		{name: "result 数组", raw: `{"result": [` + one + `]}`, wantCount: 1, wantFirst: 3},
		{name: "results 数组", raw: `{"results": [` + one + `]}`, wantCount: 1, wantFirst: 3},
		{name: "单条对象", raw: one, wantCount: 1, wantFirst: 3},
		{name: "items 对象映射", raw: `{"items": {"a": {"used": 3, "total": 10}}}`, wantCount: 1, wantFirst: 3},
		{name: "data 对象映射", raw: `{"data": {"a": {"used": 3, "total": 10}}}`, wantCount: 1, wantFirst: 3},
		{name: "items 标量映射", raw: `{"items": {"a": 5, "b": 7}}`, wantCount: 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			items, err := Normalize(parseJSON(t, tc.raw), NormalizeOptions{})
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			if len(items) != tc.wantCount {
				t.Fatalf("期望 %d 条，实得 %d", tc.wantCount, len(items))
			}
			if tc.wantFirst != 0 {
				approx(t, items[0].Used, tc.wantFirst, "used")
			}
		})
	}
}

func TestNormalizeSkipsNonRecords(t *testing.T) {
	t.Parallel()

	// 数组里混入非对象元素：跳过它们，不当成数据
	raw := `{"items": ["bad", 42, null, {"used": 1, "total": 2}, [], {"used": 3, "total": 4}]}`
	items, err := Normalize(parseJSON(t, raw), NormalizeOptions{})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("期望 2 条（跳过非对象），实得 %d", len(items))
	}
}

func TestNormalizeEmptyInnerFallsThrough(t *testing.T) {
	t.Parallel()

	// items 是空数组 → 回退到"把外层当单条"，外层没有数值则报错
	_, err := Normalize(parseJSON(t, `{"items": []}`), NormalizeOptions{})
	if err == nil {
		t.Fatal("没有任何可识别数值时应报错")
	}
}

func TestNormalizeErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  any
	}{
		{name: "nil", raw: nil},
		{name: "标量", raw: 42.0},
		{name: "字符串", raw: "hello"},
		{name: "空对象", raw: map[string]any{}},
		{name: "对象里没有数值字段", raw: map[string]any{"foo": "bar"}},
		{name: "数组里全非对象", raw: []any{"a", 1, nil}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Normalize(tc.raw, NormalizeOptions{}); err == nil {
				t.Fatal("应当报错")
			}
		})
	}
}

// 这是契约里最重要的一条约定：不能把"看不懂"当成"没有余量"。
func TestNormalizeErrorIsNotSilentEmpty(t *testing.T) {
	t.Parallel()

	_, err := Normalize(map[string]any{"status": "ok", "message": "all good"}, NormalizeOptions{})
	if err == nil {
		t.Fatal("接口返回 200 但内容无法识别时必须报错，不能默默返回空列表")
	}
	if !strings.Contains(err.Error(), "没有任何可识别") {
		t.Fatalf("错误信息应说明原因，实得: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 三者任意两个
// ---------------------------------------------------------------------------

func TestNormalizeCompletion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		raw           string
		wantUsed      *float64
		wantTotal     *float64
		wantRemaining *float64
	}{
		{name: "给 total 与 remaining，推出 used", raw: `{"total": 10, "remaining": 4}`,
			wantUsed: f(6), wantTotal: f(10), wantRemaining: f(4)},
		{name: "给 used 与 remaining，推出 total", raw: `{"used": 6, "remaining": 4}`,
			wantUsed: f(6), wantTotal: f(10), wantRemaining: f(4)},
		{name: "给 used 与 total，推出 remaining", raw: `{"used": 6, "total": 10}`,
			wantUsed: f(6), wantTotal: f(10), wantRemaining: f(4)},
		{name: "三个都给，原样保留", raw: `{"used": 6, "total": 10, "remaining": 4}`,
			wantUsed: f(6), wantTotal: f(10), wantRemaining: f(4)},
		{name: "只给 total", raw: `{"total": 10}`,
			wantTotal: f(10)},
		{name: "只给 used", raw: `{"used": 6}`,
			wantUsed: f(6)},
		{name: "只给 remaining", raw: `{"remaining": 4}`,
			wantRemaining: f(4)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			items, err := Normalize(parseJSON(t, tc.raw), NormalizeOptions{})
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			it := items[0]
			if tc.wantUsed == nil {
				if it.Used != nil {
					t.Fatalf("used 应当为 nil，实得 %v", *it.Used)
				}
			} else {
				approx(t, it.Used, *tc.wantUsed, "used")
			}
			if tc.wantTotal == nil {
				if it.Total != nil {
					t.Fatalf("total 应当为 nil，实得 %v", *it.Total)
				}
			} else {
				approx(t, it.Total, *tc.wantTotal, "total")
			}
			if tc.wantRemaining == nil {
				if it.Remaining != nil {
					t.Fatalf("remaining 应当为 nil，实得 %v", *it.Remaining)
				}
			} else {
				approx(t, it.Remaining, *tc.wantRemaining, "remaining")
			}
		})
	}
}

func f(v float64) *float64 { return &v }

func TestNormalizePercentUnitDefaultsTotalTo100(t *testing.T) {
	t.Parallel()

	// 单位是 % 却没给总量：总量按 100 算，否则百分比无从比较
	items, err := Normalize(parseJSON(t, `{"used": 30, "unit": "%"}`), NormalizeOptions{})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	it := items[0]
	approx(t, it.Total, 100, "total")
	approx(t, it.Remaining, 70, "remaining")
	// percent 是已用百分比：用了 30，总量 100
	approx(t, it.Percent, 30, "percent")
}

func TestNormalizePercentUnitSingleValue(t *testing.T) {
	t.Parallel()

	// 只给一个数且单位是 %：当作剩余百分比
	items, err := Normalize(parseJSON(t, `{"remaining": 42, "unit": "%"}`), NormalizeOptions{})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	// 剩余 42% → 已用 58%
	approx(t, items[0].Percent, 58, "percent")
}

func TestNormalizeNonNumericFieldsIgnored(t *testing.T) {
	t.Parallel()

	// 字段存在但不是数值：应当视为"没给"，而不是当成 0
	items, err := Normalize(parseJSON(t, `{"used": "abc", "total": 10, "remaining": 4}`), NormalizeOptions{})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	it := items[0]
	// used 被视作未提供，由 total - remaining 补出
	approx(t, it.Used, 6, "used")
}

func TestNormalizeNumericStrings(t *testing.T) {
	t.Parallel()

	items, err := Normalize(parseJSON(t, `{"used": "6", "total": "10.5"}`), NormalizeOptions{})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	approx(t, items[0].Used, 6, "used")
	approx(t, items[0].Total, 10.5, "total")
}

func TestNormalizePercentSuffixStripped(t *testing.T) {
	t.Parallel()

	items, err := Normalize(parseJSON(t, `{"remaining": "42%", "total": 100}`), NormalizeOptions{})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	approx(t, items[0].Remaining, 42, "remaining")
}

func TestNormalizeFloatEdgeValues(t *testing.T) {
	t.Parallel()

	// NaN / Inf 不是合法数值，应视为未提供
	raw := map[string]any{
		"used":      nan(),
		"total":     inf(),
		"remaining": 4.0,
	}
	items, err := Normalize(raw, NormalizeOptions{})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if items[0].Used != nil {
		t.Fatalf("NaN 应被视为未提供，实得 %v", *items[0].Used)
	}
	if items[0].Total != nil {
		t.Fatalf("Inf 应被视为未提供，实得 %v", *items[0].Total)
	}
}

func nan() float64 { var z float64; return z / z }
func inf() float64 { var z float64; return 1 / z }

// ---------------------------------------------------------------------------
// 别名
// ---------------------------------------------------------------------------

func TestNormalizeFieldAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		raw       string
		wantUsed  float64
		wantTotal float64
	}{
		{name: "used/usedAmount", raw: `{"usedAmount": 1, "total": 10}`, wantUsed: 1, wantTotal: 10},
		{name: "usage", raw: `{"usage": 1, "limit": 10}`, wantUsed: 1, wantTotal: 10},
		{name: "中文已用/总量", raw: `{"已用": 1, "总量": 10}`, wantUsed: 1, wantTotal: 10},
		{name: "consumed/quota", raw: `{"consumed": 1, "quota": 10}`, wantUsed: 1, wantTotal: 10},
		{name: "snake_case", raw: `{"used_amount": 1, "total_amount": 10}`, wantUsed: 1, wantTotal: 10},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			items, err := Normalize(parseJSON(t, tc.raw), NormalizeOptions{})
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			approx(t, items[0].Used, tc.wantUsed, "used")
			approx(t, items[0].Total, tc.wantTotal, "total")
		})
	}
}

func TestNormalizeRemainingAliases(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"remaining", "remain", "left", "balance", "剩余", "余额"} {
		raw := map[string]any{key: 4.0, "total": 10.0}
		items, err := Normalize(raw, NormalizeOptions{})
		if err != nil {
			t.Fatalf("%s: 意外错误 %v", key, err)
		}
		approx(t, items[0].Remaining, 4, key)
	}
}

func TestNormalizeLabelFallsBackToID(t *testing.T) {
	t.Parallel()

	items, err := Normalize(parseJSON(t, `{"items": {"mykey": {"used": 1, "total": 2}}}`), NormalizeOptions{})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if items[0].ID != "mykey" || items[0].Label != "mykey" {
		t.Fatalf("映射形式的键应当同时作为 id 与 label，实得 id=%q label=%q", items[0].ID, items[0].Label)
	}
}

func TestNormalizeIDGeneration(t *testing.T) {
	t.Parallel()

	items, err := Normalize(parseJSON(t, `[{"used":1,"total":2},{"used":3,"total":4}]`),
		NormalizeOptions{IDPrefix: "src-"})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if items[0].ID != "src-item-1" || items[1].ID != "src-item-2" {
		t.Fatalf("自动 ID 不符：%q %q", items[0].ID, items[1].ID)
	}
}

func TestNormalizeNonMapExtraIgnored(t *testing.T) {
	t.Parallel()

	items, err := Normalize(parseJSON(t, `{"used":1,"total":2,"extra":"not a map"}`), NormalizeOptions{})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if len(items[0].Extra) != 0 {
		t.Fatalf("非对象的 extra 应被忽略，实得 %v", items[0].Extra)
	}
}

func TestNormalizeExtraPreserved(t *testing.T) {
	t.Parallel()

	items, err := Normalize(parseJSON(t, `{"used":1,"total":2,"extra":{"a":1,"b":"x"}}`), NormalizeOptions{})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if items[0].Extra["a"] != 1.0 || items[0].Extra["b"] != "x" {
		t.Fatalf("extra 未保留：%v", items[0].Extra)
	}
}

// ---------------------------------------------------------------------------
// 状态
// ---------------------------------------------------------------------------

func TestNormalizeStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want Status
	}{
		{"ok", StatusOK}, {"normal", StatusOK}, {"healthy", StatusOK}, {"good", StatusOK}, {"正常", StatusOK},
		{"warning", StatusWarning}, {"warn", StatusWarning}, {"low", StatusWarning}, {"告警", StatusWarning}, {"偏低", StatusWarning},
		{"exhausted", StatusExhausted}, {"depleted", StatusExhausted}, {"empty", StatusExhausted},
		{"used", StatusExhausted}, {"已用尽", StatusExhausted}, {"耗尽", StatusExhausted},
		{"OK", StatusOK}, {"  Warning  ", StatusWarning},
		// 看不懂就是看不懂，不能默认成 ok
		{"", StatusUnknown}, {"weird", StatusUnknown}, {"paused", StatusUnknown},
	}
	for _, tc := range tests {
		if got := NormalizeStatus(tc.in); got != tc.want {
			t.Fatalf("NormalizeStatus(%q): 期望 %q，实得 %q", tc.in, tc.want, got)
		}
	}
}

func TestStatusDerivedFromPercent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		raw       string
		warningAt float64
		want      Status
	}{
		{name: "额度充足", raw: `{"used": 1, "total": 100}`, warningAt: 80, want: StatusOK},
		{name: "达到告警线", raw: `{"used": 85, "total": 100}`, warningAt: 80, want: StatusWarning},
		{name: "恰好用完", raw: `{"used": 100, "total": 100}`, warningAt: 80, want: StatusExhausted},
		{name: "超额", raw: `{"used": 120, "total": 100}`, warningAt: 80, want: StatusExhausted},
		{name: "未设告警线则不低于告警", raw: `{"used": 85, "total": 100}`, warningAt: 0, want: StatusOK},
		{name: "告警线为100时只有用尽才告警", raw: `{"used": 99, "total": 100}`, warningAt: 100, want: StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			items, err := Normalize(parseJSON(t, tc.raw), NormalizeOptions{WarningAt: tc.warningAt})
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			if items[0].Status != tc.want {
				t.Fatalf("期望 %q，实得 %q（percent=%v）", tc.want, items[0].Status, items[0].Percent)
			}
		})
	}
}

func TestStatusDerivedFromRemainingOnly(t *testing.T) {
	t.Parallel()

	// 没有百分比时按剩余量判断
	items, err := Normalize(parseJSON(t, `{"remaining": 0}`), NormalizeOptions{})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if items[0].Status != StatusExhausted {
		t.Fatalf("剩余为 0 应为 exhausted，实得 %q", items[0].Status)
	}

	items, err = Normalize(parseJSON(t, `{"remaining": 5}`), NormalizeOptions{})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if items[0].Status != StatusOK {
		t.Fatalf("剩余为正应为 ok，实得 %q", items[0].Status)
	}
}

func TestStatusExplicitBeatsDerived(t *testing.T) {
	t.Parallel()

	// 上游显式给了 status，它知道得比我们多
	items, err := Normalize(parseJSON(t, `{"used": 1, "total": 100, "status": "exhausted"}`), NormalizeOptions{})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if items[0].Status != StatusExhausted {
		t.Fatalf("显式状态应当优先，实得 %q", items[0].Status)
	}
}

func TestStatusUnrecognizedFallsBackToDerived(t *testing.T) {
	t.Parallel()

	// 状态值看不懂时，用可算的百分比推导，而不是直接标 unknown
	items, err := Normalize(parseJSON(t, `{"used": 90, "total": 100, "status": "много"}`),
		NormalizeOptions{WarningAt: 80})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if items[0].Status != StatusWarning {
		t.Fatalf("无法识别的状态应回落到推导，实得 %q", items[0].Status)
	}
}

func TestStatusUnknownWhenNoNumbersAtAll(t *testing.T) {
	t.Parallel()

	// 三个数都没有 → 无从判断。这条会被 complete 挡下，因此构造一条只有一个数的
	items, err := Normalize(parseJSON(t, `{"label": "x", "remaining": 5}`), NormalizeOptions{})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if items[0].Status != StatusOK {
		t.Fatalf("有剩余量时不应是 unknown，实得 %q", items[0].Status)
	}
}

func TestWorstStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		items []Item
		want  Status
	}{
		{name: "空列表视为 ok", items: nil, want: StatusOK},
		{name: "全 ok", items: []Item{{Status: StatusOK}, {Status: StatusOK}}, want: StatusOK},
		{name: "有告警取告警", items: []Item{{Status: StatusOK}, {Status: StatusWarning}}, want: StatusWarning},
		{name: "有耗尽取耗尽", items: []Item{{Status: StatusWarning}, {Status: StatusExhausted}}, want: StatusExhausted},
		{name: "unknown 排在 ok 之后警告之前", items: []Item{{Status: StatusOK}, {Status: StatusUnknown}}, want: StatusUnknown},
		{name: "告警重于 unknown", items: []Item{{Status: StatusUnknown}, {Status: StatusWarning}}, want: StatusWarning},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := WorstStatus(tc.items); got != tc.want {
				t.Fatalf("期望 %q，实得 %q", tc.want, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 展示文本
// ---------------------------------------------------------------------------

func TestDefaultFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		unit Unit
		want string
	}{
		{UnitPercent, "{remaining}%"},
		{UnitCNY, "{remaining} {unit}"},
		{UnitUSD, "{remaining} {unit}"},
		{UnitTokens, "{remaining} / {total} {unit}"},
		{UnitCredits, "{remaining} / {total} {unit}"},
		{UnitCount, "{remaining} / {total} {unit}"},
		{UnitUnknown, "{remaining}"},
	}
	for _, tc := range tests {
		if got := DefaultFormat(tc.unit); got != tc.want {
			t.Fatalf("DefaultFormat(%q): 期望 %q，实得 %q", tc.unit, tc.want, got)
		}
	}
}

func TestRenderTemplate(t *testing.T) {
	t.Parallel()

	item := Item{
		Label: "月度额度", Unit: UnitTokens, Window: WindowMonth,
		Used: f(300), Total: f(1000), Remaining: f(700), Percent: f(30),
	}

	tests := []struct {
		name   string
		format string
		want   string
	}{
		{name: "remaining 与 total", format: "{remaining} / {total}", want: "700 / 1,000"},
		{name: "percent", format: "{percent}%", want: "30.0%"},
		{name: "percent 指定精度", format: "{percent:0}%", want: "30%"},
		{name: "used 指定精度", format: "{used:2}", want: "300.00"},
		{name: "unit 与 label", format: "{label} {unit}", want: "月度额度 tokens"},
		{name: "window 渲染为中文", format: "{used} {window}", want: "300 每月"},
		{name: "无占位符原样", format: "固定文本", want: "固定文本"},
		{name: "未知占位符保留", format: "{nope}", want: "{nope}"},
		{name: "未闭合花括号保留", format: "abc{used", want: "abc{used"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := RenderTemplate(tc.format, item)
			// 数字带千分位时不做精确比较
			if tc.name == "remaining 与 total" {
				if !strings.Contains(got, "700") || !strings.Contains(got, "1") {
					t.Fatalf("期望含 700 与 1000，实得 %q", got)
				}
				return
			}
			if got != tc.want {
				t.Fatalf("期望 %q，实得 %q", tc.want, got)
			}
		})
	}
}

func TestRenderTemplateMissingFieldsBecomeEmpty(t *testing.T) {
	t.Parallel()

	// 引用了没值的字段 → 该处渲染成空串，而不是 "0"
	item := Item{Unit: UnitTokens}
	if got := RenderTemplate("{used}|{total}", item); got != "|" {
		t.Fatalf("缺失字段应渲染为空，实得 %q", got)
	}
	if got := RenderTemplate("{percent}", item); got != "" {
		t.Fatalf("缺失 percent 应为空，实得 %q", got)
	}
}

func TestRenderItemUsesDefaultWhenNoFormat(t *testing.T) {
	t.Parallel()

	items, err := Normalize(parseJSON(t, `{"used":300,"total":1000,"unit":"tokens"}`), NormalizeOptions{})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if !strings.Contains(items[0].Text, "700") {
		t.Fatalf("默认模板应渲染出剩余量，实得 %q", items[0].Text)
	}
}

func TestRenderItemHonorsCustomFormat(t *testing.T) {
	t.Parallel()

	items, err := Normalize(parseJSON(t, `{"used":300,"total":1000,"unit":"tokens","format":"额度 {percent:0}%"}`), NormalizeOptions{})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if items[0].Text != "额度 30%" {
		t.Fatalf("自定义模板未生效，实得 %q", items[0].Text)
	}
}

func TestHumanizeAmount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1234, "1234"},
		{10000, "1万"},
		{12345, "1.23万"},
		{100000000, "1亿"},
		{123456789, "1.23亿"},
		{1500.5, "1500.5"},
		{-20000, "-2万"},
	}
	for _, tc := range tests {
		if got := humanizeAmount(tc.in); got != tc.want {
			t.Fatalf("humanizeAmount(%v): 期望 %q，实得 %q", tc.in, tc.want, got)
		}
	}
}

func TestFormatNumMoneyAndPercent(t *testing.T) {
	t.Parallel()

	if got := formatNum(f(12.5), UnitCNY, -1); got != "12.50" {
		t.Fatalf("金额应两位小数，实得 %q", got)
	}
	if got := formatNum(f(30), UnitPercent, -1); got != "30.0" {
		t.Fatalf("百分比应一位小数，实得 %q", got)
	}
	if got := formatNum(nil, UnitCNY, -1); got != "" {
		t.Fatalf("nil 应为空，实得 %q", got)
	}
	if got := formatNum(f(1.23456), UnitCNY, 4); got != "1.2346" {
		t.Fatalf("指定精度未生效，实得 %q", got)
	}
}

// ---------------------------------------------------------------------------
// 字面值转换
// ---------------------------------------------------------------------------

func TestToStr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   any
		want string
	}{
		{"  x  ", "x"},
		{float64(42), "42"},
		{float64(42.5), "42.5"},
		{int64(7), "7"},
		{true, "true"},
		{false, "false"},
		{nil, ""},
		{[]any{1}, ""},
	}
	for _, tc := range tests {
		if got := toStr(tc.in); got != tc.want {
			t.Fatalf("toStr(%v): 期望 %q，实得 %q", tc.in, tc.want, got)
		}
	}
}

func TestToFloat(t *testing.T) {
	t.Parallel()

	if toFloat(nil) != nil {
		t.Fatal("nil 应为 nil")
	}
	if toFloat("") != nil {
		t.Fatal("空串应为 nil")
	}
	if toFloat("abc") != nil {
		t.Fatal("非数值串应为 nil")
	}
	if toFloat([]any{}) != nil {
		t.Fatal("数组应为 nil")
	}
	if got := toFloat(int(5)); got == nil || *got != 5 {
		t.Fatalf("int 应可转换，实得 %v", got)
	}
	if got := toFloat(int64(5)); got == nil || *got != 5 {
		t.Fatalf("int64 应可转换，实得 %v", got)
	}
	if got := toFloat("  7.5  "); got == nil || *got != 7.5 {
		t.Fatalf("带空白数值串应可转换，实得 %v", got)
	}
}

// ---------------------------------------------------------------------------
// 确定性
// ---------------------------------------------------------------------------

// 同一份数据每次归一的结果必须完全一致，否则界面会无故跳动。
func TestNormalizeIsDeterministic(t *testing.T) {
	t.Parallel()

	raw := `{"items": {"z": {"used":1,"total":2}, "a": {"used":3,"total":4}, "m": {"used":5,"total":6}}}`

	var first []string
	for i := 0; i < 20; i++ {
		items, err := Normalize(parseJSON(t, raw), NormalizeOptions{})
		if err != nil {
			t.Fatalf("意外错误: %v", err)
		}
		ids := make([]string, 0, len(items))
		for _, it := range items {
			ids = append(ids, it.ID)
		}
		joined := strings.Join(ids, ",")
		if i == 0 {
			first = ids
			continue
		}
		if joined != strings.Join(first, ",") {
			t.Fatalf("第 %d 次结果不同：%v vs %v", i, ids, first)
		}
	}
	// 对象映射的键序应当被排序
	if strings.Join(first, ",") != "a,m,z" {
		t.Fatalf("对象映射应按键排序输出，实得 %v", first)
	}
}

// percent 是"已用百分比"而非"剩余百分比"。
// 这个方向搞反会让状态判断整体反转——用尽的显示成健康、健康的显示成用尽。
func TestPercentMeansConsumedNotRemaining(t *testing.T) {
	t.Parallel()

	items, err := Normalize(parseJSON(t, `{"used": 90, "total": 100}`), NormalizeOptions{WarningAt: 80})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	it := items[0]
	approx(t, it.Percent, 90, "percent 应为已用百分比")
	if it.Status != StatusWarning {
		t.Fatalf("用掉九成应告警，实得 %q", it.Status)
	}

	// 反过来：只用了一成
	items, err = Normalize(parseJSON(t, `{"used": 10, "total": 100}`), NormalizeOptions{WarningAt: 80})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if items[0].Status != StatusOK {
		t.Fatalf("只用一成应为 ok，实得 %q", items[0].Status)
	}
}

// ---------------------------------------------------------------------------
// 字段透传
// ---------------------------------------------------------------------------

func TestNormalizeWindowField(t *testing.T) {
	t.Parallel()

	items, err := Normalize(parseJSON(t, `{"used":1,"total":2,"window":"每月"}`), NormalizeOptions{})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if items[0].Window != WindowMonth {
		t.Fatalf("window 未归一，实得 %q", items[0].Window)
	}
}

func TestNormalizeTimeFields(t *testing.T) {
	t.Parallel()

	items, err := Normalize(parseJSON(t, `{
		"used":1,"total":2,
		"resetAt":"2025-01-01 00:00:00",
		"expireAt":1735689600000
	}`), NormalizeOptions{})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if items[0].ResetAt != "2025-01-01T00:00:00Z" {
		t.Fatalf("resetAt 未归一，实得 %q", items[0].ResetAt)
	}
	if items[0].ExpireAt != "2025-01-01T00:00:00Z" {
		t.Fatalf("expireAt 未归一，实得 %q", items[0].ExpireAt)
	}
}

func TestNormalizeTimeFieldUnparsableBecomesEmpty(t *testing.T) {
	t.Parallel()

	// 解析不了的时间应当留空，而不是原样塞给前端
	items, err := Normalize(parseJSON(t, `{"used":1,"total":2,"resetAt":"下个月"}`), NormalizeOptions{})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if items[0].ResetAt != "" {
		t.Fatalf("无法解析的时间应为空，实得 %q", items[0].ResetAt)
	}
}

// % 单位只给一个数时的行为：total 默认 100，因此给的是"已用"还是"剩余"
// 由字段名决定，不做猜测。
func TestCompleteSinglePercentValue(t *testing.T) {
	t.Parallel()

	t.Run("只给 used", func(t *testing.T) {
		items, err := Normalize(parseJSON(t, `{"used": 25, "unit": "%"}`), NormalizeOptions{})
		if err != nil {
			t.Fatalf("意外错误: %v", err)
		}
		// 用了 25%（total 默认 100）→ 还剩 75%
		approx(t, items[0].Remaining, 75, "remaining")
		approx(t, items[0].Percent, 25, "percent")
	})

	t.Run("只给 remaining", func(t *testing.T) {
		items, err := Normalize(parseJSON(t, `{"remaining": 25, "unit": "%"}`), NormalizeOptions{})
		if err != nil {
			t.Fatalf("意外错误: %v", err)
		}
		approx(t, items[0].Remaining, 25, "remaining")
		approx(t, items[0].Percent, 75, "percent")
	})

	t.Run("只给 total 时剩余未知", func(t *testing.T) {
		items, err := Normalize(parseJSON(t, `{"total": 50, "unit": "%"}`), NormalizeOptions{})
		if err != nil {
			t.Fatalf("意外错误: %v", err)
		}
		if items[0].Remaining != nil {
			t.Fatalf("只给总量时剩余应为未知，实得 %v", *items[0].Remaining)
		}
		if items[0].Status != StatusUnknown {
			t.Fatalf("无从判断时状态应为 unknown，实得 %q", items[0].Status)
		}
	})
}

// percent >= 100 这条分支只在"没有 remaining 可比"时可达——
// 有 remaining 时"剩余归零"会先命中。
func TestStatusExhaustedFromPercentWithoutRemaining(t *testing.T) {
	t.Parallel()

	raw := map[string]any{"used": 120.0, "total": 100.0}
	items, err := Normalize(raw, NormalizeOptions{WarningAt: 80})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	it := items[0]
	// complete 会推出 remaining = -20，因此这里其实走了"剩余归零"分支；
	// 直接构造一个没有 remaining 的条目来覆盖 percent 分支
	it.Remaining = nil
	it.Percent = f(120)
	if got := deriveStatus(map[string]any{}, it, 80); got != StatusExhausted {
		t.Fatalf("已用超过 100%% 应为 exhausted，实得 %q", got)
	}
}
