package models

import (
	"context"

	"gorm.io/gorm"
)

type LogCleanupRecord struct {
	gorm.Model
	RetentionDays int    `json:"RetentionDays"`
	DeletedCount  int64  `json:"DeletedCount"`
	DurationMs    int64  `json:"DurationMs"`
	Source        string `json:"Source"`
	Type          string `json:"Type"`
}

// TrimLogCleanupRecords 保留最近 limit 条记录，删除多余的旧记录
func TrimLogCleanupRecords(ctx context.Context, limit int) {
	var maxID uint
	if err := gorm.G[LogCleanupRecord](DB).Select("id").Order("id DESC").Offset(limit - 1).Limit(1).Scan(ctx, &maxID); err != nil || maxID == 0 {
		return
	}
	DB.Unscoped().Where("id < ?", maxID).Delete(&LogCleanupRecord{})
}
