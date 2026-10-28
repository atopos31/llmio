package models

import (
	"context"
	"testing"
)

// TestEnsureConfigIfMissing_SeedsDefault 缺行时写入默认值。
func TestEnsureConfigIfMissing_SeedsDefault(t *testing.T) {
	openTestDB(t)

	ctx := context.Background()
	err := EnsureConfigIfMissing(ctx, KeySelfHealPolicy, struct {
		Enabled bool `json:"enabled"`
	}{Enabled: true})
	if err != nil {
		t.Fatalf("种子失败：%v", err)
	}

	var got Config
	if err := DB.Where("key = ?", KeySelfHealPolicy).First(&got).Error; err != nil {
		t.Fatalf("读回种子失败：%v", err)
	}
	if got.Value != `{"enabled":true}` {
		t.Fatalf("落库值 = %s，want {\"enabled\":true}", got.Value)
	}
}

// TestEnsureConfigIfMissing_DoesNotOverwrite 已存在时**什么都不做**。
//
// 这是它与 SaveConfigValue 的唯一差别，也是种子能安全放在每次启动的原因：
// 用户把开关打开之后重启，不该被改回默认值。
func TestEnsureConfigIfMissing_DoesNotOverwrite(t *testing.T) {
	openTestDB(t)

	ctx := context.Background()
	const userValue = `{"enabled":false,"max_drops":0}`
	if err := DB.Exec(
		"INSERT INTO configs (created_at, updated_at, key, value) VALUES (datetime('now'), datetime('now'), ?, ?)",
		KeySelfHealPolicy, userValue,
	).Error; err != nil {
		t.Fatalf("准备用户值失败：%v", err)
	}

	if err := EnsureConfigIfMissing(ctx, KeySelfHealPolicy, struct {
		Enabled bool `json:"enabled"`
	}{Enabled: true}); err != nil {
		t.Fatalf("种子失败：%v", err)
	}

	var got Config
	if err := DB.Where("key = ?", KeySelfHealPolicy).First(&got).Error; err != nil {
		t.Fatalf("读回失败：%v", err)
	}
	if got.Value != userValue {
		t.Fatalf("用户值被覆盖了：%s，want %s", got.Value, userValue)
	}
}

// TestGetSelfHealPolicySilentAfterSeed 种子之后读策略不再产生 record not found。
//
// 这条是这次修复的直接目的：GetSelfHealPolicy 跑在**每个聊天请求**的转发循环里，
// 缺行时每次请求都打一条 record not found。判据是"种子之后库里有那一行"，
// 而不是去抓日志——行为上等价，且不依赖日志实现。
func TestGetSelfHealPolicySilentAfterSeed(t *testing.T) {
	openTestDB(t)

	ctx := context.Background()
	if err := DB.Exec("DELETE FROM configs WHERE key = ?", KeySelfHealPolicy).Error; err != nil {
		t.Fatalf("准备缺行状态失败：%v", err)
	}

	// 种子前：读得到默认值，但库里仍是缺行。
	var n int64
	if err := DB.Model(&Config{}).Where("key = ?", KeySelfHealPolicy).Count(&n).Error; err != nil {
		t.Fatalf("计数失败：%v", err)
	}
	if n != 0 {
		t.Fatalf("前置条件不成立：种子前已经有 %d 行", n)
	}

	if err := EnsureConfigIfMissing(ctx, KeySelfHealPolicy, struct {
		Enabled  bool `json:"enabled"`
		MaxDrops int  `json:"max_drops"`
	}{Enabled: true, MaxDrops: 3}); err != nil {
		t.Fatalf("种子失败：%v", err)
	}

	// 种子后：点查能命中，不再走 not found 分支。
	err := DB.Where("key = ?", KeySelfHealPolicy).First(&Config{}).Error
	if err != nil {
		t.Fatalf("种子之后仍查不到：%v —— 噪音会继续按请求数刷", err)
	}
}
