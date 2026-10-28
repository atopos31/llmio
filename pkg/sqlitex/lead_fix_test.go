package sqlitex

// 这是独立验证者加的回归测试，**不修改**实现文件。
// 它比包内既有测试多覆盖三件事：
//  1. COMMIT 撞 BUSY 之后，除了"下一次 Begin 能成功"，还要证明**干净连接池的读写
//     立刻恢复**（原 bug 的真实症状是整库读也锁死到重启）；
//  2. 驱动 Rollback 失败时 guardedTx 返回 driver.ErrBadConn，且 database/sql 真的
//     丢弃了那条连接（用假驱动数 Open/Close 次数，不依赖真驱动"恰好"不会失败）；
//  3. BeginTx / ReadOnly / Prepare / Exec / Query / Ping 这些转发没有因为包装而丢失。

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

const verifyBusyMS = 150

// verifyHangBound 是"卡死"的上限，与 busy_timeout 无关。
//
// 它的作用只是把"永远挂住"变成一条能读的失败信息，不是用来判定锁有没有放——
// 那个由语句能否执行成功来判定。给 10 倍 busy_timeout 是留足 CI runner 的
// 调度与磁盘抖动余量：实测一次抖动可以到 379ms，而真正泄漏事务时这两句会
// **直接报错**，不会走到这条分支。
const verifyHangBound = 10 * time.Duration(verifyBusyMS) * time.Millisecond

// ───────────────────────── ① 真驱动：整库不再被锁死 ─────────────────────────

func verifyOpen(t *testing.T, driverName, dsn string, maxOpen int) *sql.DB {
	t.Helper()
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		t.Fatalf("sql.Open(%q, %q): %v", driverName, dsn, err)
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxOpen)
	db.SetConnMaxLifetime(0)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestVerify_GuardedCommitBusyLeavesWholeDBUsable 复现原故障的那个窗口：
