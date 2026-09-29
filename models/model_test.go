package models

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestChatLogIndexes 固化分析聚合所依赖的索引。
//
// 这些索引若被误删或改列，查询不会报错、只会静默退化为全表扫描，
// 很难在日常使用中发现——所以用测试守住，而不是靠注释提醒。
func TestChatLogIndexes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idx.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	closeOnCleanup(t, db)

	if err := db.AutoMigrate(&ChatLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var names []string
	if err := db.Raw(
		"SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'chat_logs'",
	).Scan(&names).Error; err != nil {
		t.Fatalf("query indexes: %v", err)
	}

	got := make(map[string]bool, len(names))
	for _, n := range names {
		got[n] = true
	}

	want := []string{
		"idx_chat_logs_created_at",     // 范围过滤 + ORDER BY
		"idx_chat_logs_status_created", // 趋势与成功率
		"idx_chat_logs_key_created",    // 按 Key 下钻
		"idx_chat_logs_name_created",   // 按模型下钻
	}
	for _, name := range want {
		if !got[name] {
			t.Fatalf("缺少索引 %q，实有：%v", name, names)
		}
	}

	// 复���索引的列顺序决定它能否被 `WHERE 前导列 = ? AND created_at >= ?` 命中，
	// 因此不仅要有，还要顺序正确。前导列必须是等值过滤的那个字段。
	composites := map[string][]string{
		"idx_chat_logs_status_created": {"status", "created_at"},
		"idx_chat_logs_key_created":    {"auth_key_id", "created_at"},
		"idx_chat_logs_name_created":   {"name", "created_at"},
	}
	for index, wantCols := range composites {
		cols := indexColumns(t, db, index)
		if len(cols) != len(wantCols) {
			t.Fatalf("索引 %s 的列数应为 %d，实得 %v", index, len(wantCols), cols)
		}
		for i := range wantCols {
			if cols[i] != wantCols[i] {
				t.Fatalf("索引 %s 第 %d 列应为 %q，实得 %q（完整：%v）",
					index, i, wantCols[i], cols[i], cols)
			}
		}
	}
}

// indexColumns 按顺序返回某个索引包含的列名。
func indexColumns(t *testing.T, db *gorm.DB, index string) []string {
	t.Helper()

	var rows []struct {
		Seqno int    `gorm:"column:seqno"`
		Name  string `gorm:"column:name"`
	}
	if err := db.Raw("PRAGMA index_info(" + index + ")").Scan(&rows).Error; err != nil {
		t.Fatalf("PRAGMA index_info(%s): %v", index, err)
	}

	cols := make([]string, 0, len(rows))
	for _, r := range rows {
		cols = append(cols, r.Name)
	}
	return cols
}
