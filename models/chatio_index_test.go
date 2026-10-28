package models

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// openTestDB 起一个临时库，并把包级 DB 指过去。
//
// 用真实的 Init 而不是手写 AutoMigrate：EnsureChatIOLogIDIndex 是**启动路径上
// 的一步**，只有走一遍真实初始化，"新装的服务有没有这个索引"才是真的被测了。
func openTestDB(t *testing.T) {
	t.Helper()
	prev := DB
	prevPath := DBPath
	Init(context.Background(), filepath.Join(t.TempDir(), "chatio-idx.db"))
	t.Cleanup(func() {
		if DB != nil {
			if sqlDB, err := DB.DB(); err == nil {
				_ = sqlDB.Close()
			}
		}
		DB = prev
		DBPath = prevPath
	})
}

// TestEnsureChatIOLogIDIndex_Created 新库初始化之后必须带上 log_id 索引。
//
// 这条不是"顺手固化一个索引名"：没有它，回填响应体那条 UPDATE 会走
// idx_chat_ios_deleted_at——在软删占比极低的库上，那等于**扫整张表**。
// 真机实测单次 0.9 秒，而这句话跑在**每个开了 io_log 的请求**上。
// 索引被误删或忘了建时，查询不会报错、只会静默退化，所以用测试守住。
func TestEnsureChatIOLogIDIndex_Created(t *testing.T) {
	openTestDB(t)

	if !indexExists(t, DB, "idx_chat_ios_log_id") {
		t.Fatal("chat_ios.log_id 上没有 idx_chat_ios_log_id：回填响应体会退化为全表扫描")
	}
}

// TestEnsureChatIOLogIDIndex_Idempotent 重复调用不能报错，也不能重建。
//
// 它在每次启动都跑一遍，所以"已存在就立刻返回"是它的一半职责。
// 重建一次在真机大表上是秒级的写锁持有，重复执行必须是无事发生。
func TestEnsureChatIOLogIDIndex_Idempotent(t *testing.T) {
	openTestDB(t)

	before := chatIOLogIDIndexRootpage(t, DB)
	for i := 0; i < 3; i++ {
		if err := EnsureChatIOLogIDIndex(context.Background()); err != nil {
			t.Fatalf("第 %d 次重复调用失败：%v", i+1, err)
		}
	}
	after := chatIOLogIDIndexRootpage(t, DB)

	if before == 0 {
		t.Fatal("索引不存在，这条用例的前提不成立")
	}
	if before != after {
		t.Fatalf("rootpage 变了（%d → %d），重复调用把索引重建了一遍", before, after)
	}
}

// TestEnsureChatIOLogIDIndex_AddsToExistingTable 存量库也要补上这条索引。
//
// 与"新库"分开测，是因为两者的路径不同：新库靠 Init 里的建表，
// 存量库只能靠 EnsureChatIOLogIDIndex 自己补。它是先查 sqlite_master
// 再建，所以这里先手动删掉索引，验证它能补回来。
func TestEnsureChatIOLogIDIndex_AddsToExistingTable(t *testing.T) {
	openTestDB(t)

	ctx := context.Background()
	if err := DB.Exec("DROP INDEX idx_chat_ios_log_id").Error; err != nil {
		t.Fatalf("准备存量库（删索引）失败：%v", err)
	}
	if indexExists(t, DB, "idx_chat_ios_log_id") {
		t.Fatal("前置条件不成立：索引还在")
	}

	// 存量数据：索引要能建在一张已经有行的表上。
	if err := DB.Exec(`INSERT INTO chat_ios (created_at, updated_at, log_id, input)
		VALUES (datetime('now'), datetime('now'), 1, 'x')`).Error; err != nil {
		t.Fatalf("塞存量行失败：%v", err)
	}

	if err := EnsureChatIOLogIDIndex(ctx); err != nil {
		t.Fatalf("给存量库补索引失败：%v", err)
	}
	if !indexExists(t, DB, "idx_chat_ios_log_id") {
		t.Fatal("存量库没有被补上 idx_chat_ios_log_id")
	}
}

// TestEnsureChatIOLogIDIndex_ReplacesWrongDefinition 同名但定义不对的索引要重建。
//
// 只按名字判"已存在"会漏掉这种情况：早先手工热修留下的 partial 索引，或列序
// 不同的同名索引，对 `log_id = ? AND deleted_at IS NULL` 未必可用。那种库上
// 查询不会报错、只会继续全表扫——正是这次要消灭的形态，而且不会有任何告警。
func TestEnsureChatIOLogIDIndex_ReplacesWrongDefinition(t *testing.T) {
	openTestDB(t)

	ctx := context.Background()
	if err := DB.Exec("DROP INDEX idx_chat_ios_log_id").Error; err != nil {
		t.Fatalf("准备库失败：%v", err)
	}
	// 同名、partial、列序反过来：三种不对叠在一起。
	if err := DB.Exec(
		"CREATE INDEX idx_chat_ios_log_id ON chat_ios (deleted_at, log_id) WHERE deleted_at IS NULL",
	).Error; err != nil {
		t.Fatalf("造同名错索引失败：%v", err)
	}

	if err := EnsureChatIOLogIDIndex(ctx); err != nil {
		t.Fatalf("修正索引失败：%v", err)
	}

	var list []struct {
		Name    string
		Partial int
	}
	if err := DB.Raw("PRAGMA index_list('chat_ios')").Scan(&list).Error; err != nil {
		t.Fatalf("读 index_list 失败：%v", err)
	}
	found := false
	for _, idx := range list {
		if idx.Name != chatIOLogIDIndexName {
			continue
		}
		found = true
		if idx.Partial != 0 {
			t.Fatal("索引仍是 partial：log_id 等值查找未必走它")
		}
	}
	if !found {
		t.Fatal("修正之后索引不存在")
	}

	var cols []struct {
		Name string
	}
	if err := DB.Raw("PRAGMA index_info('" + chatIOLogIDIndexName + "')").Scan(&cols).Error; err != nil {
		t.Fatalf("读 index_info 失败：%v", err)
	}
	if len(cols) != 1 || cols[0].Name != "log_id" {
		t.Fatalf("索引列 = %v，want 恰好 [log_id]", cols)
	}
}

