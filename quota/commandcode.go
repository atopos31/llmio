package quota

import (
	"fmt"
	"strings"
	"time"
)

// Command Code（commandcode.ai）套餐余量。
//
// 接口（2026-10-20 实测，Bearer API Key 鉴权）：
//
//	GET {base}/alpha/billing/credits        必需：三档窗口的已用量与剩余月度额度
//	GET {base}/alpha/billing/subscriptions  可选：套餐名、月度上限的依据与计费周期
//
// 为什么内置适配器要打两个端点：cc-switch 的自定义用量查询只允许一次请求，
// 社区里那两个脚本（usage-credits.js / usage-subscription.js）因此只能二选一——
// 要么知道 5 小时/周各用了多少，要么只知道套餐名与周期。内置适配器不受这个
// 限制：一次刷新连着打两个端点，三档已用量、月度总额度、周期结束时间就能
// 落在同一张卡上。
//
// credits 返回（实测节选）：
//
//	{
//	  credits: { monthlyCredits: 69.7968, purchasedCredits: 0, freeCredits: 0,
//	             belowThreshold: false, creditThreshold: 0 },
//	  windowLimits: {
//	    limited: true,
//	    fiveHour: { used: 0.2032, cap: 14, exceeded: false, resetAt: 1792494834039 },
//	    weekly:   { used: 0.2032, cap: 35, exceeded: false, resetAt: 1793081634039 } } }
//
// 两处容易读错的地方：
//
//   - credits.monthlyCredits 是**剩余**月度额度，不是上限；上限只能由订阅的
//     planId 推出来（见 commandCodePlanCredits）。
//   - windowLimits 里 used / cap 都是「已用 / 总量」，resetAt 是毫秒 epoch；
//     实测 5h cap = 月额度/5、weekly cap = 月额度/2（GOAT 70 → 14 / 35），
//     与套餐额度自洽，因此不需要再拿月额度去校正。
//
// subscriptions 返回（实测节选）：
//
//	{ success: true, data: { planId: "individual-goat", status: "active",
//	    currentPeriodStart: "2026-10-20T06:05:41.000Z",
//	    currentPeriodEnd:   "2026-11-20T06:05:41.000Z", cancelAtPeriodEnd: false } }

const (
	commandCodeCreditsPath       = "/alpha/billing/credits"
	commandCodeSubscriptionsPath = "/alpha/billing/subscriptions"
	// commandCodeDefaultTimeout 与注册表里该适配器的默认超时保持一致。
	commandCodeDefaultTimeout = 20 * time.Second
)

// commandCodePlanCredits 各套餐的月度额度上限（credits）。
//
// 与参考实现 cc-switch-commandcode-usage 的 usage-credits.js 同一份映射。
// 认不出的 planId 不影响 5 小时/每周两条窗口，只是月度那行算不出已用；
// 上游出新套餐时补一行即可。
var commandCodePlanCredits = map[string]float64{
	"individual-goat":     70,
	"individual-pro":      30,
	"individual-pro-v1":   80,
	"individual-provider": 15,
	"individual-max":      150,
	"individual-ultra":    300,
	"teams-pro":           40,
}

// commandCodeWindowMeta 两档滚动窗口的展示元信息。顺序固定 5 小时 → 每周。
//
// 月度那档不在这里：它的总量来自订阅端点而不是 windowLimits，
// 见 commandCodeMonthlyRow。
var commandCodeWindowMeta = []struct {
	Key    string
	Label  string
	Window Window
}{
	{"fiveHour", "5 小时限额", Window5h},
	{"weekly", "每周限额", WindowWeek},
}

// commandCodePlan 是从订阅端点读出来的套餐信息。
type commandCodePlan struct {
	ID             string
	Name           string
	Status         string
	PeriodStart    any
	PeriodEnd      any
	CancelAtPeriod bool
	// MonthlyCap 为 0 表示认不出 planId，月度上限未知。
	MonthlyCap float64
	CapKnown   bool
}

