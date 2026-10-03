package models

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// 迁移要守住的不变量：
//
//  1. 旧配置里的日历部分（时区/星期几/日期覆盖/同步记录）必须一字不落地搬进
//     KeyPeakCalendar——它是全局事实，没理由在改造中被丢掉；
//  2. 条款只有在旧配置**实际启用过**时才抄给当时的每条关联。抄了就等于宣称
//     "这些上游此前就在按这套时段计费"，只有真的启用过才成立；
//  3. 已经自己配过条款的关联不能被覆盖——迁移是补空缺，不是重置；
//  4. 旧键迁移后消失，于是重复执行无害，也不会把陈年条款盖到新建的关联上。

// setupPeakMigrateDB 建一张含配置表与关联表的库并接管全局 DB。
func setupPeakMigrateDB(t *testing.T) *gorm.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "peak-migrate.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	closeOnCleanup(t, db)

	if err := db.AutoMigrate(&Config{}, &ModelWithProvider{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	prev := DB
	DB = db
	t.Cleanup(func() { DB = prev })
	return db
}

func seedConfig(t *testing.T, db *gorm.DB, key, value string) {
	t.Helper()
	if err := db.Create(&Config{Key: key, Value: value}).Error; err != nil {
		t.Fatalf("seed config %s: %v", key, err)
	}
}

func seedAssociation(t *testing.T, db *gorm.DB, providerModel string, peak *PeakTerms) ModelWithProvider {
	t.Helper()
	row := ModelWithProvider{ModelID: 1, ProviderID: 2, ProviderModel: providerModel, Peak: peak}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed association: %v", err)
	}
	return row
}

func configValue(t *testing.T, db *gorm.DB, key string) (string, bool) {
	t.Helper()
	var row Config
	err := db.Where("key = ?", key).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return "", false
	}
	if err != nil {
		t.Fatalf("read config %s: %v", key, err)
	}
	return row.Value, true
}

func associationPeak(t *testing.T, db *gorm.DB, id uint) *PeakTerms {
	t.Helper()
	row, err := gorm.G[ModelWithProvider](db).Where("id = ?", id).First(context.Background())
	if err != nil {
		t.Fatalf("read association %d: %v", id, err)
	}
	return row.Peak
}

// legacyPricingJSON 造一份旧的全局峰谷配置。
func legacyPricingJSON(t *testing.T, enabled bool, periods []PeakPeriod) string {
	t.Helper()
	old := legacyPeakPricing{
		Enabled:         enabled,
		Timezone:        "Asia/Tokyo",
		Weekdays:        []int{1, 2, 3, 4, 5, 6},
		Periods:         periods,
		DateOverrides:   map[string]string{"2026-10-10": DateOverrideWork},
		HolidaySyncedAt: 1750000000,
		HolidaySource:   "remote:https://example.invalid/2026.json",
	}
	raw, err := json.Marshal(old)
	if err != nil {
		t.Fatalf("marshal legacy: %v", err)
	}
	return string(raw)
}

var legacyPeriods = []PeakPeriod{{Name: "夜间", Start: "00:30", End: "08:30", Multiplier: 0.25}}

