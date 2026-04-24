package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/atopos31/llmio/models"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupLogCleanupTestDB(t *testing.T) func() {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	if err := db.AutoMigrate(&models.ChatLog{}, &models.ChatIO{}, &models.Config{}, &models.LogCleanupRecord{}); err != nil {
		t.Fatalf("failed to migrate test database: %v", err)
	}

	models.DB = db
	return func() {
		models.DB = nil
	}
}

func TestCleanLogsByDays(t *testing.T) {
	cleanup := setupLogCleanupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	oldLog := models.ChatLog{Name: "old-log", Status: "success"}
	if err := gorm.G[models.ChatLog](models.DB).Create(ctx, &oldLog); err != nil {
		t.Fatalf("failed to create old log: %v", err)
	}
	if err := models.DB.Model(&models.ChatLog{}).Where("id = ?", oldLog.ID).Update("created_at", time.Now().AddDate(0, 0, -40)).Error; err != nil {
		t.Fatalf("failed to age old log: %v", err)
	}

	newLog := models.ChatLog{Name: "new-log", Status: "success"}
	if err := gorm.G[models.ChatLog](models.DB).Create(ctx, &newLog); err != nil {
		t.Fatalf("failed to create new log: %v", err)
	}
	if err := models.DB.Model(&models.ChatLog{}).Where("id = ?", newLog.ID).Update("created_at", time.Now().AddDate(0, 0, -2)).Error; err != nil {
		t.Fatalf("failed to date new log: %v", err)
	}

	if err := gorm.G[models.ChatIO](models.DB).Create(ctx, &models.ChatIO{LogId: oldLog.ID, Input: "old"}); err != nil {
		t.Fatalf("failed to create old chat io: %v", err)
	}
	if err := gorm.G[models.ChatIO](models.DB).Create(ctx, &models.ChatIO{LogId: newLog.ID, Input: "new"}); err != nil {
		t.Fatalf("failed to create new chat io: %v", err)
	}

	deletedCount, err := CleanLogsByDays(ctx, 30)
	if err != nil {
		t.Fatalf("CleanLogsByDays failed: %v", err)
	}
	if deletedCount != 1 {
		t.Fatalf("expected 1 deleted log, got %d", deletedCount)
	}

	var oldLogCount int64
	if err := models.DB.Unscoped().Model(&models.ChatLog{}).Where("id = ?", oldLog.ID).Count(&oldLogCount).Error; err != nil {
		t.Fatalf("failed to count old log: %v", err)
	}
	if oldLogCount != 0 {
		t.Fatalf("expected old log to be deleted, got count %d", oldLogCount)
	}

	var newLogCount int64
	if err := models.DB.Unscoped().Model(&models.ChatLog{}).Where("id = ?", newLog.ID).Count(&newLogCount).Error; err != nil {
		t.Fatalf("failed to count new log: %v", err)
	}
	if newLogCount != 1 {
		t.Fatalf("expected new log to remain, got count %d", newLogCount)
	}

	var oldChatIOCount int64
	if err := models.DB.Unscoped().Model(&models.ChatIO{}).Where("log_id = ?", oldLog.ID).Count(&oldChatIOCount).Error; err != nil {
		t.Fatalf("failed to count old chat io: %v", err)
	}
	if oldChatIOCount != 0 {
		t.Fatalf("expected old chat io to be deleted, got count %d", oldChatIOCount)
	}

	var newChatIOCount int64
	if err := models.DB.Unscoped().Model(&models.ChatIO{}).Where("log_id = ?", newLog.ID).Count(&newChatIOCount).Error; err != nil {
		t.Fatalf("failed to count new chat io: %v", err)
	}
	if newChatIOCount != 1 {
		t.Fatalf("expected new chat io to remain, got count %d", newChatIOCount)
	}
}

func TestGetLogCleanupPolicyDefaultsWhenMissing(t *testing.T) {
	cleanup := setupLogCleanupTestDB(t)
	defer cleanup()

	policy, err := GetLogCleanupPolicy(context.Background())
	if err != nil {
		t.Fatalf("GetLogCleanupPolicy failed: %v", err)
	}
	if policy == nil {
		t.Fatal("expected policy pointer")
	}
	if policy.Enabled {
		t.Fatal("expected default policy to be disabled")
	}
	if policy.RetentionDays != defaultLogRetentionDays {
		t.Fatalf("expected default retention days %d, got %d", defaultLogRetentionDays, policy.RetentionDays)
	}
}

func TestGetLogCleanupPolicyReadsConfig(t *testing.T) {
	cleanup := setupLogCleanupTestDB(t)
	defer cleanup()

	value, err := json.Marshal(models.LogCleanupPolicy{
		Enabled:       true,
		RetentionDays: 7,
	})
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}

	if err := gorm.G[models.Config](models.DB).Create(context.Background(), &models.Config{
		Key:   models.KeyLogCleanupPolicy,
		Value: string(value),
	}); err != nil {
		t.Fatalf("failed to create config: %v", err)
	}

	policy, err := GetLogCleanupPolicy(context.Background())
	if err != nil {
		t.Fatalf("GetLogCleanupPolicy failed: %v", err)
	}
	if policy == nil {
		t.Fatal("expected policy pointer")
	}
	if !policy.Enabled {
		t.Fatal("expected policy to be enabled")
	}
	if policy.RetentionDays != 7 {
		t.Fatalf("expected retention days 7, got %d", policy.RetentionDays)
	}
}
