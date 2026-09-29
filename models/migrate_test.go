package models

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// 本文件验证**从上游旧库升级到本分支**的路径。
//
// 为什么必须单独测：本分支对 ChatLog 做了三处结构性改动——
//   1. 展开内嵌的 gorm.Model（改为显式 ID/CreatedAt/UpdatedAt/DeletedAt）
//   2. 新增 peak_period 列
//   3. 新增 4 个索引（含 3 个复合）
//
// 新库上 AutoMigrate 总能建对，所以"新库能跑"证明不了升级路径可用。
// 真实风险是：AutoMigrate 在已有表上做表重建、丢数据，或索引没建上。
// 因此这里手工造一个**上游形态的旧库**，跑一遍 Init，再逐项断言。

// legacyChatLogsDDL 是升级前 chat_logs 的表结构（内嵌 gorm.Model，
// 无 peak_period，无本分支新增的复合索引）。
const legacyChatLogsDDL = `
CREATE TABLE chat_logs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	created_at DATETIME,
	updated_at DATETIME,
	deleted_at DATETIME,
	name TEXT,
	trace_id TEXT,
	provider_model TEXT,
	provider_name TEXT,
	status TEXT,
	style TEXT,
	user_agent TEXT,
	remote_ip TEXT,
	auth_key_id INTEGER,
	session_id TEXT,
	chat_io NUMERIC,
	error TEXT,
	retry INTEGER,
	proxy_time INTEGER,
	first_chunk_time INTEGER,
	chunk_time INTEGER,
	tps REAL,
	size INTEGER,
	prompt_tokens INTEGER,
	completion_tokens INTEGER,
	total_tokens INTEGER,
	prompt_tokens_details TEXT,
	input_price REAL,
	cache_read_price REAL,
	output_price REAL,
	currency TEXT
)`