// runCommandCode 执行 Command Code 取数。
func runCommandCode(cfg BuiltinConfig, base string) (*HTTPAdapterResult, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("缺少 API Key：Command Code 的用量接口用 Bearer 鉴权，" +
			"请在该数据源的 apiKey 里填入 Command Code 的 API Key")
	}

	timeout := commandCodeDefaultTimeout
	if cfg.TimeoutMs > 0 {
		timeout = time.Duration(cfg.TimeoutMs) * time.Millisecond
	}
	if timeout < time.Second {
		timeout = time.Second
	}

	creditsPath := cfg.Path
	if creditsPath == "" {
		creditsPath = commandCodeCreditsPath
	}
	creditsRaw, err := fetchJSON(joinURL(base, creditsPath, cfg.Query), apiKey, cfg.Headers, timeout, nil)
	if err != nil {
		return nil, fmt.Errorf("取 credits 失败: %w%s", err, commandCodeStatusHint(err.Error()))
	}
	credits, ok := creditsRaw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("credits 响应不是对象，接口结构可能变了")
	}

	// 订阅端点只用来补「月度上限」与「周期结束」，失败不致命：少了它，
	// 5 小时/每周照常显示，月度那行退化为只显示剩余额度。
	plan := commandCodePlan{}
	subsRaw, subsErr := fetchJSON(joinURL(base, commandCodeSubscriptionsPath, nil), apiKey, cfg.Headers, timeout, nil)
	if subsErr == nil {
		plan = commandCodeParseSubscription(subsRaw)
	}

	items, err := commandCodeBuildItems(credits, plan)
	if err != nil {
		return nil, err
	}

	raw := map[string]any{"credits": credits, "meta": commandCodeMeta(plan)}
	if subsErr != nil {
		// 失败原因留在试跑面板里，别让「月度上限缺失」看起来像上游没给
		raw["subscriptionsError"] = subsErr.Error()
	} else {
		raw["subscriptions"] = subsRaw
	}
	return &HTTPAdapterResult{Items: items, Raw: raw}, nil
}

// commandCodeStatusHint 按状态码给出可操作的自查方向。
//
// fetchJSON 的报错是纯文本（"HTTP %d: %s"），这里按前缀做一次分类；
// 命中不了就原样返回，不硬造提示。
func commandCodeStatusHint(msg string) string {
	switch {
	case strings.Contains(msg, "HTTP 401"), strings.Contains(msg, "HTTP 403"):
		return "（API Key 无效或已撤销 → 到 Command Code 控制台重新生成一把填进 apiKey）"
	case strings.Contains(msg, "HTTP 404"):
		return "（路径不存在 → Command Code 改了 alpha 接口时，用该数据源的 path 覆盖" +
			commandCodeCreditsPath + "）"
	case strings.Contains(msg, "HTTP 429"):
		return "（请求过于频繁 → 调大刷新间隔后重试）"
	default:
		return ""
	}
}

// commandCodeParseSubscription 解析订阅端点。
//
// 结构不符时返回零值：调用方按「上限未知」处理，仍能出数。
func commandCodeParseSubscription(data any) commandCodePlan {
	out := commandCodePlan{}
	root, ok := data.(map[string]any)
	if !ok {
		return out
	}
	d, ok := root["data"].(map[string]any)
	if !ok {
		return out
	}
	out.ID = toStr(d["planId"])
	out.Status = toStr(d["status"])
	out.PeriodStart = d["currentPeriodStart"]
	out.PeriodEnd = d["currentPeriodEnd"]
	if b, ok := d["cancelAtPeriodEnd"].(bool); ok {
		out.CancelAtPeriod = b
	}
	if monthly, ok := commandCodePlanCredits[out.ID]; ok && monthly > 0 {
		out.MonthlyCap = monthly
		out.CapKnown = true
	}
	out.Name = commandCodePlanName(out.ID)
	return out
}

// commandCodePlanName 由 planId 推出展示名。
//
// 与参考实现同一口径：去掉 individual- 前缀再大写（individual-goat → GOAT）。
func commandCodePlanName(planID string) string {
	s := strings.TrimPrefix(strings.TrimSpace(planID), "individual-")
	return strings.ToUpper(s)
}

// commandCodeMeta 提取供试跑面板回显的元信息。
func commandCodeMeta(plan commandCodePlan) map[string]any {
	meta := map[string]any{
		"planId":            plan.ID,
		"planName":          plan.Name,
		"status":            plan.Status,
		"cancelAtPeriodEnd": plan.CancelAtPeriod,
		"monthlyCapKnown":   plan.CapKnown,
		"periodStart":       plan.PeriodStart,
		"periodEnd":         plan.PeriodEnd,
	}
	if plan.CapKnown {
		meta["monthlyCap"] = plan.MonthlyCap
	}
	return meta
}

