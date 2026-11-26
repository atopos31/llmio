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

const (
	// authKeyLookupAttempts 是撞锁时的读重试次数。
	authKeyLookupAttempts = 3
	// authKeyInstantBusyWindow：一次失败快于它，说明 SQLite 根本没等——
	// 写者已进 PENDING 时 BUSY 不经过 busy_timeout，这种重试才有收益。
	// 等满了 busy_timeout 的是长锁，退避期间不会释放：直接交给 503，
	// 别把 3–5 秒的失败拖成 15 秒（还占着连接）。
	authKeyInstantBusyWindow = 500 * time.Millisecond
	// authKeyLookupBudget 是查询+退避的自身上限。singleflight 的函数体
	// 不跟随首个调用者的 ctx：它断开不该连累其他等待者（等待者仍按
	// 自己的 ctx 退出）。
	authKeyLookupBudget = 10 * time.Second
)

func GetAuthKey(ctx context.Context, key string) (*models.AuthKey, error) {
	ch := singleFlightGroup.DoChan(key, func() (any, error) {
		lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), authKeyLookupBudget)
		defer cancel()

		var lastErr error
		for attempt := range authKeyLookupAttempts {
			if attempt > 0 {
				sleepBackoff(lookupCtx, attempt)
			}
			start := time.Now()
			authKey, err := gorm.G[models.AuthKey](models.DB).Where("key = ?", key).Where("status = ?", true).First(lookupCtx)
			if err == nil {
				return &authKey, nil
			}
			if !IsBusyErr(err) {
				// 非撞锁错误（键不存在等）重试无意义。
				return &authKey, err
			}
			lastErr = err
			if time.Since(start) >= authKeyInstantBusyWindow {
				break
			}
		}
		return nil, lastErr
	})

	select {
	case r := <-ch:
		if r.Err != nil {
			return nil, r.Err
		}
		authKey, ok := r.Val.(*models.AuthKey)
		if !ok || authKey == nil {
			return nil, fmt.Errorf("auth key lookup returned no value")
		}
		return authKey, nil
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
