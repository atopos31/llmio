package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/atopos31/llmio/models"
	"gorm.io/gorm"
)

const (
	defaultLogRetentionDays = 30
	logCleanupCheckInterval = time.Hour
)

func DefaultLogCleanupPolicy() *models.LogCleanupPolicy {
	return &models.LogCleanupPolicy{
		Enabled:       false,
		RetentionDays: defaultLogRetentionDays,
	}
}

func GetLogCleanupPolicy(ctx context.Context) (*models.LogCleanupPolicy, error) {
	config, err := gorm.G[models.Config](models.DB).Where("key = ?", models.KeyLogCleanupPolicy).First(ctx)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return DefaultLogCleanupPolicy(), nil
		}
		return nil, err
	}

	if config.Value == "" {
		return DefaultLogCleanupPolicy(), nil
	}

	policy := DefaultLogCleanupPolicy()
	if err := json.Unmarshal([]byte(config.Value), policy); err != nil {
		return nil, fmt.Errorf("unmarshal log cleanup policy: %w", err)
	}
	if policy.RetentionDays <= 0 {
		policy.RetentionDays = defaultLogRetentionDays
	}
	return policy, nil
}

func CleanLogsByDays(ctx context.Context, days int) (int64, error) {
	if days <= 0 {
		return 0, fmt.Errorf("days must be greater than 0")
	}

	cutoffTime := time.Now().AddDate(0, 0, -days)
	var deletedCount int64

	err := models.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Unscoped().
			Where("log_id IN (SELECT id FROM chat_logs WHERE created_at < ?)", cutoffTime).
			Delete(&models.ChatIO{}).Error; err != nil {
			return fmt.Errorf("delete chat io: %w", err)
		}

		result := tx.Unscoped().Where("created_at < ?", cutoffTime).Delete(&models.ChatLog{})
		if result.Error != nil {
			return fmt.Errorf("delete logs: %w", result.Error)
		}
		deletedCount = result.RowsAffected
		return nil
	})
	if err != nil {
		return 0, err
	}

	return deletedCount, nil
}

func StartLogCleanupScheduler(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(logCleanupCheckInterval)
		defer ticker.Stop()

		lastRunDay := ""
		run := func() {
			policy, err := GetLogCleanupPolicy(ctx)
			if err != nil {
				slog.Error("load log cleanup policy failed", "error", err)
				return
			}
			if !policy.Enabled || policy.RetentionDays <= 0 {
				return
			}

			today := time.Now().Format("2006-01-02")
			if lastRunDay == today {
				return
			}

			start := time.Now()
			deletedCount, err := CleanLogsByDays(ctx, policy.RetentionDays)
			if err != nil {
				slog.Error("scheduled log cleanup failed", "retention_days", policy.RetentionDays, "error", err)
				return
			}

			lastRunDay = today
			slog.Info("scheduled log cleanup completed",
				"retention_days", policy.RetentionDays,
				"deleted_count", deletedCount,
				"duration", time.Since(start),
			)
		}

		run()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}
