package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/atopos31/llmio/models"
)

// 这个文件盯的是**状态页在迁移期间还活着**这件事。起因是真机反馈：
// 迁移跑着的时候点「开始迁移」会 500（`database is locked`），用户得手动刷新
// 才起得来。根因是"开始迁移"那条路为了拿一个文件大小去调了整条 stats 读，
// 而那条读里有全表聚合，撞上迁移的写锁就整个失败。
//
// 所以这里验三条：
//
//  1. 撞锁要**重试**，一次 BUSY 不是终局。
//  2. 真量不到时给 nil（前端画"量不到"），**不报错**、更不拿 0 冒充。
//  3. 全表聚合要**带冷却**：状态页每 2 秒轮询一次，每次都真扫是自造锁竞争。

func resetDBStatsCache() {
	dbStatsMu.Lock()
	defer dbStatsMu.Unlock()
	dbStatsCache, dbStatsAt = nil, time.Time{}
}

func TestDBStats_ReadsTheRealNumbersAndCaches(t *testing.T) {
	setupLogCompressTestDB(t)
	resetDBStatsCache()
	ctx := context.Background()

	seedPlaintextRows(t, ctx, [][]byte{[]byte("hello"), []byte("world")}, time.Hour)

	first := ReadDBStats(ctx)
	if first == nil {
		t.Fatal("量得到却给了 nil")
	}
	if first.Rows != 2 || first.PendingRows != 2 || first.FramedRows != 0 {
		t.Fatalf("行数不对：rows=%d pending=%d framed=%d", first.Rows, first.PendingRows, first.FramedRows)
	}
	if first.PageSize <= 0 || first.PageCount <= 0 {
		t.Fatalf("页信息不对：page_size=%d page_count=%d", first.PageSize, first.PageCount)
	}
	if first.StatsAt == 0 || first.Stale {
		t.Fatalf("刚量到的数应当是新鲜的：stats_at=%d stale=%v", first.StatsAt, first.Stale)
	}

	// 冷却期内直接给缓存：这一条是"别每次轮询都扫全表"的落点。
	// 用"往库里再插一行"来验——真去扫的话 rows 会变 3，给缓存就还是 2。
	seedPlaintextRows(t, ctx, [][]byte{[]byte("third")}, time.Hour)
	second := ReadDBStats(ctx)
	if second.Rows != 2 {
		t.Fatalf("冷却期内应当给缓存（rows 应仍为 2），实得 %d——全表聚合被每次轮询都真跑了", second.Rows)
	}
	if second.StatsAt != first.StatsAt {
		t.Fatalf("缓存里的 stats_at 不该变：%d → %d", first.StatsAt, second.StatsAt)
	}

	// 冷却期过了就必须真量。把时钟往回拨而不是 sleep 5 秒。
	dbStatsMu.Lock()
	dbStatsAt = time.Now().Add(-dbStatsTTL - time.Second)
	dbStatsMu.Unlock()

	third := ReadDBStats(ctx)
	if third.Rows != 3 {
		t.Fatalf("冷却期过后应当真量（rows 应为 3），实得 %d", third.Rows)
	}
}

// 空闲磁盘那一项：界面拿它**提前算**重整够不够（VACUUM 要 2 倍库大小的可用空间），
// 所以它必须在状态里，而且必须是真的（一次 statfs，不是估算）。
//
// 判据取"大于 0 且小于等于库所在卷的总量"这种弱形状，是因为测试机上的可用空间
// 是个一直在动的数：钉死具体值只会得到一条时灵时不灵的用例。真正要钉的是
// **它有没有被填上**——留 0 的话界面会把每一次重整都判成"空间不够"，
// 而那条路上的用户看到的会是一个永远点不动的按钮。
func TestDBStats_ReportsFreeDiskSpace(t *testing.T) {
	setupLogCompressTestDB(t)
	resetDBStatsCache()

	stats := ReadDBStats(context.Background())
	if stats == nil {
		t.Fatal("量得到却给了 nil")
	}
	if stats.DiskFree <= 0 {
		t.Fatalf("空闲磁盘没量到（%d）——界面据此判重整的空间够不够，0 会把每一次都判成不够",
			stats.DiskFree)
	}
	// 它必须够放得下这份刚建出来的小库：这条用例自己就活在同一个卷上。
	if !enoughDisk(stats.DiskFree, stats.FileSize) {
		t.Fatalf("同一卷上量出的空闲 %d 放不下库 %d——这两个数里有一个不是这个卷的",
			stats.DiskFree, stats.FileSize)
	}
}

// 撞锁要重试。这里不真去制造 SQLITE_BUSY（那要另一条连接持写锁，慢且脆），
// 而是直接验判据本身：判错了的后果是"不重试"，退化方向是安全的。
func TestDBStats_BusyErrorDetection(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"驱动错误码 SQLITE_BUSY", codedErr(5), true},
		{"驱动错误码 SQLITE_LOCKED", codedErr(6), true},
		{"只有文本（换过驱动也认得出）", errors.New("database is locked (5) (SQLITE_BUSY)"), true},
		{"包了一层也要认得出", fmt.Errorf("读库现状: %w", codedErr(5)), true},
		{"约束冲突不是撞锁", codedErr(19), false},
		{"语法错不是撞锁", errors.New("near \"SELEC\": syntax error"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsBusyErr(c.err); got != c.want {
				t.Fatalf("IsBusyErr(%v) = %v，期望 %v", c.err, got, c.want)
			}
		})
	}
}

