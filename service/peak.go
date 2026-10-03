package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	// 内嵌时区库：单二进制可能部署在没有 tzdata 的容器里，
	// 缺少它会让 time.LoadLocation 失败，分时段判定随之整体错位。
	_ "time/tzdata"

	"github.com/atopos31/llmio/models"
)

// ---------------------------------------------------------------------------
// 时段解析（纯函数，无 IO）
// ---------------------------------------------------------------------------

// minutesPerDay 一天的总分钟数。
const minutesPerDay = 24 * 60

// ParseClock 把 "HH:MM" 解析为一天中的分钟数。
//
// 接受 "24:00" 并返回 1440，便于把一整天写成 "00:00"-"24:00"。
func ParseClock(s string) (int, error) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid clock %q: expected HH:MM", s)
	}
	h, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, fmt.Errorf("invalid hour in %q", s)
	}
	m, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, fmt.Errorf("invalid minute in %q", s)
	}
	if h < 0 || h > 24 || m < 0 || m > 59 {
		return 0, fmt.Errorf("clock out of range: %q", s)
	}
	if h == 24 && m != 0 {
		return 0, fmt.Errorf("24:%02d is not a valid clock", m)
	}
	return h*60 + m, nil
}

// InClockWindow 判断某个时刻是否落在 [start, end) 窗口内。
//
// start == end 视为覆盖全天；start > end 视为跨零点（如 22:00-06:00）。
func InClockWindow(minutes, start, end int) bool {
	if start == end {
		return true
	}
	if start < end {
		return minutes >= start && minutes < end
	}
	// 跨零点
	return minutes >= start || minutes < end
}

// IsWorkday 判断某个日期是否为工作日。
//
// 优先级：按日期的显式覆盖 > 星期几规则。
// 覆盖必须优先，否则"周六调休上班"这类情况无法表达。
//
// 入参应已转换到配置时区。
func (r PeakResolver) IsWorkday(t time.Time) bool {
	if override, ok := r.cal.DateOverrides[t.Format("2006-01-02")]; ok {
		switch override {
		case models.DateOverrideWork:
			return true
		case models.DateOverrideRest:
			return false
		}
		// 无法识别的取值不阻断判定，回落到星期几规则
	}
	weekdays := r.cal.Weekdays
	if len(weekdays) == 0 {
		weekdays = []int{1, 2, 3, 4, 5}
	}
	wd := int(t.Weekday()) // 0=周日
	for _, d := range weekdays {
		if d == wd {
			return true
		}
	}
	return false
}

// ResolvePeriod 返回指定时刻命中的时段。未命中任何时段时返回 nil。
//
// 按配置顺序首个命中者胜出，因此 Periods 的顺序即优先级。
func (r PeakResolver) ResolvePeriod(t time.Time) *models.PeakPeriod {
	if len(r.terms.Periods) == 0 {
		return nil
	}
	local := r.cal.EffectiveTime(t)
	minutes := local.Hour()*60 + local.Minute()
	wd := int(local.Weekday())
	workday := r.IsWorkday(local)

	for i := range r.terms.Periods {
		p := &r.terms.Periods[i]

		if len(p.Days) > 0 && !containsInt(p.Days, wd) {
			continue
		}
		if p.Workday != nil && *p.Workday != workday {
			continue
		}
		start, err := ParseClock(p.Start)
		if err != nil {
			// 配置有误时跳过该时段而非整体失败：一个写坏的时段
			// 不应让所有请求的成本计算都停摆。
			continue
		}
		end, err := ParseClock(p.End)
		if err != nil {
			continue
		}
		if InClockWindow(minutes, start, end) {
			return p
		}
	}
	return nil
}

// ResolveMultiplier 返回指定时刻适用的价格乘数与命中的时段名。
// 未命中任何时段时返回 1 与空名（即基础价）。
func (r PeakResolver) ResolveMultiplier(t time.Time) (float64, string) {
	if !r.terms.Enabled {
		return 1, ""
	}
	p := r.ResolvePeriod(t)
	if p == nil {
		return 1, ""
	}
	return p.Multiplier, p.Name
}

// PeakResolver 在条款与日历之上封装判定逻辑，避免每次判定都重复解析时区。
//
// 两份入参而不是一份：条款按上游走（哪几个小时算峰时），日历全局共用
// （这一天是不是工作日）。判定要同时用到两者，但它们的归属不同。
type PeakResolver struct {
	terms models.PeakTerms
	cal   models.PeakCalendar
}

