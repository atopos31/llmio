package models

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// 这个文件只盯一件事：`PrepareEmptyStorage` 对**新库**和**老库**分别做了什么。
//
// 它是整个存储层里唯一一处"看着像会动既有数据"的地方，所以它的判据必须一条条
// 钉死。判错的代价不对称：
//
//	在新库上少设一次 —— 白丢一个免费优化（事后还能 VACUUM 补救）。
//	在老库上多设一次 —— 一份 7 GiB 的库在启动时被重建，用户只是重启了一下。
//
// 所以下面每条"老库"用例的断言都是**同一个形状**：auto_vacuum 没变、文件没动。

// openRaw 开一条裸连接，绕开 models.DB 这个包级变量——
// 本文件的用例不该和别的用例共享库文件，也不该互相踩。
func openRaw(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path))
	if err != nil {
		t.Fatalf("打开 %s 失败：%v", path, err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// readAVOnFreshConnection 另开一条连接读 auto_vacuum。
//
// 必须新开：`PRAGMA auto_vacuum` 在同一条连接上读的是**连接自己的状态**，
// 只有新建连接才真的去读文件头。这正是"设了到底有没有落下"的判据。
func readAVOnFreshConnection(t *testing.T, path string) int64 {
	t.Helper()
	db := openRaw(t, path)
	var v int64
	if err := db.Raw(`PRAGMA auto_vacuum`).Row().Scan(&v); err != nil {
		t.Fatalf("读 auto_vacuum 失败：%v", err)
	}
	return v
}

// emptyDBPath 造一个**真正的空库**：文件存在、0 字节。
// 与 ensureDBFile 的产物一致。
func emptyDBPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "empty.db")
}

// ── 新库 ──────────────────────────────────────────────────────────────────

// 空库上设 pragma，之后建表，ptrmap 跟着表一起长出来。
// 判据是**新连接读回来也是 2**——不是"这句 SQL 没报错"。
func TestPrepareEmptyStorage_AppliesToEmptyDatabase(t *testing.T) {
	t.Setenv(StorageRebuildEnv, "")
	path := emptyDBPath(t)

	db := openRaw(t, path)
	applied, err := PrepareEmptyStorage(db)
	if err != nil {
		t.Fatalf("空库上不该失败：%v", err)
	}
	if !applied {
		t.Fatal("空库上应当设 pragma，实得 applied=false")
	}
	// 建一张表：ptrmap 要跟着第一张表长出来。
	if err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, body TEXT)`).Error; err != nil {
		t.Fatalf("建表失败：%v", err)
	}
	if got := readAVOnFreshConnection(t, path); got != AutoVacuumIncremental {
		t.Fatalf("新库应当是 INCREMENTAL，新连接读回来是 %d", got)
	}
}

// 空的定义是**页数为 0**，不是"文件不存在"。建过又删空的库也走这条路吗？
// 不——这里是反例：只要曾经有过页，就不再当空库看。
func TestPrepareEmptyStorage_NonEmptyDatabaseIsUntouched(t *testing.T) {
	t.Setenv(StorageRebuildEnv, "")
	path := emptyDBPath(t)

	db := openRaw(t, path)
	if err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, body TEXT)`).Error; err != nil {
		t.Fatalf("建表失败：%v", err)
	}
	if err := db.Exec(`INSERT INTO t (body) VALUES ('x')`).Error; err != nil {
		t.Fatalf("插行失败：%v", err)
	}

	applied, err := PrepareEmptyStorage(db)
	if err != nil {
		t.Fatalf("既有库上不该失败：%v", err)
	}
	if applied {
		t.Fatal("既有库上**不许**设 pragma——这正是老库的兼容性保证")
	}
	if got := readAVOnFreshConnection(t, path); got != AutoVacuumNone {
		t.Fatalf("既有库的 auto_vacuum 被改了：%d", got)
	}
}

