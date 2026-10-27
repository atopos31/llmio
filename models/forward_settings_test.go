package models

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/atopos31/llmio/consts"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupForwardBackfillDB 打开一个只含 providers 表的临时库并挂到包级 DB，
// 让 backfillForwardSettings 像启动时一样工作。
func setupForwardBackfillDB(t *testing.T) *gorm.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "forward-backfill.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&Provider{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	closeOnCleanup(t, db)

	prev := DB
	DB = db
	t.Cleanup(func() { DB = prev })
	return db
}

func loadBackfilledProvider(t *testing.T, id uint) Provider {
	t.Helper()
	provider, err := gorm.G[Provider](DB).Where("id = ?", id).First(context.Background())
	if err != nil {
		t.Fatalf("load provider %d: %v", id, err)
	}
	return provider
}

func assertSlug(t *testing.T, provider Provider, want string) {
	t.Helper()
	if want == "" {
		if provider.ForwardSlug != nil {
			t.Errorf("provider %q 的 slug 应为空, got %q", provider.Name, *provider.ForwardSlug)
		}
		return
	}
	if provider.ForwardSlug == nil || *provider.ForwardSlug != want {
		t.Errorf("provider %q 的 slug = %v, want %q", provider.Name, provider.ForwardSlug, want)
	}
}

func assertRewrite(t *testing.T, provider Provider, want string) {
	t.Helper()
	if provider.ForwardRewrite != want {
		t.Errorf("provider %q 的 rewrite = %q, want %q", provider.Name, provider.ForwardRewrite, want)
	}
}

// 升级回填：安全名称补 slug、不安全/重名保持空；旧 ForwardMode 语义映射到改写策略
// （pure→none 不改写、off/mixed→known 认识才改）；已有值不覆盖；重复执行幂等。
func TestBackfillForwardSettings(t *testing.T) {
	db := setupForwardBackfillDB(t)

	occupied := Provider{Name: "occupied-owner", Type: "openai", ForwardSlug: new("taken")}
	if err := db.Create(&occupied).Error; err != nil {
		t.Fatalf("create occupied owner: %v", err)
	}

	safe := Provider{Name: "safe-a", Type: "openai"}
	pure := Provider{Name: "pure-provider", Type: "openai", ForwardMode: new(true)}
	mixed := Provider{Name: "mixed-provider", Type: "openai", ForwardMode: new(false)}
	unsafe := Provider{Name: "unsafe name", Type: "openai"}
	duplicate := Provider{Name: "taken", Type: "openai"}
	explicit := Provider{Name: "explicit-all", Type: "openai", ForwardRewrite: consts.ForwardRewriteAll, ForwardTargetModel: "fixed"}
	preSlug := Provider{Name: "pre-slug", Type: "openai", ForwardSlug: new("occupied")}
	for _, p := range []*Provider{&safe, &pure, &mixed, &unsafe, &duplicate, &explicit, &preSlug} {
		if err := db.Create(p).Error; err != nil {
			t.Fatalf("create provider %q: %v", p.Name, err)
		}
	}

	if err := backfillForwardSettings(context.Background()); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	// 幂等：再跑一次不得改变结果。
	if err := backfillForwardSettings(context.Background()); err != nil {
		t.Fatalf("backfill rerun: %v", err)
	}

	safe = loadBackfilledProvider(t, safe.ID)
	assertSlug(t, safe, "safe-a")
	assertRewrite(t, safe, consts.ForwardRewriteKnown)

	pure = loadBackfilledProvider(t, pure.ID)
	assertSlug(t, pure, "pure-provider")
	assertRewrite(t, pure, consts.ForwardRewriteNone)

	mixed = loadBackfilledProvider(t, mixed.ID)
	assertSlug(t, mixed, "mixed-provider")
	assertRewrite(t, mixed, consts.ForwardRewriteKnown)

	unsafe = loadBackfilledProvider(t, unsafe.ID)
	assertSlug(t, unsafe, "")
	assertRewrite(t, unsafe, consts.ForwardRewriteKnown)

	duplicate = loadBackfilledProvider(t, duplicate.ID)
	assertSlug(t, duplicate, "")

	explicit = loadBackfilledProvider(t, explicit.ID)
	assertRewrite(t, explicit, consts.ForwardRewriteAll)
	if explicit.ForwardTargetModel != "fixed" {
		t.Errorf("已有 target 不应被覆盖, got %q", explicit.ForwardTargetModel)
	}

	preSlug = loadBackfilledProvider(t, preSlug.ID)
	assertSlug(t, preSlug, "occupied")

	occupied = loadBackfilledProvider(t, occupied.ID)
	assertSlug(t, occupied, "taken")
}

// 空串改写策略按 known 补齐（老行 column 存在但为空）。
func TestBackfillForwardSettingsFillsEmptyRewrite(t *testing.T) {
	db := setupForwardBackfillDB(t)

	provider := Provider{Name: "empty-rewrite", Type: "openai"}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	// 模拟老行：列已存在且值为空串（Create 已写入空串，这里显式确认）。
	if err := db.Model(&Provider{}).Where("id = ?", provider.ID).Update("forward_rewrite", "").Error; err != nil {
		t.Fatalf("reset rewrite: %v", err)
	}

	if err := backfillForwardSettings(context.Background()); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	provider = loadBackfilledProvider(t, provider.ID)
	assertRewrite(t, provider, consts.ForwardRewriteKnown)
}