// 读者持 SHARED、写者 COMMIT 撞 BUSY。受保护驱动应当在 COMMIT 失败时把事务
// ROLLBACK 掉，于是：写者池下一次 Begin 正常；**别的池的读也不会被挡住**；
// 读者回滚后写立刻恢复。
func TestVerify_GuardedCommitBusyLeavesWholeDBUsable(t *testing.T) {
	Register()
	path := filepath.Join(t.TempDir(), "guarded-verify.db")
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(%d)&_pragma=journal_mode(delete)", filepath.ToSlash(path), verifyBusyMS)

	writer := verifyOpen(t, DriverName, dsn, 1)
	reader := verifyOpen(t, "sqlite", dsn, 1)
	clean := verifyOpen(t, DriverName, dsn, 1)

	if _, err := writer.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY AUTOINCREMENT, v TEXT NOT NULL)"); err != nil {
		t.Fatalf("create table: %v", err)
	}

	// 读者：SHARED 锁，直到回滚才放。
	readerTx, err := reader.Begin()
	if err != nil {
		t.Fatalf("reader.Begin: %v", err)
	}
	var n int
	if err := readerTx.QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatalf("reader.SELECT: %v", err)
	}

	// 写者：RESERVED -> COMMIT 需要 EXCLUSIVE -> BUSY。
	writerTx, err := writer.Begin()
	if err != nil {
		t.Fatalf("writer.Begin: %v", err)
	}
	if _, err := writerTx.Exec("INSERT INTO t(v) VALUES ('must-be-rolled-back')"); err != nil {
		t.Fatalf("writer.INSERT: %v", err)
	}
	start := time.Now()
	commitErr := writerTx.Commit()
	commitTook := time.Since(start)
	t.Logf("COMMIT -> err=%q after %s", commitErr, commitTook)
	if commitErr == nil {
		t.Fatalf("expected SQLITE_BUSY from COMMIT")
	}
	if !strings.Contains(commitErr.Error(), "database is locked") && !strings.Contains(commitErr.Error(), "SQLITE_BUSY") {
		t.Fatalf("COMMIT err = %q, want a busy/locked error", commitErr)
	}
	if errors.Is(commitErr, driver.ErrBadConn) {
		t.Fatalf("COMMIT err must not be driver.ErrBadConn when ROLLBACK succeeded; got %v", commitErr)
	}

	// 关键差异断言：泄漏事务在原生驱动下会把锁升级成 PENDING、连读都挡。
	// 受保护驱动已经回滚，读必须成功。
	//
	// **判据是"成功/失败"，不是"多快"。** 原先这里拿 wall-clock 跟 busy_timeout
	// 比大小（"超过 150ms 就说明等过锁"），这个代理指标在 CI runner 上会误报：
	// 实测红过一次——读只用 512µs（锁确实放了），而随后的写花了 379ms，
	// 于是一条正确的实现被计时抖动判成了失败。fcntl 的拿锁重试是**每 1ms 一次
	// 的上界**，不是下界：一次调度延迟、一次磁盘抖动都能让它超过阈值，
	// 而锁是否释放与这件事无关。
	//
	// 真正区分"锁放了"与"没放"的是结果：没放锁时，busy_timeout 到期后这两句话
	// 会**返回 database is locked**，而不是慢一点成功。所以这里只断言它们成功，
	// 外加一个宽松的上限（10×busy_timeout）兜住"卡死"这种真正该红的情况。
	// start 必须在这里重置：上面那个是在 Commit 之前取的，含进了等锁的时间。
	// 不重置的话，日志会把 COMMIT 的等待算到这条 SELECT 头上。
	start = time.Now()
	if err := clean.QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatalf("clean pool SELECT after guarded COMMIT failure: %v（原 bug 在这里会报 database is locked）", err)
	}
	if took := time.Since(start); took > verifyHangBound {
		t.Fatalf("clean SELECT took %s（上限 %s），连接可能卡在锁上", took, verifyHangBound)
	} else {
		t.Logf("clean pool SELECT after guarded COMMIT failure: ok in %s", took)
	}

	// 读者回滚之后，写必须恢复。同上：判据是这一句能不能成，不是它花了多久。
	if err := readerTx.Rollback(); err != nil {
		t.Fatalf("reader.Rollback: %v", err)
	}
	start = time.Now()
	if _, err := clean.Exec("INSERT INTO t(v) VALUES ('from-clean-pool')"); err != nil {
		t.Fatalf("clean pool INSERT after reader rollback: %v，写锁没有随 COMMIT 失败一起释放", err)
	}
	if took := time.Since(start); took > verifyHangBound {
		t.Fatalf("clean INSERT took %s（上限 %s），写锁没有随 COMMIT 失败一起释放", took, verifyHangBound)
	}

	// 写者池的同一条连接必须能继续开事务并提交（原 bug：cannot start a transaction
	// within a transaction (1)）。
	for i := 0; i < 3; i++ {
		tx, err := writer.Begin()
		if err != nil {
			t.Fatalf("writer.Begin #%d after failed COMMIT: %v", i+1, err)
		}
		if _, err := tx.Exec("INSERT INTO t(v) VALUES (?)", fmt.Sprintf("retry-%d", i)); err != nil {
			_ = tx.Rollback()
			t.Fatalf("writer.INSERT #%d: %v", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("writer.COMMIT #%d: %v", i+1, err)
		}
	}

	var got int
	if err := clean.QueryRow("SELECT count(*) FROM t").Scan(&got); err != nil {
		t.Fatalf("final count: %v", err)
	}
	// 1（clean）+ 3（writer 重试）= 4；撞 BUSY 的那一笔必须整笔不在。
	if want := 4; got != want {
		t.Fatalf("row count = %d, want %d（撞 BUSY 的事务必须整笔回滚）", got, want)
	}
}

// ───────────────── ② 假驱动：Rollback 失败时必须 ErrBadConn + 丢弃连接 ─────────────────

var verifyDriverSeq atomic.Int64

type verifyFakeDriver struct {
	mu     sync.Mutex
	opens  int
	closes int

	commitErr   error
	rollbackErr error
	execErr     error // 驱动 Rollback 之后的 SQL ROLLBACK 兜底
}

func (d *verifyFakeDriver) Open(string) (driver.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.opens++
	return &verifyFakeConn{d: d}, nil
}

func (d *verifyFakeDriver) counts() (opens, closes int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.opens, d.closes
}

type verifyFakeConn struct {
	d *verifyFakeDriver
}

func (c *verifyFakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("verifyFakeConn: Prepare not supported")
}
func (c *verifyFakeConn) Close() error {
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	c.d.closes++
	return nil
}
func (c *verifyFakeConn) Begin() (driver.Tx, error) { return &verifyFakeTx{c: c}, nil }

// ExecerContext 供 guardedTx.execRollback 的 "ROLLBACK" 兜底使用。
func (c *verifyFakeConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return nil, c.d.execErr
}

