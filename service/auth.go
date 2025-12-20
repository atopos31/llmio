package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/atopos31/llmio/models"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

var singleFlightGroup singleflight.Group

func GetAuthKey(ctx context.Context, key string) (*models.AuthKey, error) {
	ch := singleFlightGroup.DoChan(key, func() (any, error) {
		authKey, err := gorm.G[models.AuthKey](models.DB).Where("key = ?", key).Where("status = ?", true).First(ctx)
		return &authKey, err
	})

	select {
	case r := <-ch:
		return r.Val.(*models.AuthKey), r.Err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type KeyUpdateItem struct {
	Count  int
	UsedAt time.Time
}

var (
	updateCounts = make(map[uint]KeyUpdateItem)
	mu           sync.Mutex
	startOnce    sync.Once
)

func KeyUpdate(keyID uint, usedAt time.Time) {
	mu.Lock()
	updateCounts[keyID] = KeyUpdateItem{
		Count:  updateCounts[keyID].Count + 1,
		UsedAt: usedAt,
	}
	mu.Unlock()

	// 确保后台刷新协程只启动一次
	startOnce.Do(func() {
		go backgroundFlush()
	})
}

func backgroundFlush() {
	for range time.Tick(10 * time.Second) {
		ctx := context.Background()
		mu.Lock()
		for keyID, item := range updateCounts {
			if err := models.DB.Model(&models.AuthKey{}).WithContext(ctx).Where("id = ?", keyID).Updates(map[string]any{
				"usage_count":  gorm.Expr(fmt.Sprintf("usage_count + %d", item.Count)),
				"last_used_at": item.UsedAt,
			}).Error; err != nil {
				slog.Error("Failed to update auth key usage count", "error", err)
			}
		}
		updateCounts = make(map[uint]KeyUpdateItem) // 清空计数
		mu.Unlock()
	}
}
