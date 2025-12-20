package models

import (
	"context"
	"os"
	"path/filepath"

	"github.com/atopos31/llmio/consts"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var DB *gorm.DB

func Init(ctx context.Context, path string) {
	if err := ensureDBFile(path); err != nil {
		panic(err)
	}
	db, err := gorm.Open(sqlite.Open(path))
	if err != nil {
		panic(err)
	}
	DB = db
	if err := db.AutoMigrate(
		&Provider{},
		&Model{},
		&ModelWithProvider{},
		&ChatLog{},
		&ChatIO{},
		&Config{},
		&AuthKey{},
	); err != nil {
		panic(err)
	}
	// 兼容性考虑
	if _, err := gorm.G[ModelWithProvider](DB).Where("status IS NULL").Update(ctx, "status", true); err != nil {
		panic(err)
	}
	if _, err := gorm.G[ModelWithProvider](DB).Where("customer_headers IS NULL").Updates(ctx, ModelWithProvider{
		CustomerHeaders: map[string]string{},
	}); err != nil {
		panic(err)
	}
	if _, err := gorm.G[Model](DB).Where("strategy = '' OR strategy IS NULL").Update(ctx, "strategy", consts.BalancerDefault); err != nil {
		panic(err)
	}
	if _, err := gorm.G[Model](DB).Where("breaker IS NULL").Update(ctx, "breaker", false); err != nil {
		panic(err)
	}
	if _, err := gorm.G[ChatLog](DB).Where("auth_key_id IS NULL").Update(ctx, "auth_key_id", 0); err != nil {
		panic(err)
	}
	// 启动时执行 VACUUM 回收空间
	if err := db.Exec("VACUUM").Error; err != nil {
		panic(err)
	}
}

func ensureDBFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}