func TestMigratePeakTermsSplitsLegacyConfig(t *testing.T) {
	db := setupPeakMigrateDB(t)
	seedConfig(t, db, KeyPeakPricingLegacy, legacyPricingJSON(t, true, legacyPeriods))
	first := seedAssociation(t, db, "gpt-4o", nil)
	second := seedAssociation(t, db, "claude-sonnet", nil)

	if err := MigratePeakTermsToAssociations(context.Background()); err != nil {
		t.Fatalf("迁移应成功：%v", err)
	}

	// 日历部分原样搬过去
	raw, ok := configValue(t, db, KeyPeakCalendar)
	if !ok {
		t.Fatal("日历未写入")
	}
	var cal PeakCalendar
	if err := json.Unmarshal([]byte(raw), &cal); err != nil {
		t.Fatalf("日历不可解析：%v", err)
	}
	if cal.Timezone != "Asia/Tokyo" || len(cal.Weekdays) != 6 {
		t.Fatalf("日历的时区/工作日未搬过来：%+v", cal)
	}
	if cal.DateOverrides["2026-10-10"] != DateOverrideWork {
		t.Fatalf("日期覆盖未搬过来：%v", cal.DateOverrides)
	}
	if cal.HolidaySyncedAt != 1750000000 || cal.HolidaySource == "" {
		t.Fatalf("同步记录未搬过来：%+v", cal)
	}

	// 条款抄给当时的每条关联
	for _, id := range []uint{first.ID, second.ID} {
		peak := associationPeak(t, db, id)
		if peak == nil || !peak.Enabled || len(peak.Periods) != 1 {
			t.Fatalf("关联 %d 未拿到旧条款：%+v", id, peak)
		}
		if peak.Periods[0].Multiplier != 0.25 {
			t.Fatalf("关联 %d 的条款内容不符：%+v", id, peak.Periods[0])
		}
	}

	// 旧键消失，迁移一次性完成
	if _, ok := configValue(t, db, KeyPeakPricingLegacy); ok {
		t.Fatal("旧键应在迁移后删除，否则每次启动都会把陈年条款重抄一遍")
	}
}

func TestMigratePeakTermsDisabledOnlyMovesCalendar(t *testing.T) {
	db := setupPeakMigrateDB(t)
	seedConfig(t, db, KeyPeakPricingLegacy, legacyPricingJSON(t, false, legacyPeriods))
	row := seedAssociation(t, db, "gpt-4o", nil)

	if err := MigratePeakTermsToAssociations(context.Background()); err != nil {
		t.Fatalf("迁移应成功：%v", err)
	}

	// 没启用过 = 从没在计费，抄下去只会制造"配过"的假象
	if peak := associationPeak(t, db, row.ID); peak != nil {
		t.Fatalf("未启用过的旧配置不该抄给关联，实得 %+v", peak)
	}
	if _, ok := configValue(t, db, KeyPeakCalendar); !ok {
		t.Fatal("日历部分与启用与否无关，仍应搬过来")
	}
	if _, ok := configValue(t, db, KeyPeakPricingLegacy); ok {
		t.Fatal("旧键应被删除")
	}
}

func TestMigratePeakTermsEmptyPeriodsOnlyMovesCalendar(t *testing.T) {
	db := setupPeakMigrateDB(t)
	seedConfig(t, db, KeyPeakPricingLegacy, legacyPricingJSON(t, true, nil))
	row := seedAssociation(t, db, "gpt-4o", nil)

	if err := MigratePeakTermsToAssociations(context.Background()); err != nil {
		t.Fatalf("迁移应成功：%v", err)
	}

	// 启用了但没有时段 = 没有任何时刻会被命中，抄一份空条款同样没有意义
	if peak := associationPeak(t, db, row.ID); peak != nil {
		t.Fatalf("空时段不该抄给关联，实得 %+v", peak)
	}
}

func TestMigratePeakTermsKeepsAssociationOwnTerms(t *testing.T) {
	db := setupPeakMigrateDB(t)
	seedConfig(t, db, KeyPeakPricingLegacy, legacyPricingJSON(t, true, legacyPeriods))

	own := PeakTerms{
		Enabled: true,
		Periods: []PeakPeriod{{Name: "自己的", Start: "09:00", End: "18:00", Multiplier: 1.5}},
	}
	row := seedAssociation(t, db, "gpt-4o", &own)

	if err := MigratePeakTermsToAssociations(context.Background()); err != nil {
		t.Fatalf("迁移应成功：%v", err)
	}

	// 迁移是补空缺而不是重置：已经自己配过的关联必须原样保留
	peak := associationPeak(t, db, row.ID)
	if peak == nil || len(peak.Periods) != 1 || peak.Periods[0].Name != "自己的" {
		t.Fatalf("已配好的关联被迁移覆盖了：%+v", peak)
	}
}