// TestChatIOUpdateUsesLogIDIndex 是这条索引真正要保住的东西：
// 回填响应体的那句 UPDATE 必须走 log_id 索引，而不是走 deleted_at。
//
// 断言的是**执行计划**而不是耗时：耗时在慢磁盘与 CI runner 上会抖，
// 而"选了哪个索引"是确定的。这条用例锁住的正是 2026-10-28 那次
// 5895ms SLOW SQL 的成因。
func TestChatIOUpdateUsesLogIDIndex(t *testing.T) {
	openTestDB(t)

	if err := DB.Exec(`INSERT INTO chat_ios (created_at, updated_at, log_id, input)
		VALUES (datetime('now'), datetime('now'), 4242, 'x')`).Error; err != nil {
		t.Fatalf("塞行失败：%v", err)
	}

	var plan []struct {
		Detail string
	}
	// 与 service.updateChatIOOutput 发出的那条语句同形。
	if err := DB.Raw(
		`EXPLAIN QUERY PLAN UPDATE chat_ios SET updated_at = ?
		 WHERE log_id = ? AND deleted_at IS NULL`,
		"2026-10-28 00:00:00", 4242,
	).Scan(&plan).Error; err != nil {
		t.Fatalf("取执行计划失败：%v", err)
	}

	var joined string
	for _, p := range plan {
		joined += p.Detail + "\n"
	}
	if !strings.Contains(joined, "idx_chat_ios_log_id") {
		t.Fatalf("回填响应体没有走 log_id 索引，执行计划：\n%s", joined)
	}
}

// TestInitDoesNotRebuildChatIOs 守的是这条索引真正的风险面：**不能重建表**。
//
// 需要澄清一个容易想当然的点：重建表**不会**因为"给列加索引"而发生。
// AutoMigrate 对索引走的是 HasIndex → CreateIndex，是原地建索引；只有列
// 类型 / 可空性 / 主键变化才会落到 SQLite 的"建新表-拷数据-删旧表-改名"。
// 所以这条用例实际守的是两件事：启动路径只做了原地建索引（rootpage 不变），
// 以及以后有人改 chat_ios 的列定义时会被这里拦下。
//
// serializer_test.go 的 C1 守的是"列定义没变、既有索引没被删"；这里补上
// 另一半：**索引加上了，而表还是那张表**。判据用 rootpage——表被重建过一定换页。
func TestInitDoesNotRebuildChatIOs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rebuild-guard.db")
	// 用上游真实建库语句造库（不是手写 DDL）：要证明的正是
	// "在真机上那张老表上跑一次 Init，表不会被重建"。
	openLegacyDB(t, path)

	seedRoot := rawTableRootpage(t, path, "chat_ios")

	// 走一遍真实的启动迁移。
	prev := DB
	prevPath := DBPath
	Init(context.Background(), path)
	t.Cleanup(func() {
		if DB != nil {
			if sqlDB, err := DB.DB(); err == nil {
				_ = sqlDB.Close()
			}
		}
		DB = prev
		DBPath = prevPath
	})

	if !indexExists(t, DB, "idx_chat_ios_log_id") {
		t.Fatal("启动之后没有 idx_chat_ios_log_id")
	}
	if got := tableRootpage(t, DB, "chat_ios"); got != seedRoot {
		t.Fatalf("chat_ios 的 rootpage 从 %d 变成了 %d：表被重建过（在 7 GB 生产库上是灾难）",
			seedRoot, got)
	}

	// 种子行必须还在，且还是原样——重建会连数据一起搬，但也要确认没丢。
	var input string
	if err := DB.Raw("SELECT input FROM chat_ios WHERE log_id = 1").Row().Scan(&input); err != nil {
		t.Fatalf("读种子行失败：%v", err)
	}
	if input != `{"model":"gpt-4o"}` {
		t.Fatalf("种子行的 input = %q，want %s", input, `{"model":"gpt-4o"}`)
	}
}

// rawTableRootpage 用裸连接打开库（不经 Init），只为在 Init 之前读一次结构。
func rawTableRootpage(t *testing.T, path, table string) int64 {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("裸开库失败：%v", err)
	}
	closeOnCleanup(t, db)
	return tableRootpage(t, db, table)
}

// tableRootpage 读表的 rootpage。表被重建（建新表-拷数据-删旧表-改名）一定换页。
func tableRootpage(t *testing.T, db *gorm.DB, table string) int64 {
	t.Helper()
	var rootpage int64
	if err := db.Raw(
		"SELECT rootpage FROM sqlite_master WHERE type = 'table' AND name = ?", table,
	).Scan(&rootpage).Error; err != nil {
		t.Fatalf("读 %s 的 rootpage 失败：%v", table, err)
	}
	return rootpage
}

// chatIOLogIDIndexRootpage 读索引的 rootpage，用来判断"有没有被重建"。
func chatIOLogIDIndexRootpage(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var rootpage int64
	if err := db.Raw(
		"SELECT rootpage FROM sqlite_master WHERE type = 'index' AND name = ?",
		"idx_chat_ios_log_id",
	).Scan(&rootpage).Error; err != nil {
		t.Fatalf("读索引 rootpage 失败：%v", err)
	}
	return rootpage
}