// 真量不到（库都关了）时：给 nil，不 panic、不报错、不拿 0 冒充。
func TestDBStats_UnreadableReturnsNilNotZeros(t *testing.T) {
	setupLogCompressTestDB(t)
	resetDBStatsCache()

	ctx := context.Background()
	sqlDB, err := models.DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	if got := ReadDBStats(ctx); got != nil {
		t.Fatalf("量不到应当给 nil（前端据此画「量不到」），实得 %+v", got)
	}
	// 再问一次也不该 panic，也不该突然冒出一组 0。
	if got := ReadDBStats(ctx); got != nil {
		t.Fatalf("第二次仍然该是 nil，实得 %+v", got)
	}
}

// 量得到 → 后来量不到：给**上一次的**并标 stale，而不是把整块收回去。
// 这中间的差别在界面上是"数字旧了几分钟"和"什么都没有"。
func TestDBStats_ServesLastKnownWhenMeasurementFails(t *testing.T) {
	setupLogCompressTestDB(t)
	resetDBStatsCache()
	ctx := context.Background()

	seedPlaintextRows(t, ctx, [][]byte{[]byte("hello")}, time.Hour)
	good := ReadDBStats(ctx)
	if good == nil {
		t.Fatal("第一次应当量得到")
	}

	// 让缓存过期，再把库关掉——下一次真量必然失败。
	dbStatsMu.Lock()
	dbStatsAt = time.Now().Add(-dbStatsTTL - time.Second)
	dbStatsMu.Unlock()
	sqlDB, _ := models.DB.DB()
	_ = sqlDB.Close()

	got := ReadDBStats(ctx)
	if got == nil {
		t.Fatal("量不到时应当拿上一次的顶一顶，实得 nil")
	}
	if !got.Stale {
		t.Fatal("顶替的那一份必须标 stale——不标的话，刚 VACUUM 完的人会以为没生效")
	}
	if got.Rows != good.Rows {
		t.Fatalf("顶替的应当是上一次那份数，rows=%d 期望 %d", got.Rows, good.Rows)
	}
}

// 上一次的数太老就不给了：刚 VACUUM 完却显示十分钟前的大小，
// 比不显示更容易让人做错判断。
func TestDBStats_TooOldCacheIsNotServed(t *testing.T) {
	setupLogCompressTestDB(t)
	resetDBStatsCache()
	ctx := context.Background()

	seedPlaintextRows(t, ctx, [][]byte{[]byte("hello")}, time.Hour)
	if ReadDBStats(ctx) == nil {
		t.Fatal("第一次应当量得到")
	}

	dbStatsMu.Lock()
	dbStatsAt = time.Now().Add(-dbStatsMaxStale - time.Second)
	dbStatsMu.Unlock()
	sqlDB, _ := models.DB.DB()
	_ = sqlDB.Close()

	if got := ReadDBStats(ctx); got != nil {
		t.Fatalf("超过 maxStale 的旧数不该再给，实得 %+v", got)
	}
}

// 调用方拿到的是副本：它会在 DTO 上写 Stale，不能改到缓存本体。
func TestDBStats_CallersGetTheirOwnCopy(t *testing.T) {
	setupLogCompressTestDB(t)
	resetDBStatsCache()
	ctx := context.Background()

	seedPlaintextRows(t, ctx, [][]byte{[]byte("hello")}, time.Hour)
	first := ReadDBStats(ctx)
	first.Stale = true
	first.Rows = 999

	second := ReadDBStats(ctx)
	if second.Stale || second.Rows == 999 {
		t.Fatal("缓存被调用方改掉了")
	}
}

// codedErr 造一个带 SQLite 错误码的 error，形状与驱动一致（*Error 有 Code()）。
type codedErr int

func (e codedErr) Error() string { return fmt.Sprintf("sqlite error (code %d)", int(e)) }
func (e codedErr) Code() int     { return int(e) }

// 待迁移的行数按**三列**算，不按请求体一列算。
//
// 按单列算的话，"请求体迁完了、响应体还没"这种最常见的中间态会读成 0 行——
// 而它正是真机上发生过的：界面报"已完成"，1.36 GiB 明文一行没动。
// 这个口径与迁移自己的候选集必须一致：两个数说的是同一件事，分头写迟早会漂。
func TestDBStats_PendingSpansAllThreeColumns(t *testing.T) {
	setupLogCompressTestDB(t)
	resetDBStatsCache()
	ctx := context.Background()

	ids := seedPlaintextRows(t, ctx, [][]byte{bigRow(1, 8<<10)}, time.Hour)
	if _, err := RunLogCompress(ctx, false); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}

	// 请求体已压成帧、响应体还是明文：只看 input 的话这里 pending 是 0。
	plain := "响应体明文"
	writeRawColumn(t, ctx, ids[0], "of_string", plain)

	got := ReadDBStats(ctx)
	if got == nil {
		t.Fatal("量得到却给了 nil")
	}
	if got.PendingRows != 1 {
		t.Fatalf("pending_rows=%d，期望 1——响应体那一列没被算进候选", got.PendingRows)
	}
	if got.FramedRows != 1 {
		t.Fatalf("framed_rows=%d，期望 1——请求体那一列已经是帧了", got.FramedRows)
	}
	// 「这两列此刻占多少字节」**故意不在这里**：那个数只能靠读载荷算
	// （`sum(length(CAST(列 AS BLOB)))`，真机 1.36 GiB 要 0.80 秒），
	// 而这个接口每 2 秒被轮询一次，长读事务在 `journal_mode=delete` 下挡写。
	// 界面要的"库现在多大"由 file_size 回答。详见 db_stats.go 的字段说明。
	if got.FileSize <= 0 {
		t.Fatalf("file_size=%d，界面靠它报「实际占用」", got.FileSize)
	}
}