// NewPeakResolver 构造判定器。
func NewPeakResolver(terms models.PeakTerms, cal models.PeakCalendar) PeakResolver {
	return PeakResolver{terms: terms, cal: cal}
}

// Terms 返回底层条款。
func (r PeakResolver) Terms() models.PeakTerms { return r.terms }

// Calendar 返回底层日历。
func (r PeakResolver) Calendar() models.PeakCalendar { return r.cal }

func containsInt(hay []int, needle int) bool {
	for _, v := range hay {
		if v == needle {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 时间轴预览
// ---------------------------------------------------------------------------

// SchedulePoint 是时间轴上的一段（同一时段连续区间的合并结果）。
type SchedulePoint struct {
	Start      int64   `json:"start"`      // Unix 毫秒
	End        int64   `json:"end"`        // Unix 毫秒
	Period     string  `json:"period"`     // 时段名，空表示基础价
	Multiplier float64 `json:"multiplier"` // 该段的乘数
	Workday    bool    `json:"workday"`    // 该段起始日是否为工作日
}

// PreviewSchedule 从 start 起按分钟步进采样 days 天，把相邻同结果的采样
// 合并为连续区间返回。
//
// 合并而非逐分钟返回：一个月逐分钟是 4 万多条，前端画不出来也读不懂；
// 合并成区间后通常只剩十几段，跨零点与调休造成的错位一眼可见。
func PreviewSchedule(r PeakResolver, start time.Time, days int) []SchedulePoint {
	if days < 1 {
		days = 1
	}
	const step = time.Minute

	local := start.Truncate(step)
	end := local.AddDate(0, 0, days)

	out := make([]SchedulePoint, 0, 16)
	var cur *SchedulePoint

	for t := local; t.Before(end); t = t.Add(step) {
		multiplier, period := r.ResolveMultiplier(t)
		localT := r.cal.EffectiveTime(t)
		workday := r.IsWorkday(localT)
		ms := t.UnixMilli()

		if cur != nil && cur.Period == period && cur.Multiplier == multiplier && cur.Workday == workday {
			cur.End = ms + step.Milliseconds()
			continue
		}
		if cur != nil {
			out = append(out, *cur)
		}
		cur = &SchedulePoint{
			Start:      ms,
			End:        ms + step.Milliseconds(),
			Period:     period,
			Multiplier: multiplier,
			Workday:    workday,
		}
	}
	if cur != nil {
		out = append(out, *cur)
	}
	return out
}

// ---------------------------------------------------------------------------
// 日历读取（带缓存）
// ---------------------------------------------------------------------------

// peakCacheTTL 日历缓存有效期。
//
// 分时段判定在每次代理请求的热路径上，不能每次都读库；但改配置后
// 也不应等到重启才生效，因此用短 TTL 而非永久缓存。
const peakCacheTTL = 30 * time.Second

var (
	peakMu       sync.RWMutex
	peakCached   models.PeakCalendar
	peakCachedAt time.Time
	peakLoaded   bool
)

// GetPeakCalendar 读取全局工作日日历，带短 TTL 缓存。
// 读取失败或配置为空时返回默认日历，保证热路径不会因配置问题中断。
func GetPeakCalendar(ctx context.Context) models.PeakCalendar {
	peakMu.RLock()
	if peakLoaded && time.Since(peakCachedAt) < peakCacheTTL {
		cfg := peakCached
		peakMu.RUnlock()
		return cfg
	}
	peakMu.RUnlock()

	cfg := loadPeakCalendar(ctx)

	peakMu.Lock()
	peakCached = cfg
	peakCachedAt = time.Now()
	peakLoaded = true
	peakMu.Unlock()
	return cfg
}

func loadPeakCalendar(ctx context.Context) models.PeakCalendar {
	var row models.Config
	err := models.DB.WithContext(ctx).Where("key = ?", models.KeyPeakCalendar).First(&row).Error
	if err != nil {
		// 未配置或查询失败：回落到默认（东八区、周一至周五）
		return models.DefaultPeakCalendar()
	}
	if strings.TrimSpace(row.Value) == "" {
		return models.DefaultPeakCalendar()
	}
	var cfg models.PeakCalendar
	if err := json.Unmarshal([]byte(row.Value), &cfg); err != nil {
		// 日历损坏时回落到默认而非报错：计费配置不该让代理请求失败。
		return models.DefaultPeakCalendar()
	}
	if cfg.DateOverrides == nil {
		cfg.DateOverrides = map[string]string{}
	}
	return cfg
}

// InvalidatePeakCalendar 使日历缓存失效，供配置写入后调用。
func InvalidatePeakCalendar() {
	peakMu.Lock()
	peakLoaded = false
	peakCachedAt = time.Time{}
	peakMu.Unlock()
}

// ApplyPeakMultiplier 把乘数应用到三档单价上。
//
// 这是价格进入 ChatLog 快照的唯一入口：日志记录的是**实际生效价**，
// 因此历史成本不会被后来的配置修改改写，Cost() 也无需感知时段概念。
func ApplyPeakMultiplier(inputPrice, cacheReadPrice, outputPrice, multiplier float64) (float64, float64, float64) {
	if multiplier == 1 {
		return inputPrice, cacheReadPrice, outputPrice
	}
	return inputPrice * multiplier, cacheReadPrice * multiplier, outputPrice * multiplier
}

// ResolvePricingFor 是热路径入口：为指定上游的条款解析出此刻的生效单价与时段名。
//
// terms 为 nil（该关联没配峰谷）或未启用时直接返回原价，不产生额外开销。
// 日历从全局取——条款按上游，日历是事实。
func ResolvePricingFor(ctx context.Context, at time.Time, terms *models.PeakTerms, inputPrice, cacheReadPrice, outputPrice float64) (float64, float64, float64, string) {
	if terms == nil || !terms.Enabled {
		return inputPrice, cacheReadPrice, outputPrice, ""
	}
	multiplier, period := NewPeakResolver(*terms, GetPeakCalendar(ctx)).ResolveMultiplier(at)
	i, c, o := ApplyPeakMultiplier(inputPrice, cacheReadPrice, outputPrice, multiplier)
	return i, c, o, period
}

// ---------------------------------------------------------------------------
// 节假日数据同步
// ---------------------------------------------------------------------------

// SyncHolidays 拉取指定年份节假日并合并进日历的 DateOverrides。
//
// fetch 为 nil 时使用 DefaultHolidayFetcher（远端优先、失败回落到内置数据）。
//
// 合并语义：同一年份的既有覆盖会被**整体替换**，其他年份的保持不动。
// 整体替换而非逐条合并，是为了让重复同步幂等——否则已撤销的调休
// 会以陈旧条目的形式残留下来。
func SyncHolidays(ctx context.Context, year int, fetch HolidayFetcher) (models.PeakCalendar, int, error) {
	if fetch == nil {
		fetch = DefaultHolidayFetcher
	}
	days, source, err := fetch(ctx, year)
	if err != nil {
		return models.PeakCalendar{}, 0, err
	}
	overrides := models.ToDateOverrides(days)
	if len(overrides) == 0 {
		return models.PeakCalendar{}, 0, fmt.Errorf("holiday source returned no usable dates for %d", year)
	}

	cfg := GetPeakCalendar(ctx)

	// 先清掉该年份的旧覆盖，再写入新的，实现"按年替换"
	prefix := fmt.Sprintf("%04d-", year)
	merged := make(map[string]string, len(cfg.DateOverrides)+len(overrides))
	for date, v := range cfg.DateOverrides {
		if strings.HasPrefix(date, prefix) {
			continue
		}
		merged[date] = v
	}
	for date, v := range overrides {
		merged[date] = v
	}
	cfg.DateOverrides = merged
	cfg.HolidaySyncedAt = time.Now().Unix()
	cfg.HolidaySource = source

	if err := SavePeakCalendar(ctx, cfg); err != nil {
		return models.PeakCalendar{}, 0, err
	}
	return cfg, len(overrides), nil
}

// SavePeakCalendar 持久化全局日历并使其缓存失效。
func SavePeakCalendar(ctx context.Context, cfg models.PeakCalendar) error {
	if cfg.DateOverrides == nil {
		cfg.DateOverrides = map[string]string{}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := models.SaveConfigValue(ctx, models.KeyPeakCalendar, string(raw)); err != nil {
		return err
	}

	InvalidatePeakCalendar()
	return nil
}
