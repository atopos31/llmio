package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atopos31/llmio/models"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// 测试辅助
// ---------------------------------------------------------------------------

func setupPeakDB(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "peak.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.Config{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	prev := models.DB
	models.DB = db
	InvalidatePeakCalendar()
	t.Cleanup(func() {
		models.DB = prev
		InvalidatePeakCalendar()
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
}

// sh 构造指定时区的时间，便于断言时不受运行环境时区影响。
func sh(t *testing.T, y int, m time.Month, d, hh, mm int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load tz: %v", err)
	}
	return time.Date(y, m, d, hh, mm, 0, 0, loc)
}

// peakFixture 是判定逻辑用例的取材便利结构：把条款（时段）与日历
// （时区/工作日/日期覆盖）写在一个字面量里，由 terms() / calendar() 拆开。
//
// 这不是线上类型，只用在这里——判定逻辑的用例大都要同时摆好几项，
// 分开写两遍会让"这条用例到底在验什么"淹没在字段堆里。
// **归属本身**（时段归条款、工作日归日历）由 TestPeakTermsAndCalendarAreSeparate
// 用真实的两个类型钉住，避免这里拆错了却没人发现。
type peakFixture struct {
	Enabled       bool
	Timezone      string
	Weekdays      []int
	Periods       []models.PeakPeriod
	DateOverrides map[string]string
}

func (f peakFixture) terms() models.PeakTerms {
	return models.PeakTerms{Enabled: f.Enabled, Periods: f.Periods}
}

func (f peakFixture) calendar() models.PeakCalendar {
	return models.PeakCalendar{
		Timezone:      f.Timezone,
		Weekdays:      f.Weekdays,
		DateOverrides: f.DateOverrides,
	}
}

// resolveWith 用一份 fixture 造判定器。
func resolveWith(f peakFixture) PeakResolver {
	return NewPeakResolver(f.terms(), f.calendar())
}

// ---------------------------------------------------------------------------
// 时钟解析
// ---------------------------------------------------------------------------

func TestParseClock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    int
		wantErr bool
	}{
		{name: "零点", in: "00:00", want: 0},
		{name: "常规", in: "08:30", want: 8*60 + 30},
		{name: "带前导零", in: "09:05", want: 9*60 + 5},
		{name: "无前导零", in: "9:5", want: 9*60 + 5},
		{name: "23:59", in: "23:59", want: 23*60 + 59},
		{name: "24:00 表示一天结束", in: "24:00", want: 1440},
		{name: "前后空白", in: "  08:30  ", want: 8*60 + 30},
		{name: "冒号两侧空白", in: " 08 : 30 ", want: 8*60 + 30},

		{name: "小时越界", in: "25:00", wantErr: true},
		{name: "分钟越界", in: "08:60", wantErr: true},
		{name: "负数", in: "-1:00", wantErr: true},
		{name: "24:01 非法", in: "24:01", wantErr: true},
		{name: "缺少分钟", in: "08", wantErr: true},
		{name: "缺少小时", in: ":30", wantErr: true},
		{name: "非数字", in: "ab:cd", wantErr: true},
		{name: "多余冒号", in: "08:30:00", wantErr: true},
		{name: "空串", in: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseClock(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("want %d, got %d", tc.want, got)
			}
		})
	}
}