// openLegacyDB 建一个旧形态的库并塞入一条历史数据。
func openLegacyDB(t *testing.T, path string) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if err := db.Exec(legacyChatLogsDDL).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}

	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := db.Exec(`
		INSERT INTO chat_logs
			(created_at, updated_at, name, trace_id, provider_model, provider_name, status,
			 style, user_agent, remote_ip, auth_key_id, session_id, chat_io, retry,
			 proxy_time, first_chunk_time, chunk_time, tps, size,
			 prompt_tokens, completion_tokens, total_tokens, prompt_tokens_details,
			 input_price, cache_read_price, output_price, currency)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		created, created, "gpt-4o", "trace-abc", "gpt-4o-2024", "prov-a", "success",
		"openai", "claude-cli/1.0", "127.0.0.1", 0, "sess-1", true, 0,
		int64(1200*time.Millisecond), int64(300*time.Millisecond), int64(900*time.Millisecond), 42.5, 2048,
		100, 50, 150, `{"cached_tokens":20}`,
		1.5, 0.3, 6.0, "CNY",
	).Error; err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

func TestInit_UpgradesLegacySchemaInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	openLegacyDB(t, path)

	// 跑一次真实的启动初始化
	Init(context.Background(), path)
	closeOnCleanup(t, DB)

	t.Run("历史数据完整保留", func(t *testing.T) {
		// 这条断言守的是最坏情况：AutoMigrate 走了表重建路径却没搬数据。
		// 在 SQLite 上 GORM 会为"列定义变化"重建表，一旦实现有变就可能丢行。
		var count int64
		if err := DB.Model(&ChatLog{}).Count(&count).Error; err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 1 {
			t.Fatalf("历史数据应完整保留，实得 %d 行", count)
		}

		var row ChatLog
		if err := DB.First(&row).Error; err != nil {
			t.Fatalf("load: %v", err)
		}
		// 逐字段核对，确认没有被重建过程改写
		if row.Name != "gpt-4o" || row.TraceID != "trace-abc" || row.ProviderName != "prov-a" {
			t.Fatalf("业务字段被改写：%+v", row)
		}
		if row.Status != "success" || row.Currency != "CNY" {
			t.Fatalf("状态/币种被改写：%+v", row)
		}
		if row.PromptTokens != 100 || row.CompletionTokens != 50 || row.TotalTokens != 150 {
			t.Fatalf("token 字段被改写：%+v", row)
		}
		if row.PromptTokensDetails.CachedTokens != 20 {
			t.Fatalf("JSON 详情列被改写：%+v", row.PromptTokensDetails)
		}
		if row.InputPrice != 1.5 || row.OutputPrice != 6.0 {
			t.Fatalf("价格快照被改写：%+v", row)
		}
		if row.Tps != 42.5 || row.Size != 2048 {
			t.Fatalf("性能字段被改写：%+v", row)
		}
		// 主键与时间戳必须保住（展开 gorm.Model 后仍是同名同义列）
		if row.ID == 0 {
			t.Fatal("主键丢失")
		}
		if !row.CreatedAt.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
			t.Fatalf("created_at 被改写：%v", row.CreatedAt)
		}
	})

	t.Run("新列已补齐且默认安全", func(t *testing.T) {
		var row ChatLog
		if err := DB.First(&row).Error; err != nil {
			t.Fatalf("load: %v", err)
		}
		// 旧数据没有时段概念，升级后应为空串（即按基础价计费），
		// 而不是被凭空赋一个时段名
		if row.PeakPeriod != "" {
			t.Fatalf("历史行的 peak_period 应为空，实得 %q", row.PeakPeriod)
		}
	})

	t.Run("新增索引全部建成", func(t *testing.T) {
		for _, idx := range []string{
			"idx_chat_logs_created_at",
			"idx_chat_logs_status_created",
			"idx_chat_logs_key_created",
			"idx_chat_logs_name_created",
		} {
			if !indexExists(t, DB, idx) {
				t.Fatalf("升级后缺少索引 %s", idx)
			}
		}
	})

	t.Run("升级后仍可正常写入与查询", func(t *testing.T) {
		// 用新字段写一条，确认新旧行在同表里共存且都可读
		fresh := ChatLog{
			CreatedAt:    time.Now(),
			Name:         "claude-3",
			Status:       "success",
			ProviderName: "prov-b",
			Currency:     "CNY",
			PeakPeriod:   "夜间优惠",
			Usage:        Usage{PromptTokens: 10, TotalTokens: 10},
		}
		if err := DB.Create(&fresh).Error; err != nil {
			t.Fatalf("create: %v", err)
		}

		var all []ChatLog
		if err := DB.Order("id ASC").Find(&all).Error; err != nil {
			t.Fatalf("find: %v", err)
		}
		if len(all) != 2 {
			t.Fatalf("应有 2 行，实得 %d", len(all))
		}
		if all[0].PeakPeriod != "" || all[1].PeakPeriod != "夜间优惠" {
			t.Fatalf("新旧行的 peak_period 不符：%q / %q", all[0].PeakPeriod, all[1].PeakPeriod)
		}
	})
}

// TestInit_UpgradeIsIdempotent 重复启动不应重复建索引或改写数据。
func TestInit_UpgradeIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	openLegacyDB(t, path)

	Init(context.Background(), path)
	closeCurrentDBOnCleanup(t)

	var first ChatLog
	if err := DB.First(&first).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	firstUpdatedAt := first.UpdatedAt

	// 关掉第一个连接再跑第二次，否则前一个连接会占着库文件
	closeCurrentDB(t)

	// 再跑一次（模拟二次启动）
	Init(context.Background(), path)

	var second ChatLog
	if err := DB.First(&second).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if second.Name != first.Name || second.TotalTokens != first.TotalTokens {
		t.Fatalf("二次启动改写了数据：%+v", second)
	}
	// 行数不应增加
	var count int64
	if err := DB.Model(&ChatLog{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("二次启动后行数应为 1，实得 %d", count)
	}
	_ = firstUpdatedAt
}

// TestInit_FreshDatabaseStillWorks 新库路径不能被升级逻辑带坏。
func TestInit_FreshDatabaseStillWorks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")

	Init(context.Background(), path)
	closeOnCleanup(t, DB)

	if !indexExists(t, DB, "idx_chat_logs_status_created") {
		t.Fatal("新库也应建成复合索引")
	}
	if err := DB.Create(&ChatLog{Name: "m", Status: "success"}).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
}

// closeCurrentDB 关闭当前包级连接。
//
// 多次调用 Init 的测试必须在两次之间调用它：Init 每次都会换一个新的
// 包级连接，前一个若不关就一直占着文件，Windows 上 t.TempDir 清理会失败。
func closeCurrentDB(t *testing.T) {
	t.Helper()
	if DB == nil {
		return
	}
	if sqlDB, err := DB.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// closeCurrentDBOnCleanup 把 closeCurrentDB 注册为清理步骤。
func closeCurrentDBOnCleanup(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { closeCurrentDB(t) })
}

// indexExists 查询 sqlite_master 判断索引是否存在。
func indexExists(t *testing.T, db *gorm.DB, name string) bool {
	t.Helper()
	var n int64
	if err := db.Raw(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?", name,
	).Scan(&n).Error; err != nil {
		t.Fatalf("query index %s: %v", name, err)
	}
	return n > 0
}
