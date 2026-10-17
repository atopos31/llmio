// Package sqlitex 在 glebarez/sqlite 之上补一条 database/sql 不会替你做的事：
// **事务结束后保证连接是干净的**。
//
// 为什么需要它（这是线上整库锁死的直接原因）：
//
//   - SQLite 的回滚日志模式（journal_mode=delete）下，COMMIT 需要 EXCLUSIVE 锁；
//     只要有别的连接持着 SHARED（一次普通查询就算），它就得等，等不到
//     busy_timeout 就返回 SQLITE_BUSY。**而 SQLite 的语义是：COMMIT 返回 BUSY 时
//     事务并没有结束，仍然活着**（可以重试 COMMIT 或 ROLLBACK）。
//   - Go 的 database/sql 在 Tx.Commit 出错时只做一件事：把连接放回池子。
//     它既不会调用 driver.Tx.Rollback，也不会因为 BUSY 丢弃连接
//     （见 GOROOT/src/database/sql/sql.go 的 Tx.Commit → tx.close(err) →
//     putConn(err)：只有 err == driver.ErrBadConn 才会 Close，其余一律回池）。
//   - 于是那条连接带着一个**没结束的写事务**回到空闲池：RESERVED/PENDING 锁永远
//     不放。之后所有读都等满 busy_timeout 报 SQLITE_BUSY，所有写在拿锁时失败，
//     在这条连接上 Begin 直接报 "cannot start a transaction within a transaction
//     (1)"——整库死锁，直到进程重启（重启时 SQLite 用残留的 -journal 回滚）。
//
// 这个包做的事只有两件：
//  1. COMMIT 失败时**自己把事务回滚掉**（先驱动 Rollback，再退回 SQL ROLLBACK）。
//  2. 回滚不掉时返回 driver.ErrBadConn，让 database/sql 丢弃这条连接（进程内关闭
//     文件描述符、释放 fcntl 锁）——宁可换一条新连接，也不把脏连接放回池子。
//
// 用 DriverName + sqlite.Dialector{DriverName: sqlitex.DriverName} 启用，见 models.Init。
package sqlitex

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync"

	gsqlite "github.com/glebarez/go-sqlite"
)

// DriverName 是受保护驱动的注册名。
//
// 与 glebarez 自带的 "sqlite" 并存：测试与迁移脚本里直接用 "sqlite" 仍然可用，
// 只有服务自己的连接池走这一条。
const DriverName = "sqlite-guarded"

var registerOnce sync.Once

// Register 注册受保护驱动。可重复调用。
func Register() {
	registerOnce.Do(func() {
		sql.Register(DriverName, &guardedDriver{base: &gsqlite.Driver{}})
	})
}

type guardedDriver struct {
	base driver.Driver
}

func (d *guardedDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	return &guardedConn{Conn: conn}, nil
}

// guardedConn 只做两件事：把 Begin/BeginTx 返回的事务包起来；其余能力一律透传。
//
// 必须显式转发 database/sql 认的那几个可选接口：不转发的话，ExecContext /
// QueryContext 会退化成"每条语句 Prepare 一次"，连接池也会失去 Ping/ResetSession
// 这些语义。driver.ErrSkip 是这些接口约定的"我没实现，你退回去走通用路径"。
type guardedConn struct {
	driver.Conn
}

func (c *guardedConn) Begin() (driver.Tx, error) {
	tx, err := c.Conn.Begin()
	if err != nil {
		return nil, err
	}
	return &guardedTx{Tx: tx, conn: c.Conn}, nil
}

func (c *guardedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	beginner, ok := c.Conn.(driver.ConnBeginTx)
	if !ok {
		// 底层驱动没实现 ConnBeginTx：退回它自己的 Begin。
		//
		// **不能返回 driver.ErrSkip**：BeginTx 这条路径上 database/sql 不会替你
		// 回退（ErrSkip 会被原样抛给调用方，表现为 "driver: skip fast-path"）。
		// 这个分支只在包装别的驱动时出现（真实驱动实现了 ConnBeginTx）。
		tx, err := c.Conn.Begin()
		if err != nil {
			return nil, err
		}
		return &guardedTx{Tx: tx, conn: c.Conn}, nil
	}
	tx, err := beginner.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &guardedTx{Tx: tx, conn: c.Conn}, nil
}

func (c *guardedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if p, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return p.PrepareContext(ctx, query)
	}
	return c.Conn.Prepare(query)
}

func (c *guardedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if e, ok := c.Conn.(driver.ExecerContext); ok {
		return e.ExecContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

func (c *guardedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if q, ok := c.Conn.(driver.QueryerContext); ok {
		return q.QueryContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

func (c *guardedConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *guardedConn) ResetSession(ctx context.Context) error {
	if r, ok := c.Conn.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *guardedConn) IsValid() bool {
	if v, ok := c.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

func (c *guardedConn) CheckNamedValue(nv *driver.NamedValue) error {
	if ck, ok := c.Conn.(driver.NamedValueChecker); ok {
		return ck.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

// guardedTx 在事务失败后负责把连接恢复成"没有未结束事务"的状态。
type guardedTx struct {
	driver.Tx
	conn driver.Conn
}

// Commit 提交事务。失败时事务仍可能开着，必须收尾后才能把连接交还池子。
func (t *guardedTx) Commit() error {
	err := t.Tx.Commit()
	if err == nil {
		return nil
	}
	if !t.abort() {
		// 回滚不掉：这条连接不可信，让 database/sql 直接 Close 掉它。
		return driver.ErrBadConn
	}
	return err
}

// Rollback 回滚事务。驱动回滚失败时同样兜底，保证连接不会带着事务回池。
func (t *guardedTx) Rollback() error {
	err := t.Tx.Rollback()
	if err == nil {
		return nil
	}
	if t.abort() {
		return nil
	}
	return driver.ErrBadConn
}

// abort 尽最大努力结束 SQLite 侧的事务。返回 false 表示连接必须丢弃。
func (t *guardedTx) abort() bool {
	// 1) 正规路径：驱动自己的 Rollback。
	if err := t.Tx.Rollback(); err == nil || isNoActiveTx(err) {
		return true
	}
	// 2) 兜底：COMMIT 失败后事务通常还开着，用 SQL 再回滚一次。
	if err := t.execRollback(); err == nil || isNoActiveTx(err) {
		return true
	}
	return false
}

func (t *guardedTx) execRollback() error {
	const stmt = "ROLLBACK"
	if ex, ok := t.conn.(driver.ExecerContext); ok {
		_, err := ex.ExecContext(context.Background(), stmt, nil)
		return err
	}
	if ex, ok := t.conn.(driver.Execer); ok {
		_, err := ex.Exec(stmt, nil)
		return err
	}
	return errors.New("sqlitex: 连接不支持直接执行 ROLLBACK")
}

// isNoActiveTx 判"这句话是在说没有活动事务"——那说明事务已经结束，属于成功。
func isNoActiveTx(err error) bool {
	if err == nil {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no transaction is active") ||
		strings.Contains(msg, "cannot rollback - no transaction")
}