func TestMigratePeakTermsIsIdempotent(t *testing.T) {
	db := setupPeakMigrateDB(t)
	seedConfig(t, db, KeyPeakPricingLegacy, legacyPricingJSON(t, true, legacyPeriods))
	row := seedAssociation(t, db, "gpt-4o", nil)

	ctx := context.Background()
	if err := MigratePeakTermsToAssociations(ctx); err != nil {
		t.Fatalf("首次迁移应成功：%v", err)
	}

	// 迁移后用户改了日历，又建了一条新关联
	later := PeakCalendar{Timezone: "UTC", Weekdays: []int{0}, DateOverrides: map[string]string{}}
	raw, err := json.Marshal(later)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := SaveConfigValue(ctx, KeyPeakCalendar, string(raw)); err != nil {
		t.Fatalf("save calendar: %v", err)
	}
	fresh := seedAssociation(t, db, "new-model", nil)

	if err := MigratePeakTermsToAssociations(ctx); err != nil {
		t.Fatalf("重跑迁移应无错：%v", err)
	}

	got, ok := configValue(t, db, KeyPeakCalendar)
	if !ok || got != string(raw) {
		t.Fatalf("重跑迁移改动了用户后来保存的日历：%s", got)
	}
	if peak := associationPeak(t, db, fresh.ID); peak != nil {
		t.Fatalf("新建的关联被旧配置污染了：%+v", peak)
	}
	if peak := associationPeak(t, db, row.ID); peak == nil {
		t.Fatal("重跑迁移不该撤销首次迁移的结果")
	}
}

func TestMigratePeakTermsMissingLegacyKeyIsNoop(t *testing.T) {
	db := setupPeakMigrateDB(t)
	row := seedAssociation(t, db, "gpt-4o", nil)

	if err := MigratePeakTermsToAssociations(context.Background()); err != nil {
		t.Fatalf("没有旧键时应直接返回：%v", err)
	}
	if _, ok := configValue(t, db, KeyPeakCalendar); ok {
		t.Fatal("没有旧配置就不该凭空写出一份日历")
	}
	if peak := associationPeak(t, db, row.ID); peak != nil {
		t.Fatalf("没有旧配置就不该给关联写条款：%+v", peak)
	}
}

func TestMigratePeakTermsCorruptLegacyValueDropsKey(t *testing.T) {
	db := setupPeakMigrateDB(t)
	seedConfig(t, db, KeyPeakPricingLegacy, "{not json")
	row := seedAssociation(t, db, "gpt-4o", nil)

	// 读不出来的旧值不值得让进程起不来：删掉即可，旧配置本就会回落到默认
	if err := MigratePeakTermsToAssociations(context.Background()); err != nil {
		t.Fatalf("损坏的旧值不应让迁移报错：%v", err)
	}
	if _, ok := configValue(t, db, KeyPeakPricingLegacy); ok {
		t.Fatal("损坏的旧键应被删掉，否则每次启动都要再报一次")
	}
	if _, ok := configValue(t, db, KeyPeakCalendar); ok {
		t.Fatal("未解析出内容时不该写入日历")
	}
	if peak := associationPeak(t, db, row.ID); peak != nil {
		t.Fatalf("未解析出内容时不该给关联写条款：%+v", peak)
	}
}

func TestMigratePeakTermsPropagatesQueryError(t *testing.T) {
	db := setupPeakMigrateDB(t)

	// 表不存在时查询返回的不是 ErrRecordNotFound，应原样透出而不是当成"没有旧配置"
	if err := db.Exec("DROP TABLE configs").Error; err != nil {
		t.Fatalf("drop: %v", err)
	}

	if err := MigratePeakTermsToAssociations(context.Background()); err == nil {
		t.Fatal("查询失败时应返回错误")
	}
}
