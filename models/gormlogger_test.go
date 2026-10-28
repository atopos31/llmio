package models

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// captureGormLog 跑一遍 newGormLogger 的**真实配置**，把输出收回来。
//
// 走 newGormLoggerWith 而不是在这里另抄一份 logger.Config：抄一份就等于
// 测 gorm 库自己的 switch 语义，改 newGormLogger 永远不会让用例变红。
// 测试里能换的只有输出目的地。
func captureGormLog(t *testing.T, run func(l logger.Interface)) string {
	t.Helper()

	var buf strings.Builder
	run(newGormLoggerWith(&writerFunc{out: &buf}))
	return buf.String()
}

// TestGormLoggerSuppressesRecordNotFound 锁住"缺行不打日志"这条设置。
//
// 它值得一条用例，因为 GORM 的默认值是反的（IgnoreRecordNotFoundError=false），
// 而本仓有好几处**把缺行当成正常态**的读：策略配置缺行回默认值、
// 按名字查模型未命中走回退、按 log_id 查 IO 但这个 log 本来就没开 io_log。
// 哪天有人把 logger 换回默认值，这些噪音会立刻按周期或按请求数刷回来——
// 而那种回归不会让任何功能坏掉，只会让日志没法看，所以只能靠测试守。
func TestGormLoggerSuppressesRecordNotFound(t *testing.T) {
	got := captureGormLog(t, func(l logger.Interface) {
		// 模拟 GORM 在 First 没查着时的调用：err 是 ErrRecordNotFound，
		// 耗时是这份语料的真实量级（真机 0.25ms 一条）。
		//
		// **耗时必须给成"不慢"**：GORM 的 Trace 是 switch，错误分支被
		// IgnoreRecordNotFoundError 关掉之后，还会落到「慢 SQL」那一支上。
		// 这一点是对的——一条真的慢的未命中查询仍然值得看一眼——
		// 但本仓这几条策略读都是索引点查，量级在亚毫秒。
		l.Trace(context.Background(), time.Now(), func() (string, int64) {
			return "SELECT * FROM `configs` WHERE key = 'log_compress_policy' LIMIT 1", 0
		}, gorm.ErrRecordNotFound)
	})

	if got != "" {
		t.Fatalf("record not found 被打出来了：\n%s\n它会在每个请求 / 每个调度周期刷一次", got)
	}
}

// TestGormLoggerStillLogsSlowNotFound 慢的未命中查询仍然要打出来。
//
// 与上一条是一对，把边界钉死：IgnoreRecordNotFoundError 只关**错误分支**，
// 关不掉**慢 SQL 分支**。这是 GORM 的 switch 语义（errors 那一支跳过后，
// 接着判 elapsed > SlowThreshold），也正是我们想要的行为——
// 缺行本身不是错误，但一次真的跑了几百毫秒的查询，不管查没查着都该看一眼。
func TestGormLoggerStillLogsSlowNotFound(t *testing.T) {
	got := captureGormLog(t, func(l logger.Interface) {
		l.Trace(context.Background(), time.Now().Add(-2*time.Second), func() (string, int64) {
			return "SELECT * FROM `configs` LIMIT 1", 0
		}, gorm.ErrRecordNotFound)
	})

	if !strings.Contains(got, "SLOW SQL") {
		t.Fatalf("慢的未命中查询被完全吞掉了，日志内容：%q", got)
	}
}

// TestGormLoggerKeepsOtherErrors 真正的错误必须还看得见。
//
// 上一条只该关掉 ErrRecordNotFound，不该顺手把"表不存在/库读不通"一起吞掉。
func TestGormLoggerKeepsOtherErrors(t *testing.T) {
	got := captureGormLog(t, func(l logger.Interface) {
		l.Trace(context.Background(), time.Now(), func() (string, int64) {
			return "SELECT * FROM `nope`", 0
		}, errNotRecordNotFound)
	})

	if !strings.Contains(got, "no such table") {
		t.Fatalf("非 ErrRecordNotFound 的错误被吞掉了，日志内容：%q", got)
	}
}

// TestGormLoggerKeepsSlowSQL 慢 SQL 必须还看得见。
//
// 与上面是一对：这次修复只该关掉"缺行"这类假错误，不该把慢 SQL 一起关掉——
// chat_ios 那条 5895ms 的 UPDATE 正是靠它发现的，关掉它等于砸了唯一的
// 性能观测手段。
func TestGormLoggerKeepsSlowSQL(t *testing.T) {
	got := captureGormLog(t, func(l logger.Interface) {
		l.Trace(context.Background(), time.Now().Add(-2*time.Second), func() (string, int64) {
			return "UPDATE `chat_ios` SET `of_string_array`=? WHERE log_id = ?", 1
		}, nil)
	})

	if !strings.Contains(got, "SLOW SQL") {
		t.Fatalf("慢 SQL 没有被打出来，日志内容：%q", got)
	}
	if !strings.Contains(got, "chat_ios") {
		t.Fatalf("慢 SQL 打出来了但没带语句：%q", got)
	}
}

// writerFunc 让 logger.New 能写到 buffer 里。
type writerFunc struct{ out *strings.Builder }

func (w *writerFunc) Printf(format string, args ...interface{}) {
	w.out.WriteString(sprintf(format, args...))
	w.out.WriteString("\n")
}

var errNotRecordNotFound = errors.New("no such table: nope")

func sprintf(format string, args ...interface{}) string { return fmt.Sprintf(format, args...) }