func TestInClockWindow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		minutes    int
		start, end int
		want       bool
	}{
		// 普通窗口 [start, end)
		{name: "窗口内", minutes: 600, start: 540, end: 660, want: true},
		{name: "等于起点含在内", minutes: 540, start: 540, end: 660, want: true},
		{name: "等于终点不含", minutes: 660, start: 540, end: 660, want: false},
		{name: "起点之前", minutes: 539, start: 540, end: 660, want: false},
		{name: "终点之后", minutes: 661, start: 540, end: 660, want: false},

		// start == end 视为全天
		{name: "起止相同视为全天-零点", minutes: 0, start: 0, end: 0, want: true},
		{name: "起止相同视为全天-正午", minutes: 720, start: 0, end: 0, want: true},

		// 跨零点（start > end）
		{name: "跨零点-起点之后", minutes: 23 * 60, start: 22 * 60, end: 6 * 60, want: true},
		{name: "跨零点-终点之前", minutes: 5 * 60, start: 22 * 60, end: 6 * 60, want: true},
		{name: "跨零点-恰在起点", minutes: 22 * 60, start: 22 * 60, end: 6 * 60, want: true},
		{name: "跨零点-恰在终点不含", minutes: 6 * 60, start: 22 * 60, end: 6 * 60, want: false},
		{name: "跨零点-窗口外", minutes: 12 * 60, start: 22 * 60, end: 6 * 60, want: false},

		// 夜间优惠的实际形态 00:30-08:30
		{name: "00:30-08:30 内", minutes: 3 * 60, start: 30, end: 8*60 + 30, want: true},
		{name: "00:30-08:30 外", minutes: 12 * 60, start: 30, end: 8*60 + 30, want: false},
		{name: "00:30 之前", minutes: 10, start: 30, end: 8*60 + 30, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := InClockWindow(tc.minutes, tc.start, tc.end); got != tc.want {
				t.Fatalf("want %v, got %v", tc.want, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 工作日判定
// ---------------------------------------------------------------------------

func TestIsWorkdayWeekdayRule(t *testing.T) {
	t.Parallel()

	cfg := peakFixture{Weekdays: []int{1, 2, 3, 4, 5}}
	r := resolveWith(cfg)

	// 2026-09-28 是周一，2026-10-03 是周六
	got := map[string]bool{
		"周一": r.IsWorkday(sh(t, 2026, 9, 28, 12, 0)),
		"周六": r.IsWorkday(sh(t, 2026, 10, 3, 12, 0)),
		"周日": r.IsWorkday(sh(t, 2026, 10, 4, 12, 0)),
	}
	if !got["周一"] {
		t.Fatal("周一应为工作日")
	}
	if got["周六"] || got["周日"] {
		t.Fatal("周六周日应为休息日")
	}
}

func TestIsWorkdayDefaultWeekdaysWhenEmpty(t *testing.T) {
	t.Parallel()

	// Weekdays 为空时应默认周一至周五
	r := resolveWith(peakFixture{})
	if !r.IsWorkday(sh(t, 2026, 9, 28, 12, 0)) {
		t.Fatal("空 Weekdays 时周一应为工作日")
	}
	if r.IsWorkday(sh(t, 2026, 10, 3, 12, 0)) {
		t.Fatal("空 Weekdays 时周六应为休息日")
	}
}

func TestIsWorkdayDateOverrideWins(t *testing.T) {
	t.Parallel()

	cfg := peakFixture{
		Weekdays: []int{1, 2, 3, 4, 5},
		DateOverrides: map[string]string{
			// 国庆放假：周五变休息日
			"2026-10-02": models.DateOverrideRest,
			// 调休上班：周六变工作日
			"2026-10-10": models.DateOverrideWork,
		},
	}
	r := resolveWith(cfg)

	if r.IsWorkday(sh(t, 2026, 10, 2, 12, 0)) {
		t.Fatal("被标记为休息的周五应判为休息日")
	}
	if !r.IsWorkday(sh(t, 2026, 10, 10, 12, 0)) {
		t.Fatal("被标记为工作的周六应判为工作日（调休）")
	}
}

func TestIsWorkdayUnknownOverrideFallsBack(t *testing.T) {
	t.Parallel()

	// 无法识别的取值不应阻断判定，应回落到星期几规则
	cfg := peakFixture{
		Weekdays:      []int{1, 2, 3, 4, 5},
		DateOverrides: map[string]string{"2026-09-28": "nonsense"},
	}
	r := resolveWith(cfg)
	if !r.IsWorkday(sh(t, 2026, 9, 28, 12, 0)) {
		t.Fatal("无法识别的覆盖值应回落到星期几规则（周一=工作日）")
	}
}

func TestIsWorkdayCustomWeekdays(t *testing.T) {
	t.Parallel()

	// 自定义：只有周二算工作日（如某些地区周末不同）
	r := resolveWith(peakFixture{Weekdays: []int{2}})
	if !r.IsWorkday(sh(t, 2026, 9, 29, 12, 0)) {
		t.Fatal("周二应为工作日")
	}
	if r.IsWorkday(sh(t, 2026, 9, 28, 12, 0)) {
		t.Fatal("周一应为休息日")
	}
}

// ---------------------------------------------------------------------------
// 时段解析
// ---------------------------------------------------------------------------

func TestResolvePeriodFirstMatchWins(t *testing.T) {
	t.Parallel()

	wt := true
	// 顺序即优先级：先特例（工作日午休高价）后一般（工作日标准）
	cfg := peakFixture{
		Enabled:  true,
		Weekdays: []int{1, 2, 3, 4, 5},
		Periods: []models.PeakPeriod{
			{Name: "尖峰", Start: "12:00", End: "14:00", Multiplier: 2, Workday: &wt},
			{Name: "标准", Start: "00:00", End: "24:00", Multiplier: 1},
		},
	}
	r := resolveWith(cfg)

	// 周一午间 → 尖峰
	p := r.ResolvePeriod(sh(t, 2026, 9, 28, 13, 0))
	if p == nil || p.Name != "尖峰" || p.Multiplier != 2 {
		t.Fatalf("应命中尖峰，实得 %+v", p)
	}
	// 周一其他时间 → 标准
	p = r.ResolvePeriod(sh(t, 2026, 9, 28, 9, 0))
	if p == nil || p.Name != "标准" {
		t.Fatalf("应命中标准，实得 %+v", p)
	}
}

func TestResolvePeriodCrossMidnight(t *testing.T) {
	t.Parallel()

	cfg := peakFixture{
		Enabled: true,
		Periods: []models.PeakPeriod{
			{Name: "夜间", Start: "00:30", End: "08:30", Multiplier: 0.25},
		},
	}
	r := resolveWith(cfg)

	// 02:00 命中
	if p := r.ResolvePeriod(sh(t, 2026, 9, 28, 2, 0)); p == nil || p.Name != "夜间" {
		t.Fatalf("02:00 应命中夜间，实得 %+v", p)
	}
	// 12:00 不命中
	if p := r.ResolvePeriod(sh(t, 2026, 9, 28, 12, 0)); p != nil {
		t.Fatalf("12:00 不应命中，实得 %+v", p)
	}
}

func TestResolvePeriodDaysFilter(t *testing.T) {
	t.Parallel()

	// 仅周末的优惠
	cfg := peakFixture{
		Enabled: true,
		Periods: []models.PeakPeriod{
			{Name: "周末优惠", Start: "00:00", End: "24:00", Multiplier: 0.5, Days: []int{0, 6}},
		},
	}
	r := resolveWith(cfg)

	if p := r.ResolvePeriod(sh(t, 2026, 10, 3, 12, 0)); p == nil { // 周六
		t.Fatal("周六应命中周末优惠")
	}
	if p := r.ResolvePeriod(sh(t, 2026, 9, 28, 12, 0)); p != nil { // 周一
		t.Fatalf("周一不应命中，实得 %+v", p)
	}
}

func TestResolvePeriodWorkdayFilter(t *testing.T) {
	t.Parallel()

	wt := true
	wf := false
	cfg := peakFixture{
		Enabled:  true,
		Weekdays: []int{1, 2, 3, 4, 5},
		Periods: []models.PeakPeriod{
			{Name: "工作日价", Start: "00:00", End: "24:00", Multiplier: 1, Workday: &wt},
			{Name: "休息日价", Start: "00:00", End: "24:00", Multiplier: 0.5, Workday: &wf},
		},
	}
	r := resolveWith(cfg)

	if p := r.ResolvePeriod(sh(t, 2026, 9, 28, 12, 0)); p == nil || p.Name != "工作日价" {
		t.Fatalf("周一点工作日价，实得 %+v", p)
	}
	if p := r.ResolvePeriod(sh(t, 2026, 10, 3, 12, 0)); p == nil || p.Name != "休息日价" {
		t.Fatalf("周六应命中休息日价，实得 %+v", p)
	}
}

func TestResolvePeriodInvalidClockSkipped(t *testing.T) {
	t.Parallel()

	// 一个写坏的时段不应让所有判定失败，而应被跳过
	cfg := peakFixture{
		Enabled: true,
		Periods: []models.PeakPeriod{
			{Name: "坏时段", Start: "bogus", End: "24:00", Multiplier: 9},
			{Name: "好东西", Start: "00:00", End: "24:00", Multiplier: 1.5},
		},
	}
	r := resolveWith(cfg)

	p := r.ResolvePeriod(sh(t, 2026, 9, 28, 12, 0))
	if p == nil || p.Name != "好东西" {
		t.Fatalf("应跳过坏时段命中后续时段，实得 %+v", p)
	}
}

func TestResolvePeriodNoPeriods(t *testing.T) {
	t.Parallel()

	r := resolveWith(peakFixture{Enabled: true})
	if p := r.ResolvePeriod(sh(t, 2026, 9, 28, 12, 0)); p != nil {
		t.Fatalf("无时段配置时应返回 nil，实得 %+v", p)
	}
}

func TestResolveMultiplier(t *testing.T) {
	t.Parallel()

	cfg := peakFixture{
		Enabled: true,
		Periods: []models.PeakPeriod{
			{Name: "夜间", Start: "00:30", End: "08:30", Multiplier: 0.25},
		},
	}

	t.Run("启用且命中", func(t *testing.T) {
		m, name := resolveWith(cfg).ResolveMultiplier(sh(t, 2026, 9, 28, 2, 0))
		if m != 0.25 || name != "夜间" {
			t.Fatalf("want 0.25/夜间, got %v/%q", m, name)
		}
	})

	t.Run("启用但未命中回落基础价", func(t *testing.T) {
		m, name := resolveWith(cfg).ResolveMultiplier(sh(t, 2026, 9, 28, 12, 0))
		if m != 1 || name != "" {
			t.Fatalf("want 1/空, got %v/%q", m, name)
		}
	})

	t.Run("未启用时恒为基础价", func(t *testing.T) {
		off := cfg
		off.Enabled = false
		m, name := resolveWith(off).ResolveMultiplier(sh(t, 2026, 9, 28, 2, 0))
		if m != 1 || name != "" {
			t.Fatalf("want 1/空, got %v/%q", m, name)
		}
	})
}

func TestResolveMultiplierTimezoneIsRespected(t *testing.T) {
	t.Parallel()

	// 同一绝对时刻，在 Asia/Shanghai 是 02:00（命中夜间），在 UTC 是 18:00（不命中）。
	// 时区若被忽略，这个测试会失败——这正是它要守住的东西。
	at := time.Date(2026, 9, 27, 18, 0, 0, 0, time.UTC) // 上海次日 02:00

	cfgSH := peakFixture{
		Enabled:  true,
		Timezone: "Asia/Shanghai",
		Periods:  []models.PeakPeriod{{Name: "夜间", Start: "00:30", End: "08:30", Multiplier: 0.25}},
	}
	if m, _ := resolveWith(cfgSH).ResolveMultiplier(at); m != 0.25 {
		t.Fatalf("按上海时区应命中夜间，实得乘数 %v", m)
	}

	cfgUTC := cfgSH
	cfgUTC.Timezone = "UTC"
	if m, _ := resolveWith(cfgUTC).ResolveMultiplier(at); m != 1 {
		t.Fatalf("按 UTC 不应命中，实得乘数 %v", m)
	}
}

func TestResolveMultiplierUnknownTimezoneFallsBack(t *testing.T) {
	t.Parallel()

	// 时区无法解析时应退回原时间，而不是失败
	cfg := peakFixture{
		Enabled:  true,
		Timezone: "Not/AZone",
		Periods:  []models.PeakPeriod{{Name: "全天", Start: "00:00", End: "24:00", Multiplier: 2}},
	}
	if m, name := resolveWith(cfg).ResolveMultiplier(sh(t, 2026, 9, 28, 12, 0)); m != 2 || name != "全天" {
		t.Fatalf("非法时区不应阻断判定，实得 %v/%q", m, name)
	}
}

func TestResolverReturnsBothHalves(t *testing.T) {
	t.Parallel()

	// 判定器同时持有条款与日历，两个取值器各自原样返回自己那一半——
	// 别让"取条款"顺手把日历也塞进去，那样调用方就分不清哪份是哪份了。
	terms := models.PeakTerms{Enabled: true, Periods: []models.PeakPeriod{
		{Name: "夜间", Start: "00:00", End: "08:00", Multiplier: 0.25},
	}}
	cal := models.PeakCalendar{Timezone: "UTC", Weekdays: []int{6}}
	r := NewPeakResolver(terms, cal)

	if got := r.Terms(); !got.Enabled || len(got.Periods) != 1 || got.Periods[0].Name != "夜间" {
		t.Fatalf("Terms() 应返回条款，实得 %+v", got)
	}
	if got := r.Calendar(); got.Timezone != "UTC" || len(got.Weekdays) != 1 || got.Weekdays[0] != 6 {
		t.Fatalf("Calendar() 应返回日历，实得 %+v", got)
	}
}

// ---------------------------------------------------------------------------
// 乘数应用
// ---------------------------------------------------------------------------

func TestApplyPeakMultiplier(t *testing.T) {
	t.Parallel()

	t.Run("乘数为 1 时原值返回", func(t *testing.T) {
		i, c, o := ApplyPeakMultiplier(1, 2, 3, 1)
		if i != 1 || c != 2 || o != 3 {
			t.Fatalf("want 1,2,3 got %v,%v,%v", i, c, o)
		}
	})

	t.Run("打折", func(t *testing.T) {
		i, c, o := ApplyPeakMultiplier(4, 2, 8, 0.25)
		if i != 1 || c != 0.5 || o != 2 {
			t.Fatalf("want 1,0.5,2 got %v,%v,%v", i, c, o)
		}
	})

	t.Run("加价", func(t *testing.T) {
		i, c, o := ApplyPeakMultiplier(2, 3, 4, 2)
		if i != 4 || c != 6 || o != 8 {
			t.Fatalf("want 4,6,8 got %v,%v,%v", i, c, o)
		}
	})

	t.Run("乘数为 0 时归零", func(t *testing.T) {
		i, c, o := ApplyPeakMultiplier(5, 5, 5, 0)
		if i != 0 || c != 0 || o != 0 {
			t.Fatalf("want all 0 got %v,%v,%v", i, c, o)
		}
	})
}

// ---------------------------------------------------------------------------
// 日历读取与持久化
// ---------------------------------------------------------------------------

func TestGetPeakCalendarDefaultsWhenAbsent(t *testing.T) {
	setupPeakDB(t)

	cfg := GetPeakCalendar(context.Background())
	if cfg.Timezone != "Asia/Shanghai" {
		t.Fatalf("默认时区不符：%q", cfg.Timezone)
	}
	if len(cfg.Weekdays) == 0 {
		t.Fatal("默认应给出工作日定义（周一至周五）")
	}
	if cfg.DateOverrides == nil {
		t.Fatal("DateOverrides 应为非 nil，避免调用方需做空值判断")
	}
}

func TestGetPeakCalendarCorruptConfigFallsBack(t *testing.T) {
	setupPeakDB(t)

	if err := models.DB.Create(&models.Config{Key: models.KeyPeakCalendar, Value: "{not json"}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	InvalidatePeakCalendar()

	cfg := GetPeakCalendar(context.Background())
	if cfg.Timezone != "Asia/Shanghai" {
		t.Fatalf("配置损坏时应回落到默认日历，而不是让热路径出错，实得 %+v", cfg)
	}
}

func TestGetPeakCalendarEmptyValueFallsBack(t *testing.T) {
	setupPeakDB(t)

	if err := models.DB.Create(&models.Config{Key: models.KeyPeakCalendar, Value: "   "}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	InvalidatePeakCalendar()

	if cfg := GetPeakCalendar(context.Background()); cfg.Timezone != "Asia/Shanghai" {
		t.Fatalf("空值应回落默认，实得 %q", cfg.Timezone)
	}
}

func TestSavePeakCalendarCreatesThenUpdates(t *testing.T) {
	setupPeakDB(t)
	ctx := context.Background()

	cfg := models.DefaultPeakCalendar()
	cfg.Timezone = "UTC"
	cfg.Weekdays = []int{1, 2, 3, 4, 5, 6}

	if err := SavePeakCalendar(ctx, cfg); err != nil {
		t.Fatalf("create: %v", err)
	}
	got := GetPeakCalendar(ctx)
	if got.Timezone != "UTC" || len(got.Weekdays) != 6 {
		t.Fatalf("首次保存后读取不符：%+v", got)
	}

	// 再次保存走更新分支
	cfg.Timezone = "Asia/Tokyo"
	if err := SavePeakCalendar(ctx, cfg); err != nil {
		t.Fatalf("update: %v", err)
	}
	got = GetPeakCalendar(ctx)
	if got.Timezone != "Asia/Tokyo" {
		t.Fatalf("更新后读取不符：%+v", got)
	}

	// 只应有一行配置
	var count int64
	if err := models.DB.Model(&models.Config{}).Where("key = ?", models.KeyPeakCalendar).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("配置应只有一行，实得 %d", count)
	}
}

func TestSavePeakCalendarNormalizesNilOverrides(t *testing.T) {
	setupPeakDB(t)

	cfg := models.PeakCalendar{Timezone: "Asia/Shanghai"}
	if err := SavePeakCalendar(context.Background(), cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	// 落库后应是 {} 而非 null，便于前端直接做 map 操作
	var row models.Config
	if err := models.DB.Where("key = ?", models.KeyPeakCalendar).First(&row).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(row.Value), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ov, ok := decoded["dateOverrides"]; !ok || ov == nil {
		t.Fatalf("dateOverrides 应落库为 {}，实得 %v", decoded["dateOverrides"])
	}
}

func TestInvalidatePeakCalendarForcesReload(t *testing.T) {
	setupPeakDB(t)
	ctx := context.Background()

	if cfg := GetPeakCalendar(ctx); cfg.Timezone != "Asia/Shanghai" {
		t.Fatal("初始应为默认时区")
	}

	// 绕过 Save（不触发失效）直接改库，模拟外部改动
	cfg := models.DefaultPeakCalendar()
	cfg.Timezone = "UTC"
	raw, _ := json.Marshal(cfg)
	if err := models.DB.Create(&models.Config{Key: models.KeyPeakCalendar, Value: string(raw)}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 仍在 TTL 内，读到的还是旧值
	if GetPeakCalendar(ctx).Timezone != "Asia/Shanghai" {
		t.Fatal("TTL 内应命中缓存")
	}

	InvalidatePeakCalendar()
	if GetPeakCalendar(ctx).Timezone != "UTC" {
		t.Fatal("失效后应读到新值")
	}
}

func TestResolvePricingForDisabledIsZeroOverhead(t *testing.T) {
	setupPeakDB(t)

	// 没配峰谷（nil）与配了但没启用，都必须原样返回，且**不读库**：
	// 热路径上绝大多数关联都是这两种状态，多一次日历查询就是白花。
	i, c, o, period := ResolvePricingFor(context.Background(), time.Now(), nil, 1, 2, 3)
	if i != 1 || c != 2 || o != 3 || period != "" {
		t.Fatalf("nil 条款应原样返回，实得 %v,%v,%v,%q", i, c, o, period)
	}

	off := models.PeakTerms{Enabled: false, Periods: []models.PeakPeriod{
		{Name: "夜间", Start: "00:00", End: "24:00", Multiplier: 0.25},
	}}
	i, c, o, period = ResolvePricingFor(context.Background(), time.Now(), &off, 1, 2, 3)
	if i != 1 || c != 2 || o != 3 || period != "" {
		t.Fatalf("未启用的条款应原样返回，实得 %v,%v,%v,%q", i, c, o, period)
	}
}

func TestResolvePricingForEnabled(t *testing.T) {
	setupPeakDB(t)
	ctx := context.Background()

	if err := SavePeakCalendar(ctx, models.DefaultPeakCalendar()); err != nil {
		t.Fatalf("save calendar: %v", err)
	}
	terms := models.PeakTerms{
		Enabled: true,
		Periods: []models.PeakPeriod{
			{Name: "夜间", Start: "00:00", End: "24:00", Multiplier: 0.25},
		},
	}

	at := sh(t, 2026, 9, 28, 12, 0)
	i, c, o, period := ResolvePricingFor(ctx, at, &terms, 4, 2, 8)
	if i != 1 || c != 0.5 || o != 2 {
		t.Fatalf("单价未按乘数折算：%v,%v,%v", i, c, o)
	}
	if period != "夜间" {
		t.Fatalf("时段名应为夜间，实得 %q", period)
	}
}

// 条款与日历的归属是这次改造的核心，单独钉一遍：
// 时段（哪几个小时算峰时）按上游走，工作日/时区全局共用。
func TestPeakTermsAndCalendarAreSeparate(t *testing.T) {
	setupPeakDB(t)
	ctx := context.Background()

	// 同一份日历，两份条款 → 结果必须不同：说明乘数取自条款
	if err := SavePeakCalendar(ctx, models.PeakCalendar{Timezone: "Asia/Shanghai"}); err != nil {
		t.Fatalf("save calendar: %v", err)
	}
	at := sh(t, 2026, 9, 28, 12, 0)
	half := models.PeakTerms{Enabled: true, Periods: []models.PeakPeriod{
		{Name: "半价", Start: "00:00", End: "24:00", Multiplier: 0.5},
	}}
	double := models.PeakTerms{Enabled: true, Periods: []models.PeakPeriod{
		{Name: "双倍", Start: "00:00", End: "24:00", Multiplier: 2},
	}}
	if _, _, o, name := ResolvePricingFor(ctx, at, &half, 4, 2, 8); o != 4 || name != "半价" {
		t.Fatalf("半价条款应得 4，实得 %v（%s）", o, name)
	}
	if _, _, o, name := ResolvePricingFor(ctx, at, &double, 4, 2, 8); o != 16 || name != "双倍" {
		t.Fatalf("双倍条款应得 16，实得 %v（%s）", o, name)
	}

	// 同一份条款，两份日历 → 结果必须不同：说明工作日取自全局日历
	rest := models.PeakTerms{Enabled: true, Periods: []models.PeakPeriod{
		{Name: "仅休息日", Start: "00:00", End: "24:00", Multiplier: 0.5, Workday: boolPtr(false)},
	}}
	if _, _, _, name := ResolvePricingFor(ctx, at, &rest, 4, 2, 8); name != "" {
		t.Fatalf("2026-09-28 是周一，休息日条款不该命中，实得 %q", name)
	}
	if err := SavePeakCalendar(ctx, models.PeakCalendar{
		Timezone:      "Asia/Shanghai",
		DateOverrides: map[string]string{"2026-09-28": models.DateOverrideRest},
	}); err != nil {
		t.Fatalf("save calendar: %v", err)
	}
	if _, _, _, name := ResolvePricingFor(ctx, at, &rest, 4, 2, 8); name != "仅休息日" {
		t.Fatalf("日历把该日改成休息日后应命中，实得 %q", name)
	}
}

func boolPtr(b bool) *bool { return &b }

// ---------------------------------------------------------------------------
// 时间轴预览
// ---------------------------------------------------------------------------

func TestPreviewScheduleMergesAdjacent(t *testing.T) {
	t.Parallel()

	cfg := peakFixture{
		Enabled:  true,
		Timezone: "Asia/Shanghai",
		Weekdays: []int{1, 2, 3, 4, 5},
		Periods: []models.PeakPeriod{
			{Name: "夜间", Start: "00:00", End: "08:00", Multiplier: 0.25},
			{Name: "白天", Start: "08:00", End: "24:00", Multiplier: 1},
		},
	}
	r := resolveWith(cfg)

	// 从周一 00:00 起预览一整天
	start := sh(t, 2026, 9, 28, 0, 0)
	points := PreviewSchedule(r, start, 1)

	if len(points) != 2 {
		t.Fatalf("一天两个时段应合并为 2 段，实得 %d：%+v", len(points), points)
	}
	if points[0].Period != "夜间" || points[0].Multiplier != 0.25 {
		t.Fatalf("首段应为夜间：%+v", points[0])
	}
	if points[1].Period != "白天" {
		t.Fatalf("次段应为白天：%+v", points[1])
	}
	// 两段首尾相接，无空隙无重叠
	if points[0].End != points[1].Start {
		t.Fatalf("相邻段应首尾相接：%d vs %d", points[0].End, points[1].Start)
	}
	// 覆盖整整一天
	if got := points[1].End - points[0].Start; got != (24 * time.Hour).Milliseconds() {
		t.Fatalf("应覆盖 24 小时，实得 %d ms", got)
	}
}

func TestPreviewScheduleIncludesWorkdayFlags(t *testing.T) {
	t.Parallel()

	cfg := peakFixture{
		Enabled:  true,
		Timezone: "Asia/Shanghai",
		Weekdays: []int{1, 2, 3, 4, 5},
		Periods:  []models.PeakPeriod{{Name: "全天", Start: "00:00", End: "24:00", Multiplier: 1}},
	}
	r := resolveWith(cfg)

	// 周六起预览 3 天：周六、周日为休息日，周一为工作日。
	// 时段与乘数全程不变，因此切段完全由工作日标记驱动。
	start := sh(t, 2026, 10, 3, 0, 0) // 周六
	points := PreviewSchedule(r, start, 3)

	if len(points) != 2 {
		t.Fatalf("应切为 2 段（周末合并 + 周一），实得 %d：%+v", len(points), points)
	}
	if points[0].Workday {
		t.Fatalf("首段覆盖周末，不应是工作日：%+v", points[0])
	}
	if !points[1].Workday {
		t.Fatalf("次段为周一，应是工作日：%+v", points[1])
	}
	// 首段应覆盖两个整天
	if got := points[0].End - points[0].Start; got != (48 * time.Hour).Milliseconds() {
		t.Fatalf("首段应覆盖 48 小时，实得 %d ms", got)
	}
	if got := points[1].End - points[1].Start; got != (24 * time.Hour).Milliseconds() {
		t.Fatalf("次段应覆盖 24 小时，实得 %d ms", got)
	}
}

func TestPreviewScheduleWorkdayToggleSplits(t *testing.T) {
	t.Parallel()

	// 周日→周一 会切换工作日标记
	cfg := peakFixture{
		Enabled:  true,
		Timezone: "Asia/Shanghai",
		Weekdays: []int{1, 2, 3, 4, 5},
		Periods:  []models.PeakPeriod{{Name: "全天", Start: "00:00", End: "24:00", Multiplier: 1}},
	}
	r := resolveWith(cfg)

	start := sh(t, 2026, 10, 4, 0, 0) // 周日
	points := PreviewSchedule(r, start, 2)
	if len(points) != 2 {
		t.Fatalf("跨周日→周一应切为 2 段，实得 %d：%+v", len(points), points)
	}
	if points[0].Workday {
		t.Fatalf("首段为周日，不应是工作日：%+v", points[0])
	}
	if !points[1].Workday {
		t.Fatalf("次段为周一，应是工作日：%+v", points[1])
	}
}

func TestPreviewScheduleClampsDays(t *testing.T) {
	t.Parallel()

	r := resolveWith(peakFixture{Enabled: true})
	start := sh(t, 2026, 9, 28, 0, 0)

	// days < 1 应钳到 1 天而不是返回空或 panic
	points := PreviewSchedule(r, start, 0)
	if len(points) == 0 {
		t.Fatal("days=0 应钳到 1 天")
	}
	total := points[len(points)-1].End - points[0].Start
	if total != (24 * time.Hour).Milliseconds() {
		t.Fatalf("应覆盖 24 小时，实得 %d ms", total)
	}
}

func TestPreviewScheduleDisabledIsSingleSegment(t *testing.T) {
	t.Parallel()

	cfg := peakFixture{Enabled: false}
	r := resolveWith(cfg)
	points := PreviewSchedule(r, sh(t, 2026, 9, 28, 0, 0), 1)

	// 未启用时全程基础价，但工作日标记仍会切换，因此至少 1 段
	if len(points) == 0 {
		t.Fatal("不应返回空")
	}
	for _, p := range points {
		if p.Multiplier != 1 {
			t.Fatalf("未启用时乘数应恒为 1：%+v", p)
		}
		if p.Period != "" {
			t.Fatalf("未启用时时段名应为空：%+v", p)
		}
	}
}

// ---------------------------------------------------------------------------
// 节假日转换与同步
// ---------------------------------------------------------------------------

func TestToDateOverrides(t *testing.T) {
	t.Parallel()

	days := []models.HolidayDay{
		{Date: "2026-10-01", Name: "国庆节", IsOffDay: true},
		{Date: "2026-10-10", Name: "国庆节后上班", IsOffDay: false},
		{Date: "", Name: "无日期应被跳过", IsOffDay: true},
	}
	got := models.ToDateOverrides(days)

	if len(got) != 2 {
		t.Fatalf("应产出 2 条（跳过空日期），实得 %d：%v", len(got), got)
	}
	if got["2026-10-01"] != models.DateOverrideRest {
		t.Fatalf("放假应映射为 rest，实得 %q", got["2026-10-01"])
	}
	if got["2026-10-10"] != models.DateOverrideWork {
		t.Fatalf("调休应映射为 work，实得 %q", got["2026-10-10"])
	}
}

func TestSyncHolidaysMergesAndReplacesByYear(t *testing.T) {
	setupPeakDB(t)
	ctx := context.Background()

	// 预置：2025 年的旧数据 + 同一个 2026 年的一条陈旧数据 + 手动覆盖
	cfg := models.DefaultPeakCalendar()
	cfg.DateOverrides = map[string]string{
		"2025-01-01":  models.DateOverrideRest,
		"2026-10-01":  models.DateOverrideRest, // 陈旧，应被 2026 同步结果替换
		"2026-12-31":  models.DateOverrideWork, // 手动覆盖，同属 2026，会被年度替换清掉
		"2023-05-01:": models.DateOverrideWork, // 非法键也不应让同步失败
	}
	if err := SavePeakCalendar(ctx, cfg); err != nil {
		t.Fatalf("seed: %v", err)
	}

	fake := func(_ context.Context, year int) ([]models.HolidayDay, string, error) {
		if year != 2026 {
			return nil, "", errors.New("unexpected year")
		}
		return []models.HolidayDay{
			{Date: "2026-10-01", Name: "国庆节", IsOffDay: true},
			{Date: "2026-10-10", Name: "调休上班", IsOffDay: false},
		}, "fake://source", nil
	}

	got, count, err := SyncHolidays(ctx, 2026, fake)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if count != 2 {
		t.Fatalf("同步条数应为 2，实得 %d", count)
	}

	// 其他年份保留
	if got.DateOverrides["2025-01-01"] != models.DateOverrideRest {
		t.Fatal("其他年份的覆盖应保留")
	}
	// 同年的手工条目被年度替换清掉
	if _, exists := got.DateOverrides["2026-12-31"]; exists {
		t.Fatal("同年的旧条目应被年度替换清除（保证重复同步幂等）")
	}
	// 新年份数据写入
	if got.DateOverrides["2026-10-01"] != models.DateOverrideRest {
		t.Fatalf("同步结果未写入：%v", got.DateOverrides)
	}
	if got.DateOverrides["2026-10-10"] != models.DateOverrideWork {
		t.Fatalf("调休未写入：%v", got.DateOverrides)
	}
	// 元信息
	if got.HolidaySource != "fake://source" {
		t.Fatalf("来源未记录：%q", got.HolidaySource)
	}
	if got.HolidaySyncedAt == 0 {
		t.Fatal("同步时间未记录")
	}

	// 落库且缓存已失效，重新读取应拿到新值
	if GetPeakCalendar(ctx).DateOverrides["2026-10-10"] != models.DateOverrideWork {
		t.Fatal("同步结果未持久化")
	}
}

func TestSyncHolidaysIsIdempotent(t *testing.T) {
	setupPeakDB(t)
	ctx := context.Background()

	fake := func(_ context.Context, _ int) ([]models.HolidayDay, string, error) {
		return []models.HolidayDay{
			{Date: "2026-10-01", Name: "国庆节", IsOffDay: true},
		}, "fake", nil
	}

	first, _, err := SyncHolidays(ctx, 2026, fake)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	second, _, err := SyncHolidays(ctx, 2026, fake)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}

	if len(first.DateOverrides) != len(second.DateOverrides) {
		t.Fatalf("重复同步应幂等：%d vs %d", len(first.DateOverrides), len(second.DateOverrides))
	}
}

func TestSyncHolidaysFetcherError(t *testing.T) {
	setupPeakDB(t)

	fake := func(_ context.Context, _ int) ([]models.HolidayDay, string, error) {
		return nil, "", errors.New("network down")
	}
	if _, _, err := SyncHolidays(context.Background(), 2026, fake); err == nil {
		t.Fatal("数据源失败时应返回错误")
	}
}

func TestSyncHolidaysEmptyResultIsError(t *testing.T) {
	setupPeakDB(t)

	// 数据源返回了 200 但内容为空：不能当成"同步成功 0 条"，
	// 否则会静默清空该年份的既有覆盖
	fake := func(_ context.Context, _ int) ([]models.HolidayDay, string, error) {
		return nil, "fake", nil
	}
	if _, _, err := SyncHolidays(context.Background(), 2026, fake); err == nil {
		t.Fatal("空结果应报错")
	}
}

func TestSyncHolidaysNilFetcherUsesDefault(t *testing.T) {
	setupPeakDB(t)

	// 传 nil 应使用 DefaultHolidayFetcher。内置数据覆盖 2024-2026，
	// 因此这个年份即使完全离线也应成功。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, count, err := SyncHolidays(ctx, 2026, nil)
	if err != nil {
		t.Fatalf("内置数据应保证 2026 可用：%v", err)
	}
	if count == 0 {
		t.Fatal("应同步出条目")
	}
	if cfg.DateOverrides["2026-10-10"] != models.DateOverrideWork {
		t.Fatalf("调休日未写入：%v", cfg.DateOverrides)
	}
}

// ---------------------------------------------------------------------------
// 内置节假日数据
// ---------------------------------------------------------------------------

func TestEmbeddedHolidayYears(t *testing.T) {
	t.Parallel()

	years := EmbeddedHolidayYears()
	if len(years) == 0 {
		t.Fatal("应内置至少一个年份的节假日数据")
	}
	want := map[int]bool{2024: true, 2025: true, 2026: true}
	for _, y := range years {
		delete(want, y)
	}
	if len(want) > 0 {
		t.Fatalf("缺少内置年份：%v（实有 %v）", want, years)
	}
	// 必须升序
	for i := 1; i < len(years); i++ {
		if years[i] <= years[i-1] {
			t.Fatalf("年份应升序：%v", years)
		}
	}
}

func TestLoadEmbeddedHolidays(t *testing.T) {
	t.Parallel()

	days, source, err := LoadEmbeddedHolidays(2026)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(days) == 0 {
		t.Fatal("应解析出条目")
	}
	if !strings.HasPrefix(source, "bundled:") {
		t.Fatalf("来源应标记为 bundled，实得 %q", source)
	}

	// 数据必须同时含放假与调休，否则"工作日"判定无法覆盖调休场景
	var hasOff, hasWork bool
	for _, d := range days {
		if d.Date == "" {
			t.Fatal("日期不应为空")
		}
		if d.IsOffDay {
			hasOff = true
		} else {
			hasWork = true
		}
	}
	if !hasOff || !hasWork {
		t.Fatalf("内置数据应同时含放假日与调休日：off=%v work=%v", hasOff, hasWork)
	}
}

func TestLoadEmbeddedHolidaysMissingYear(t *testing.T) {
	t.Parallel()

	if _, _, err := LoadEmbeddedHolidays(1999); err == nil {
		t.Fatal("未内置的年份应报错")
	}
}

// 内置数据是调休判定的最后一道保障，因此校验其完整性与内部一致性。
func TestEmbeddedHolidaysAreWellFormed(t *testing.T) {
	t.Parallel()

	for _, year := range EmbeddedHolidayYears() {
		days, _, err := LoadEmbeddedHolidays(year)
		if err != nil {
			t.Fatalf("%d: %v", year, err)
		}
		seen := map[string]bool{}
		for _, d := range days {
			parsed, err := time.Parse("2006-01-02", d.Date)
			if err != nil {
				t.Fatalf("%d: 非法日期 %q", year, d.Date)
			}
			if parsed.Year() != year {
				t.Fatalf("%d: 日期 %q 不在该年份内", year, d.Date)
			}
			if seen[d.Date] {
				t.Fatalf("%d: 日期 %q 重复", year, d.Date)
			}
			seen[d.Date] = true
		}
		// 内置数据条数应在合理区间：过少说明取数不完整
		if len(days) < 20 {
			t.Fatalf("%d: 仅 %d 条，疑似数据不完整", year, len(days))
		}
	}
}

func TestDefaultHolidayFetcherFallsBackToEmbedded(t *testing.T) {
	// 把远端地址指向必然失败的本地端口，验证回落到内置数据
	prev := HolidaySourceURLs
	HolidaySourceURLs = []string{"http://127.0.0.1:1/%d.json"}
	t.Cleanup(func() { HolidaySourceURLs = prev })

	days, source, err := DefaultHolidayFetcher(context.Background(), 2026)
	if err != nil {
		t.Fatalf("远端不可用时应回落到内置数据：%v", err)
	}
	if len(days) == 0 {
		t.Fatal("应拿到内置条目")
	}
	if !strings.HasPrefix(source, "bundled:") {
		t.Fatalf("回落后来源应标记为 bundled，实得 %q", source)
	}
}

func TestDefaultHolidayFetcherBothFail(t *testing.T) {
	// 远端不可用 + 年份未内置 → 必须报错，让调用方保留既有配置
	prev := HolidaySourceURLs
	HolidaySourceURLs = []string{"http://127.0.0.1:1/%d.json"}
	t.Cleanup(func() { HolidaySourceURLs = prev })

	if _, _, err := DefaultHolidayFetcher(context.Background(), 1999); err == nil {
		t.Fatal("两者都不可用时应报错")
	}
}

func TestDefaultHolidayFetcherPrefersRemote(t *testing.T) {
	// 远端可用时应优先采用远端（当年的安排可能被调整）
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"year":2026,"days":[{"date":"2026-12-25","name":"远端专用","isOffDay":true}]}`))
	}))
	defer srv.Close()

	prev := HolidaySourceURLs
	HolidaySourceURLs = []string{srv.URL + "/%d.json"}
	t.Cleanup(func() { HolidaySourceURLs = prev })

	days, source, err := DefaultHolidayFetcher(context.Background(), 2026)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !strings.HasPrefix(source, "remote:") {
		t.Fatalf("应优先采用远端，实得来源 %q", source)
	}
	if len(days) != 1 || days[0].Date != "2026-12-25" {
		t.Fatalf("应采用远端数据，实得 %+v", days)
	}
}

// ---------------------------------------------------------------------------
// 远端数据源
// ---------------------------------------------------------------------------

func TestFetchHolidaysFromRemote(t *testing.T) {
	// 用本地服务器替代外网，验证解析逻辑
	payload := `{"year":2026,"days":[
		{"date":"2026-01-01","name":"元旦","isOffDay":true},
		{"date":"2026-01-04","name":"调休上班","isOffDay":false}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	// 复用真实实现但把 URL 指向本地：通过替换模板临时改地址不可行，
	// 因此这里直接验证 JSON 解析这一等价逻辑。
	var got holidayResponse
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Year != 2026 || len(got.Days) != 2 {
		t.Fatalf("解析结果不符：%+v", got)
	}
	overrides := models.ToDateOverrides(got.Days)
	if overrides["2026-01-01"] != models.DateOverrideRest || overrides["2026-01-04"] != models.DateOverrideWork {
		t.Fatalf("转换结果不符：%v", overrides)
	}
}

func TestFetchHolidaysFromRemoteEndToEnd(t *testing.T) {
	// 把数据源指向本地服务器，真正走一遍 FetchHolidaysFromRemote 的
	// 请求、状态码、体积限制与解析路径。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/2026.json") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"year":2026,"days":[
			{"date":"2026-01-01","name":"元旦","isOffDay":true},
			{"date":"2026-01-04","name":"调休上班","isOffDay":false}
		]}`))
	}))
	defer srv.Close()

	prev := HolidaySourceURLs
	HolidaySourceURLs = []string{srv.URL + "/%d.json"}
	t.Cleanup(func() { HolidaySourceURLs = prev })

	days, source, err := FetchHolidaysFromRemote(context.Background(), 2026)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(days) != 2 {
		t.Fatalf("应解析出 2 条，实得 %d", len(days))
	}
	if days[0].Date != "2026-01-01" || !days[0].IsOffDay {
		t.Fatalf("首条不符：%+v", days[0])
	}
	if source == "" {
		t.Fatal("应回传数据来源")
	}

	// 年份不在服务端已知范围时应报错而不是返回空
	if _, _, err := FetchHolidaysFromRemote(context.Background(), 1999); err == nil {
		t.Fatal("非 200 响应应报错")
	}
}

func TestFetchHolidaysFromRemoteRejectsUnparsable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html>not json</html>`))
	}))
	defer srv.Close()

	prev := HolidaySourceURLs
	HolidaySourceURLs = []string{srv.URL + "/%d.json"}
	t.Cleanup(func() { HolidaySourceURLs = prev })

	if _, _, err := FetchHolidaysFromRemote(context.Background(), 2026); err == nil {
		t.Fatal("非 JSON 响应应报错")
	}
}

func TestFetchHolidaysFromRemoteRejectsEmptyDays(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"year":2026,"days":[]}`))
	}))
	defer srv.Close()

	prev := HolidaySourceURLs
	HolidaySourceURLs = []string{srv.URL + "/%d.json"}
	t.Cleanup(func() { HolidaySourceURLs = prev })

	// 200 但无条目：必须报错，否则会静默清空该年份的既有覆盖
	if _, _, err := FetchHolidaysFromRemote(context.Background(), 2026); err == nil {
		t.Fatal("空 days 应报错")
	}
}

func TestFetchHolidaysFromRemoteContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`{"year":2026,"days":[{"date":"2026-01-01","isOffDay":true}]}`))
	}))
	defer srv.Close()

	prev := HolidaySourceURLs
	HolidaySourceURLs = []string{srv.URL + "/%d.json"}
	t.Cleanup(func() { HolidaySourceURLs = prev })

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	if _, _, err := FetchHolidaysFromRemote(ctx, 2026); err == nil {
		t.Fatal("已取消的 context 应报错")
	}
}
