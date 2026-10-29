package models

import (
	"context"
	"path/filepath"
	"testing"
)

// initModelAt 在指定路径上走一次真实初始化（= 真机启动），
// 并在测试结束时关连接、还原包级状态。
func initModelAt(t *testing.T, path string) {
	t.Helper()
	prevDB, prevPath := DB, DBPath
	closeCurrentDB(t)
	Init(context.Background(), path)
	t.Cleanup(func() {
		if DB != nil {
			if sqlDB, err := DB.DB(); err == nil {
				_ = sqlDB.Close()
			}
		}
		DB, DBPath = prevDB, prevPath
	})
}

// chat_logs 的每一条删除路径都是 Unscoped 硬删除，deleted_at 恒为 NULL。
// 这条索引因此命中整张表，却会被查询计划器优先选中（详见 DropChatLogsDeletedAtIndex）。
func TestFreshDBCreatesNoChatLogsDeletedAtIndex(t *testing.T) {
	initModelAt(t, filepath.Join(t.TempDir(), "fresh.db"))

	if indexExists(t, DB, "idx_chat_logs_deleted_at") {
		t.Fatal("新库上又建出了 idx_chat_logs_deleted_at：它会把分析页取数逼成全表扫")
	}
	// 该留的索引不能被顺手弄丢：时间窗取数靠它
	if !indexExists(t, DB, "idx_chat_logs_created_at") {
		t.Fatal("idx_chat_logs_created_at 不见了：分析页取数会退化成全表扫")
	}
}

// 存量库（老版本建的）上的旧索引要在启动迁移里被摘掉，且摘掉时不动数据。
func TestInitDropsLegacyChatLogsDeletedAtIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-deleted-at-idx.db")
	initModelAt(t, path)

	// 模拟老版本：标签还在时 GORM 建出来的就是这条
	if err := DB.Exec("CREATE INDEX idx_chat_logs_deleted_at ON chat_logs(deleted_at)").Error; err != nil {
		t.Fatalf("造旧索引失败：%v", err)
	}
	if err := DB.Exec(`INSERT INTO chat_logs (created_at, name, status) VALUES ("2026-10-29 00:00:00", "m", "success")`).Error; err != nil {
		t.Fatalf("造种子行失败：%v", err)
	}

	// 真机上就是"升级镜像后重启"这一步
	closeCurrentDB(t)
	Init(context.Background(), path)

	if indexExists(t, DB, "idx_chat_logs_deleted_at") {
		t.Fatal("升级之后旧索引还在：分析页取数会继续全表扫")
	}
	var n int64
	if err := DB.Raw(`SELECT COUNT(*) FROM chat_logs`).Scan(&n).Error; err != nil {
		t.Fatalf("查行数失败：%v", err)
	}
	if n != 1 {
		t.Fatalf("摘索引动了数据：行数 %d，want 1", n)
	}
}

// 迁移函数必须可重复调用：每次启动都会跑。
func TestDropChatLogsDeletedAtIndexIsIdempotent(t *testing.T) {
	initModelAt(t, filepath.Join(t.TempDir(), "idempotent.db"))
	ctx := context.Background()

	for i := range 2 {
		if err := DropChatLogsDeletedAtIndex(ctx); err != nil {
			t.Fatalf("第 %d 次调用失败：%v", i+1, err)
		}
	}
	if indexExists(t, DB, "idx_chat_logs_deleted_at") {
		t.Fatal("调用之后索引仍然存在")
	}
}