// 这条是**安全保证本身**，不是它的推论：非空库上那句 pragma 是**静默无效**的。
//
// 也就是说，就算将来有人把 PrepareEmptyStorage 误接到了既有库上，
// 后果也只是白跑一句 SQL——而不是一次 60 秒的重建。
// 这条实测是整个"不影响最老的库"结论的地基，所以直接钉住它。
func TestAutoVacuumPragmaIsNoopOnNonEmptyDatabase(t *testing.T) {
	path := emptyDBPath(t)

	db := openRaw(t, path)
	if err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, body TEXT)`).Error; err != nil {
		t.Fatalf("建表失败：%v", err)
	}
	if err := db.Exec(`INSERT INTO t (body) VALUES ('x')`).Error; err != nil {
		t.Fatalf("插行失败：%v", err)
	}

	// 绕过 PrepareEmptyStorage 的页数判据，直接设——模拟"判据哪天被写错了"。
	if err := db.Exec(`PRAGMA auto_vacuum = 2`).Error; err != nil {
		t.Fatalf("设 pragma 本身不该报错：%v", err)
	}
	if got := readAVOnFreshConnection(t, path); got != AutoVacuumNone {
		t.Fatalf("非空库上设 pragma 应当是**静默无效**的，实得 %d", got)
	}
}

// ── 开关 ──────────────────────────────────────────────────────────────────

// `off` 是"一概不碰"，空库也一样。
func TestPrepareEmptyStorage_OffDoesNothingEvenOnEmptyDatabase(t *testing.T) {
	t.Setenv(StorageRebuildEnv, "off")
	path := emptyDBPath(t)

	db := openRaw(t, path)
	applied, err := PrepareEmptyStorage(db)
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	if applied {
		t.Fatal("off 下不该设 pragma")
	}
	if err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, body TEXT)`).Error; err != nil {
		t.Fatalf("建表失败：%v", err)
	}
	if got := readAVOnFreshConnection(t, path); got != AutoVacuumNone {
		t.Fatalf("off 下的库应当保持默认，实得 %d", got)
	}
}

// 拼错的值（"true" / "yes" / "ON" / "1"）一律当 auto。
//
// 这一条防的是**最坏的那类错误**：用户想表达"要开"，写了个程序看不懂的值，
// 程序若把它当 on，7 GiB 的老库就在启动时重建了；当 auto 则只在空库上生效，
// 最坏后果是"新库没开成"，下次改对就行。边界必须往小的一侧倒。
func TestStorageRebuildMode_UnknownValuesFallBackToAuto(t *testing.T) {
	for _, v := range []string{"", "true", "yes", "ON", "1", "enable", "auto "} {
		t.Run("值="+v, func(t *testing.T) {
			t.Setenv(StorageRebuildEnv, v)
			if got := StorageRebuildMode(); got != "auto" {
				t.Fatalf("非法值 %q 应当当 auto，实得 %q", v, got)
			}
		})
	}
	for _, v := range []string{"off", "on"} {
		t.Run("值="+v, func(t *testing.T) {
			t.Setenv(StorageRebuildEnv, v)
			if got := StorageRebuildMode(); got != v {
				t.Fatalf("%q 应当原样返回，实得 %q", v, got)
			}
		})
	}
}

// 默认（环境变量没设）必须是 auto：这是"老库不受影响"的默认姿态。
func TestStorageRebuildMode_DefaultsToAuto(t *testing.T) {
	t.Setenv(StorageRebuildEnv, "")
	if got := StorageRebuildMode(); got != "auto" {
		t.Fatalf("默认应当是 auto，实得 %q", got)
	}
}

// 三个常量就是 SQLite PRAGMA 的定义，改动它们等于改变了 SQLite 的语义。
func TestAutoVacuumConstantsMatchSQLite(t *testing.T) {
	if AutoVacuumNone != 0 || AutoVacuumFull != 1 || AutoVacuumIncremental != 2 {
		t.Fatalf("auto_vacuum 常量与 SQLite 定义不符：%d/%d/%d",
			AutoVacuumNone, AutoVacuumFull, AutoVacuumIncremental)
	}
}

// ReadAutoVacuum 读的就是文件头那个值——与另开连接读到的一致。
func TestReadAutoVacuum(t *testing.T) {
	path := emptyDBPath(t)
	db := openRaw(t, path)
	if err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY)`).Error; err != nil {
		t.Fatalf("建表失败：%v", err)
	}
	got, err := ReadAutoVacuum(db)
	if err != nil {
		t.Fatalf("读失败：%v", err)
	}
	if got != readAVOnFreshConnection(t, path) {
		t.Fatalf("ReadAutoVacuum 与另开连接读到的值不一致：%d", got)
	}
}