type verifyFakeTx struct{ c *verifyFakeConn }

func (t *verifyFakeTx) Commit() error   { return t.c.d.commitErr }
func (t *verifyFakeTx) Rollback() error { return t.c.d.rollbackErr }

// verifyRegisterFake 把假驱动包一层受保护驱动注册进去，返回驱动名。
func verifyRegisterFake(t *testing.T, d *verifyFakeDriver) string {
	t.Helper()
	name := fmt.Sprintf("verify-fake-%d", verifyDriverSeq.Add(1))
	sql.Register(name, &guardedDriver{base: d})
	return name
}

var verifyBusyErr = errors.New("database is locked (5) (SQLITE_BUSY)")

// TestVerify_CommitFailureDiscardsConnWhenRollbackFails：COMMIT 失败 + 驱动 Rollback
// 失败 + SQL ROLLBACK 兜底也失败 => guardedTx 返回 driver.ErrBadConn，
// database/sql 必须关闭（丢弃）这条连接，下一句操作换新连接。
func TestVerify_CommitFailureDiscardsConnWhenRollbackFails(t *testing.T) {
	d := &verifyFakeDriver{commitErr: verifyBusyErr, rollbackErr: verifyBusyErr, execErr: verifyBusyErr}
	name := verifyRegisterFake(t, d)
	db := verifyOpen(t, name, ":memory:", 1)

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	err = tx.Commit()
	t.Logf("Commit -> err=%v (errors.Is ErrBadConn=%v)；opens/closes=%s", err, errors.Is(err, driver.ErrBadConn), opensCloses(d))
	if !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("Commit err = %v, want driver.ErrBadConn（Rollback 失败时必须让池子丢弃连接）", err)
	}
	if opens, closes := d.counts(); opens != 1 || closes != 1 {
		t.Fatalf("opens=%d closes=%d, want 1/1（脏连接必须被丢弃）", opens, closes)
	}

	// 下一次操作应该拿到一条新连接。
	if err := db.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if opens, closes := d.counts(); opens != 2 || closes != 1 {
		t.Fatalf("after Ping: opens=%d closes=%d, want 2/1（丢弃后必须重开）", opens, closes)
	}
}

// TestVerify_CommitFailureKeepsConnWhenSQLRollbackSucceeds：驱动 Rollback 失败，
// 但 SQL ROLLBACK 兜底成功 => 连接是干净的，返回原始锁错误，不丢弃连接。
func TestVerify_CommitFailureKeepsConnWhenSQLRollbackSucceeds(t *testing.T) {
	d := &verifyFakeDriver{commitErr: verifyBusyErr, rollbackErr: verifyBusyErr, execErr: nil}
	name := verifyRegisterFake(t, d)
	db := verifyOpen(t, name, ":memory:", 1)

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	commitErr := tx.Commit()
	t.Logf("Commit -> err=%v", commitErr)
	if !errors.Is(commitErr, verifyBusyErr) {
		t.Fatalf("Commit err = %v, want the original busy error (SQL ROLLBACK 已兜底成功)", commitErr)
	}
	if errors.Is(commitErr, driver.ErrBadConn) {
		t.Fatalf("Commit err must not be ErrBadConn after SQL ROLLBACK succeeded")
	}
	if opens, closes := d.counts(); opens != 1 || closes != 0 {
		t.Fatalf("opens=%d closes=%d, want 1/0（兜底成功则保留连接）", opens, closes)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("Ping after kept conn: %v", err)
	}
	if opens, closes := d.counts(); opens != 1 || closes != 0 {
		t.Fatalf("after Ping: opens=%d closes=%d, want 1/0（应复用保留的连接）", opens, closes)
	}
}

// TestVerify_RollbackFailureDiscardsConn：Rollback 也失败（含 SQL 兜底）时，
// guardedTx.Rollback 必须返回 driver.ErrBadConn 并让池子丢弃连接。
func TestVerify_RollbackFailureDiscardsConn(t *testing.T) {
	d := &verifyFakeDriver{rollbackErr: verifyBusyErr, execErr: verifyBusyErr}
	name := verifyRegisterFake(t, d)
	db := verifyOpen(t, name, ":memory:", 1)

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	rbErr := tx.Rollback()
	t.Logf("Rollback -> err=%v", rbErr)
	if !errors.Is(rbErr, driver.ErrBadConn) {
		t.Fatalf("Rollback err = %v, want driver.ErrBadConn", rbErr)
	}
	if opens, closes := d.counts(); opens != 1 || closes != 1 {
		t.Fatalf("opens=%d closes=%d, want 1/1（回滚不掉的连接必须丢弃）", opens, closes)
	}
}

