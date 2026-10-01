package models

import "time"

// KeyPeakCalendar 工作日日历在 configs 表中的键。
const KeyPeakCalendar = "peak_calendar"

// KeyPeakPricingLegacy 旧的全局分时段计费配置键。
//
// 峰谷条款现在挂在每条「模型 × 上游」关联上（ModelWithProvider.Peak），
// 全局只剩一份工作日日历。这个键只在升级时读一次，用来把旧配置拆成
// 日历（搬进 KeyPeakCalendar）与各条的条款（抄到关联行上）。
const KeyPeakPricingLegacy = "peak_pricing"

// PeakPeriod 一个计费时段。
//
// 定价方式是**相对基础价的乘数**，而非另存一套绝对价：
// 上游的峰谷策略基本都是"标准价的某个比例"（如夜间优惠 ×0.25），
// 用乘数可以同时表达涨价与降价，且不会让 ModelWithProvider 的价格字段翻三倍。
type PeakPeriod struct {
	Name string `json:"name"`
	// Start/End 为一天中的 "HH:MM"，允许 "24:00"。Start > End 表示跨零点。
	Start string `json:"start"`
	End   string `json:"end"`
	// Multiplier 为相对基础价的乘数，1 表示不加价不减价。
	Multiplier float64 `json:"multiplier"`
	// Days 限定星期几（0=周日 … 6=周六）。为空表示不限制。
	Days []int `json:"days,omitempty"`
	// Workday 限定是否只在工作日/休息日生效。nil 表示不限制。
	Workday *bool `json:"workday,omitempty"`
}

// PeakTerms 某个上游的峰谷计费条款。
//
// 时段的匹配顺序即优先级，**首个命中者胜出**：这样"先特例后一般"可以
// 直接靠数组顺序表达，不需要额外的优先级字段。
// 全部未命中时回落到乘数 1（基础价）。
//
// 条款而不是全局配置：峰谷窗口是**上游的商务条款**，同一时间点上
// A 家中转在打折、B 家已进入峰时是完全正常的，一份全局配置必然有一半是错的。
// 落点是 ModelWithProvider——价格（InputPrice 那三档）本来就挂在那里，
// 乘数作用于哪套基础价，就该和那套基础价待在同一行。
type PeakTerms struct {
	Enabled bool `json:"enabled"`
	// Periods 时段列表，顺序即优先级。
	Periods []PeakPeriod `json:"periods"`
}

// PeakCalendar 全局工作日日历。
//
// 留在全局而不是跟着条款走：这是一份**日历事实**（哪天放假、哪天调休、
// 按哪个时区算"今天"），对所有上游是同一个答案。每个关联各存一份的话，
// 同步一次节假日要写 N 遍，N 份之间还会不一致。
type PeakCalendar struct {
	// Timezone 用于判定"此刻是本地的几点/星期几"。为空时按 server 本地时区。
	// 必须显式配置的原因：上游的峰谷窗口按供应商所在时区定义，
	// 而部署机可能是任意时区，二者不一致会让时段整体错位。
	Timezone string `json:"timezone"`
	// Weekdays 工作日定义（0=周日 … 6=周六）。为空时默认周一至周五。
	Weekdays []int `json:"weekdays,omitempty"`
	// DateOverrides 按日期覆盖工作日判定，键为 "2006-01-02"，值为 "work" 或 "rest"。
	//
	// 这一项是法定节假日与调休的落点：中国的调休制度会让某个周六成为工作日，
	// 单靠星期几无法表达，必须能按具体日期覆盖。
	//
	// 刻意不加 omitempty：空 map 也要落库为 {}，否则配置文件里看不到这个字段，
	// 手工编辑时不知道有它可填。
	DateOverrides map[string]string `json:"dateOverrides"`
	// HolidaySyncedAt 最近一次同步节假日数据的 unix 秒，0 表示从未同步。
	HolidaySyncedAt int64 `json:"holidaySyncedAt,omitempty"`
	// HolidaySource 最近一次同步的数据来源，供界面展示来源与可信度。
	HolidaySource string `json:"holidaySource,omitempty"`
}

// 日期覆盖的取值。
const (
	DateOverrideWork = "work"
	DateOverrideRest = "rest"
)

// DefaultPeakTerms 返回一份新的峰谷条款模板：**关闭**状态，两段示例时段。
//
// 默认关闭是刻意的——分时段计费会改变成本数字的语义，
// 在用户明确配置前不应静默启用。
//
// 关着也给两段示例：打开开关的人马上要填的是时段，空白列表只会让人
// 先去别处找格式；照抄示例改数字比从零写起快得多，也不影响"没配就不生效"。
func DefaultPeakTerms() PeakTerms {
	return PeakTerms{
		Enabled: false,
		Periods: []PeakPeriod{
			{
				Name:       "标准时段",
				Start:      "08:30",
				End:        "00:30",
				Multiplier: 1,
			},
			{
				Name:       "夜间优惠",
				Start:      "00:30",
				End:        "08:30",
				Multiplier: 0.25,
			},
		},
	}
}

// DefaultPeakCalendar 返回默认工作日日历：东八区、周一至周五、无节假日数据。
func DefaultPeakCalendar() PeakCalendar {
	return PeakCalendar{
		Timezone:      "Asia/Shanghai",
		Weekdays:      []int{1, 2, 3, 4, 5},
		DateOverrides: map[string]string{},
	}
}

// HolidayDay 是节假日数据源的一条记录。
type HolidayDay struct {
	Date string `json:"date"`
	Name string `json:"name"`
	// IsOffDay 为 true 表示放假（休息日），false 表示调休上班（工作日）。
	IsOffDay bool `json:"isOffDay"`
}

// ToDateOverrides 把节假日数据转换为日期覆盖表。
func ToDateOverrides(days []HolidayDay) map[string]string {
	out := make(map[string]string, len(days))
	for _, d := range days {
		if d.Date == "" {
			continue
		}
		if d.IsOffDay {
			out[d.Date] = DateOverrideRest
		} else {
			out[d.Date] = DateOverrideWork
		}
	}
	return out
}

// EffectiveTime 返回用于时段判定的时间，已转换到配置的时区。
// 时区无法解析时退回原时间，调用方不应因此失败。
func (c PeakCalendar) EffectiveTime(t time.Time) time.Time {
	if c.Timezone == "" {
		return t
	}
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return t
	}
	return t.In(loc)
}