// commandCodeBuildItems 把两个端点的响应拼成契约行。
//
// 三行：5 小时限额、每周限额、每月额度。前两行的总量由 windowLimits 直接给出；
// 月度那行的总量来自订阅的 planId（认不出时只显示剩余额度）。
func commandCodeBuildItems(credits map[string]any, plan commandCodePlan) ([]map[string]any, error) {
	out := []map[string]any{}

	windows, _ := credits["windowLimits"].(map[string]any)
	for _, meta := range commandCodeWindowMeta {
		w, ok := windows[meta.Key].(map[string]any)
		if !ok {
			continue
		}
		used := toFloat(w["used"])
		total := toFloat(w["cap"])
		if used == nil && total == nil {
			continue
		}
		row := map[string]any{
			"id":      meta.Key,
			"label":   meta.Label,
			"unit":    string(UnitCredits),
			"window":  string(meta.Window),
			"format":  "{used:2} / {total:2} CREDITS",
			"resetAt": w["resetAt"],
		}
		// exceeded 是上游自己的判定，比按百分比猜更准（额度可能提前判满）
		if exceeded, _ := w["exceeded"].(bool); exceeded {
			row["status"] = string(StatusExhausted)
		}
		// 必须写**解引用后的 float64**：契约的 toFloat 只认数值本体，
		// 放 *float64 进去会被当成不可识别的类型，整条被丢弃。
		if used != nil {
			row["used"] = *used
		}
		if total != nil {
			row["total"] = *total
		}
		out = append(out, row)
	}

	if row := commandCodeMonthlyRow(credits, plan); row != nil {
		out = append(out, row)
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("响应里没有可识别的额度：windowLimits 与 credits 都是空的")
	}
	return out, nil
}

// commandCodeMonthlyRow 拼「每月额度」那行。
//
// credits.monthlyCredits 是**剩余**额度，因此已用 = 上限 - 剩余；认不出 planId
// 时上限未知，就只报剩余额度——不给一个 100 分制的假百分比，那会让「用了多少」
// 看起来像真的。
func commandCodeMonthlyRow(credits map[string]any, plan commandCodePlan) map[string]any {
	bag, _ := credits["credits"].(map[string]any)
	if bag == nil {
		return nil
	}
	remaining := toFloat(bag["monthlyCredits"])
	if remaining == nil {
		return nil
	}

	extra := map[string]any{
		"purchasedCredits": bag["purchasedCredits"],
		"freeCredits":      bag["freeCredits"],
		"belowThreshold":   bag["belowThreshold"],
		"creditThreshold":  bag["creditThreshold"],
	}
	if plan.ID != "" {
		extra["planId"] = plan.ID
		extra["planName"] = plan.Name
		if plan.Name != "" {
			extra["note"] = plan.Name + " 套餐"
		}
	}
	if plan.Status != "" {
		extra["planStatus"] = plan.Status
	}
	if plan.CancelAtPeriod {
		extra["cancelAtPeriodEnd"] = true
	}

	row := map[string]any{
		"id":     "monthly",
		"label":  "每月额度",
		"unit":   string(UnitCredits),
		"window": string(WindowMonth),
		"extra":  extra,
	}
	if plan.CapKnown {
		used := plan.MonthlyCap - *remaining
		if used < 0 {
			used = 0
		}
		row["used"] = used
		row["total"] = plan.MonthlyCap
		row["format"] = "{used:2} / {total:2} CREDITS"
	} else {
		row["remaining"] = *remaining
		row["format"] = "{remaining:2} CREDITS"
	}
	if end := CoerceTime(plan.PeriodEnd); end != "" {
		row["resetAt"] = end
		extra["periodEnd"] = end
	}
	if start := CoerceTime(plan.PeriodStart); start != "" {
		extra["periodStart"] = start
	}
	// belowThreshold 是上游自己给的「低于阈值」信号，比按百分比猜更准
	if below, _ := bag["belowThreshold"].(bool); below {
		row["status"] = string(StatusWarning)
	}
	return row
}
