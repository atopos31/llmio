package sqlitex

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/glebarez/go-sqlite"
)

// 本文件钉住线上那次"整库死锁到重启"的根因，以及本包的修复。
//
// 复现的形状（与生产同一份驱动、同一种回滚日志模式）：
//
//	读者池：BEGIN + SELECT，一直持着 SHARED（模拟一次慢查询）
//	写者池：BEGIN + INSERT（拿到 RESERVED），COMMIT 时等不到 EXCLUSIVE，
//	        等满 busy_timeout 返回 SQLITE_BUSY——**事务此时仍然开着**
//
// 原生驱动下，database/sql 会把这条带着未结束事务的连接放回池子：
// 下一次 Begin 直接报 "cannot start a transaction within a transaction (1)"，
// 而且那条事务的锁一直不放——线上所有读写都等满 5 秒然后 500。
//
// 受保护驱动下，COMMIT 失败会先自己 ROLLBACK，连接干净地回池，下一笔事务正常。

func openPool(t *testing.T, driverName, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		t.Fatalf("打开 %s 失败：%v", driverName, err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

// commitBusy 制造一次"COMMIT 撞 BUSY"，返回写者池。读者事务由调用方收尾。
func commitBusy(t *testing.T, writer, reader *sql.DB) {
	t.Helper()

	if _, err := writer.Exec(`CREATE TABLE IF NOT EXISTS t (x INTEGER)`); err != nil {
		t.Fatalf("建表失败：%v", err)
	}

	readerTx, err := reader.Begin()
	if err != nil {
		t.Fatalf("读者 Begin 失败：%v", err)
	}
	var n int
	// 一句读就让这条连接持住 SHARED，直到它回滚。
	if err := readerTx.QueryRow(`SELECT count(*) FROM t`).Scan(&n); err != nil {
		t.Fatalf("读者查询失败：%v", err)
	}

	writerTx, err := writer.Begin()
	if err != nil {
		t.Fatalf("写者 Begin 失败：%v", err)
	}
	if _, err := writerTx.Exec(`INSERT INTO t (x) VALUES (1)`); err != nil {
		t.Fatalf("写者 INSERT 失败：%v", err)
	}
	commitErr := writerTx.Commit()
	if commitErr == nil {
		t.Fatalf("期望 COMMIT 撞上读者的 SHARED 报 SQLITE_BUSY，实际成功")
	}
	if !strings.Contains(commitErr.Error(), "SQLITE_BUSY") && !strings.Contains(commitErr.Error(), "database is locked") {
		t.Fatalf("期望 COMMIT 报锁冲突，实际：%v", commitErr)
	}
	if err := readerTx.Rollback(); err != nil {
		t.Fatalf("读者回滚失败：%v", err)
	}
}

func TestGuardedDriver_CommitBusyDoesNotPoisonConn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guarded.db")
	Register()

	writer := openPool(t, DriverName, path+"?_pragma=busy_timeout(150)")
	reader := openPool(t, "sqlite", path+"?_pragma=busy_timeout(150)")

	commitBusy(t, writer, reader)

	// 修复的核心断言：撞过 BUSY 之后，池里那条连接仍然能开新事务并写完。
	// 原生驱动在这里会报 "cannot start a transaction within a transaction (1)"。
	for i := 0; i < 3; i++ {
		tx, err := writer.Begin()
		if err != nil {
			t.Fatalf("第 %d 次 Begin 失败（连接被泄漏的事务污染了）：%v", i+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO t (x) VALUES (?)`, i+2); err != nil {
			t.Fatalf("第 %d 次 INSERT 失败：%v", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("第 %d 次 COMMIT 失败：%v", i+1, err)
		}
	}

	var got int
	if err := writer.QueryRow(`SELECT count(*) FROM t`).Scan(&got); err != nil {
		t.Fatalf("收尾查询失败：%v", err)
	}
	// 撞 BUSY 的那一笔必须整笔回滚（不是"半行"）：库里的 3 行只可能来自后面
	// 三次成功提交。多出来的那一行说明失败的事务其实被提交了一部分——那是最糟的形状。
	if got != 3 {
		t.Fatalf("期望 3 行（失败那笔已完整回滚 + 3 次成功提交），实际 %d", got)
	}
}

// 对照：原生驱动必须复现出用户看到的那句话。这条不是"测别人的 bug"，
// 而是证明上面的修复确实针对了一个真实存在的失败路径。
func TestStockDriver_CommitBusyPoisonsConn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stock.db")
	writer := openPool(t, "sqlite", path+"?_pragma=busy_timeout(150)")
	reader := openPool(t, "sqlite", path+"?_pragma=busy_timeout(150)")

	commitBusy(t, writer, reader)

	_, err := writer.Begin()
	if err == nil {
		t.Fatalf("原生驱动下期望下一次 Begin 报 cannot start a transaction within a transaction，实际成功——" +
			"这条测试没抓住它要抓的路径")
	}
	if !strings.Contains(err.Error(), "cannot start a transaction within a transaction") {
		t.Fatalf("期望 BEGIN 报 'cannot start a transaction within a transaction'，实际：%v", err)
	}
}

func TestIsNoActiveTx(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, true},
		{"标准说法", errString("cannot rollback - no transaction is active"), true},
		{"别的说法", errString("database is locked (5) (SQLITE_BUSY)"), false},
	} {
		if got := isNoActiveTx(tc.err); got != tc.want {
			t.Fatalf("%s：期望 %v，实际 %v", tc.name, tc.want, got)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }
