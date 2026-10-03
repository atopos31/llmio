package models

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"gorm.io/gorm"
)

// legacyPeakPricing 旧的全局峰谷配置形态。
//
// 单独列一份而不是复用 PeakTerms + PeakCalendar：这里描述的是**历史格式**，
// 它不该跟着现在的结构体一起演进。若某天再给日历加字段，这个结构必须
// 保持原样，否则迁移逻辑会跟着漂移。
type legacyPeakPricing struct {
	Enabled         bool              `json:"enabled"`
	Timezone        string            `json:"timezone"`
	Weekdays        []int             `json:"weekdays"`
	Periods         []PeakPeriod      `json:"periods"`
	DateOverrides   map[string]string `json:"dateOverrides"`
	HolidaySyncedAt int64             `json:"holidaySyncedAt"`
	HolidaySource   string            `json:"holidaySource"`
}

// MigratePeakTermsToAssociations 把旧的全局峰谷配置拆成「全局日历 + 每条关联的条款」。
//
// 迁移完成后旧键即被删除，因此：
//   - 可重复执行（第二次找不到键，直接返回）；
//   - 新建的关联不会被一份陈年旧配置反复盖条款。
//
// 只有旧配置**启用过**才把条款抄给当时的每条关联：那是它此前实际生效的范围，
// 升级后成本数字不该因此变化。没启用的旧配置本来就没在计费，抄下去等于把
// 一份示例模板盖到所有关联上，反而制造出"配过"的假象。
func MigratePeakTermsToAssociations(ctx context.Context) error {
	var row Config
	err := DB.WithContext(ctx).Where("key = ?", KeyPeakPricingLegacy).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	var old legacyPeakPricing
	if err := json.Unmarshal([]byte(row.Value), &old); err != nil {
		// 旧值损坏时删掉即可：旧配置本就回落到默认值，不值得因为一段
		// 读不出来的 JSON 让整个进程起不来
		slog.Warn("peak pricing legacy config is unreadable, dropping it", "error", err)
		return DB.WithContext(ctx).Where("key = ?", KeyPeakPricingLegacy).Delete(&Config{}).Error
	}

	calendar := PeakCalendar{
		Timezone:        old.Timezone,
		Weekdays:        old.Weekdays,
		DateOverrides:   old.DateOverrides,
		HolidaySyncedAt: old.HolidaySyncedAt,
		HolidaySource:   old.HolidaySource,
	}
	if calendar.DateOverrides == nil {
		calendar.DateOverrides = map[string]string{}
	}
	raw, err := json.Marshal(calendar)
	if err != nil {
		return err
	}
	if err := SaveConfigValue(ctx, KeyPeakCalendar, string(raw)); err != nil {
		return err
	}

	if old.Enabled && len(old.Periods) > 0 {
		terms := PeakTerms{Enabled: true, Periods: old.Periods}
		rows, err := gorm.G[ModelWithProvider](DB).Where("peak IS NULL").Find(ctx)
		if err != nil {
			return err
		}
		// 逐条走结构体更新而不是一条 UPDATE 拼 JSON：序列化交给字段上的
		// serializer 去做，将来换存储格式时这里不必跟着改。
		for _, r := range rows {
			if _, err := gorm.G[ModelWithProvider](DB).
				Where("id = ?", r.ID).
				Updates(ctx, ModelWithProvider{Peak: &terms}); err != nil {
				return err
			}
		}
		slog.Info("migrated peak pricing terms onto existing associations", "count", len(rows))
	}

	return DB.WithContext(ctx).Where("key = ?", KeyPeakPricingLegacy).Delete(&Config{}).Error
}

// SaveConfigValue 按 key 写入配置值，不存在则新建。
func SaveConfigValue(ctx context.Context, key, value string) error {
	var row Config
	err := DB.WithContext(ctx).Where("key = ?", key).First(&row).Error
	switch {
	case err == nil:
		row.Value = value
		return DB.WithContext(ctx).Save(&row).Error
	case errors.Is(err, gorm.ErrRecordNotFound):
		return DB.WithContext(ctx).Create(&Config{Key: key, Value: value}).Error
	default:
		return err
	}
}
