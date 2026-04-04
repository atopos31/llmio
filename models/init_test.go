package models

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestInit_BackfillsAuthKeyIOLogToFalse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "llmio.db")

	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open seed database: %v", err)
	}

	if err := db.Exec(`
		CREATE TABLE auth_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME,
			updated_at DATETIME,
			deleted_at DATETIME,
			name TEXT,
			key TEXT,
			status NUMERIC,
			allow_all NUMERIC,
			models TEXT,
			expires_at DATETIME,
			usage_count INTEGER,
			last_used_at DATETIME
		)
	`).Error; err != nil {
		t.Fatalf("failed to create legacy auth_keys table: %v", err)
	}

	if err := db.Exec(`INSERT INTO auth_keys (name, key, status, allow_all, usage_count) VALUES (?, ?, ?, ?, ?)`,
		"legacy-project", "legacy-key", true, true, 0,
	).Error; err != nil {
		t.Fatalf("failed to seed legacy auth key: %v", err)
	}

	Init(context.Background(), path)

	authKey, err := gorm.G[AuthKey](DB).Where("key = ?", "legacy-key").First(context.Background())
	if err != nil {
		t.Fatalf("failed to load migrated auth key: %v", err)
	}

	if authKey.IOLog == nil {
		t.Fatal("expected io_log to be backfilled")
	}
	if *authKey.IOLog {
		t.Fatal("expected io_log default to false")
	}
}