// TestVerify_RollbackFailureButSQLFallbackSucceeds：驱动 Rollback 失败、SQL 兜底
// 成功 => 事务确实结束了，Rollback 返回 nil，连接保留。
func TestVerify_RollbackFailureButSQLFallbackSucceeds(t *testing.T) {
	d := &verifyFakeDriver{rollbackErr: verifyBusyErr, execErr: nil}
	name := verifyRegisterFake(t, d)
	db := verifyOpen(t, name, ":memory:", 1)

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback err = %v, want nil（SQL 兜底成功即视为干净）", err)
	}
	if opens, closes := d.counts(); opens != 1 || closes != 0 {
		t.Fatalf("opens=%d closes=%d, want 1/0", opens, closes)
	}
}

// TestVerify_CommitErrorSayingNoTransactionIsSuccess：错误文案是"没有活动事务"时，
// 说明事务已经结束，连接是干净的——不能因此丢弃连接。
func TestVerify_CommitErrorSayingNoTransactionIsSuccess(t *testing.T) {
	noTx := errors.New("SQL logic error: cannot rollback - no transaction is active (1)")
	d := &verifyFakeDriver{commitErr: noTx, rollbackErr: noTx, execErr: noTx}
	name := verifyRegisterFake(t, d)
	db := verifyOpen(t, name, ":memory:", 1)

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	commitErr := tx.Commit()
	t.Logf("Commit -> err=%v", commitErr)
	if !errors.Is(commitErr, noTx) {
		t.Fatalf("Commit err = %v, want the driver error unchanged", commitErr)
	}
	if errors.Is(commitErr, driver.ErrBadConn) {
		t.Fatalf("already-ended tx must not be reported as ErrBadConn")
	}
	if opens, closes := d.counts(); opens != 1 || closes != 0 {
		t.Fatalf("opens=%d closes=%d, want 1/0", opens, closes)
	}
}

// ───────────────── ③ 转发：包装不能丢能力 ─────────────────

func TestVerify_ConnForwarding(t *testing.T) {
	Register()
	path := filepath.Join(t.TempDir(), "forwarding.db")
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(%d)", filepath.ToSlash(path), verifyBusyMS)
	db := verifyOpen(t, DriverName, dsn, 2)

	if err := db.Ping(); err != nil {
		t.Fatalf("Ping through guarded driver: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE f (id INTEGER PRIMARY KEY, v TEXT)"); err != nil {
		t.Fatalf("ExecContext: %v", err)
	}
	stmt, err := db.Prepare("INSERT INTO f(v) VALUES (?)")
	if err != nil {
		t.Fatalf("PrepareContext: %v", err)
	}
	if _, err := stmt.Exec("prepared"); err != nil {
		t.Fatalf("stmt.Exec: %v", err)
	}
	if err := stmt.Close(); err != nil {
		t.Fatalf("stmt.Close: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	roTx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx(ReadOnly): %v", err)
	}
	var got string
	if err := roTx.QueryRowContext(ctx, "SELECT v FROM f LIMIT 1").Scan(&got); err != nil {
		t.Fatalf("read-only tx query: %v", err)
	}
	if err := roTx.Commit(); err != nil {
		t.Fatalf("read-only tx commit: %v", err)
	}
	if got != "prepared" {
		t.Fatalf("got %q, want prepared", got)
	}

	// 事务应当能正常开启并回滚（guarded Begin 路径）。
	rwTx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := rwTx.Exec("INSERT INTO f(v) VALUES ('rolled-back')"); err != nil {
		t.Fatalf("tx.Exec: %v", err)
	}
	if err := rwTx.Rollback(); err != nil {
		t.Fatalf("tx.Rollback: %v", err)
	}
	var cnt int
	if err := db.QueryRow("SELECT count(*) FROM f").Scan(&cnt); err != nil {
		t.Fatalf("final query: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("count = %d, want 1（回滚的事务不该留下行）", cnt)
	}
}

func opensCloses(d *verifyFakeDriver) string {
	o, c := d.counts()
	return fmt.Sprintf("%d/%d", o, c)
}
