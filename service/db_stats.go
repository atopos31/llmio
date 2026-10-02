package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/atopos31/llmio/models"
)

// 本文件量"库的现状"，供状态页使用。两条规矩都是真机打出来的：
//
//  1. **它不许失败。** 状态页在迁移期间每 2 秒轮询一次，而迁移正在写同一张表。
//     原先这里一撞锁就 `InternalServerError`，前端整块报错、用户得手动刷新；
//     更要命的是"开始迁移"那个动作**也走这条读**（只为了拿一个文件大小去做备份
//     校验），于是撞一次锁连迁移都起不来——实测反馈就是这个形状。
//  2. **它不许每次轮询都扫全表。** 这一组数里最难的是全表聚合（`pending_rows`
//     要扫整表 typeof，真库 1 854 000 页）。挂个 2 秒轮询、每次都真扫，
//     等于自己给自己制造锁竞争——读越慢，下一次撞锁的概率越高。
//
// 所以：常数级的那部分（pragma、文件大小）每次真读；全表聚合那部分带 TTL。
// runtime 里没有"半真半假"的选项，两者失效原因是同一个（撞锁），
// 所以合在一组：要么整体新鲜，要么整体是上一次的并标上 stale。

const (
	// dbStatsTTL 是这一组数的冷却期。5 秒对一张进度卡足够新鲜
	// （真正会动的进度条走的是 state，那份读的是 Config，廉价且不缓存）。
	dbStatsTTL = 5 * time.Second

	// dbStatsMaxStale 是"拿上一次的数顶一顶"的上界。再老就不给了：
	// 刚 VACUUM 完却显示十分钟前的大小，比不显示更容易让人得出错误结论。
	dbStatsMaxStale = time.Minute

	// dbStatsAttempts 是撞锁后的总尝试次数。一次 SQLITE_BUSY 要等满
	// busy_timeout（实测驱动默认给 5000ms），所以次数不能多——状态页是
	// 2 秒轮询的，读得比它慢只会让请求叠起来。
	dbStatsAttempts  = 3
	dbStatsRetryGap  = 250 * time.Millisecond
	dbStatsStmtLimit = 6 * time.Second
)

// SQLite 的错误码（`sqlite3.SQLITE_BUSY`/`SQLITE_LOCKED`）。
//
// 不引 `modernc.org/sqlite/lib` 就为了两个常数：它是间接依赖，而这个文件
// 只需要这两个数。驱动自己的 `Error.Code()` 返回的就是这两个值。
const (
	sqliteBusy   = 5
	sqliteLocked = 6
)

// DBStats 是状态页要的"库的现状"。
//
// 字段全部非空——**整体**可能是 nil（一次都没量到），但不给"半真半假"的一组数：
// `auto_vacuum=0` 与 `freelist_count=0` 都是有确切含义的值（前者意味着文件永不
// 缩小，界面据此画橙色警告线），拿 0 去表示"不知道"会把警告画错。
type DBStats struct {
	Path        string `json:"path"`
	FileSize    int64  `json:"file_size"`
	PageSize    int64  `json:"page_size"`
	PageCount   int64  `json:"page_count"`
	Freelist    int64  `json:"freelist_count"`
	AutoVacuum  int64  `json:"auto_vacuum"`
	Rows        int64  `json:"rows"`
	PendingRows int64  `json:"pending_rows"`
	FramedRows  int64  `json:"framed_rows"`
	BlockRows   int64  `json:"block_rows"`
	GroupRows   int64  `json:"block_group_rows"`
	GroupBytes  int64  `json:"block_group_bytes"`
	ColumnBytes int64  `json:"input_column_bytes"`

	// StatsAt 是这组数字量出来的时刻（unix 毫秒）。前端据此说明"数据截至"。
	StatsAt int64 `json:"stats_at"`
	// Stale 表示这一组是**上一次**量到的（本次真读失败，拿旧的顶了一下）。
	Stale bool `json:"stale"`
}

var (
	dbStatsMu    sync.Mutex // 保护下面两个
	dbStatsCache *DBStats
	dbStatsAt    time.Time
)

// ReadDBStats 返回库的现状。**它不返回错误**：量不到就返回 nil，
// 让调用方把整块画成"量不到"，而不是把 500 甩给一个只是想知道进度的人。
func ReadDBStats(ctx context.Context) *DBStats {
	if s, ok := cachedDBStats(dbStatsTTL); ok {
		return s
	}

	// 同一瞬间可能有好几个轮询进来（多标签页、或上一次还没回来）。
	// 抢不到就别排队——直接拿上一次的：为一个已经迟到的数再等 5 秒不值得。
	if !dbStatsMu.TryLock() {
		if s, ok := cachedDBStats(0); ok {
			s.Stale = true
			return s
		}
		return nil
	}
	defer dbStatsMu.Unlock()

	// 再查一次：等锁的这段时间里可能已经有别的请求量好了。
	if s, ok := cachedDBStats(dbStatsTTL); ok {
		return s
	}

	// 真读用一个**脱离请求的 ctx**：前端关掉标签页会取消请求 ctx，而这次读
	// 的结果是要进缓存的，给下一个来的人用。被取消的读没资格污染缓存。
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dbStatsStmtLimit)
	defer cancel()

	stats, err := readDBStatsWithRetry(readCtx)
	if err == nil {
		stats.StatsAt = time.Now().UnixMilli()
		// 存进缓存的是**副本**：调用方（handler）会在这份 DTO 上写 Stale，
		// 直接把同一个指针塞进缓存的话，下一次来的人拿到的就是被人改过的数。
		cached := *stats
		dbStatsCache, dbStatsAt = &cached, time.Now()
		return stats
	}

	slog.Warn("db stats: 量库现状失败，拿上一次的顶替", "error", err)
	if s, ok := cachedDBStats(dbStatsMaxStale); ok {
		s.Stale = true
		return s
	}
	return nil
}

// cachedDBStats 取缓存。maxAge<=0 表示"多老都要"。
// 返回副本：调用方（handler）会往上写 Stale，不该改到缓存本体。
func cachedDBStats(maxAge time.Duration) (*DBStats, bool) {
	if dbStatsCache == nil {
		return nil, false
	}
	if maxAge > 0 && time.Since(dbStatsAt) > maxAge {
		return nil, false
	}
	cp := *dbStatsCache
	return &cp, true
}

// DBFileSize 只量库文件的字节数，**不碰数据库**。
//
// 「开始迁移」那条路只需要它：原先它调用了整条 stats，于是"迁移能不能开始"
// 被一次撞锁的读给卡住了。文件大小是个 os.Stat，没有锁可言。
func DBFileSize() (int64, error) {
	if models.DBPath == "" {
		return 0, os.ErrNotExist
	}
	info, err := os.Stat(models.DBPath)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func readDBStatsWithRetry(ctx context.Context) (*DBStats, error) {
	var last error
	for attempt := 0; attempt < dbStatsAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(dbStatsRetryGap):
			}
		}
		stats, err := readDBStatsOnce(ctx)
		if err == nil {
			return stats, nil
		}
		last = err
		// 不是撞锁就别重试了：重试解决不了语法错、表不存在这类问题。
		if !isBusyErr(err) {
			return nil, err
		}
	}
	return nil, last
}

// isBusyErr 判"这一句是不是撞上别人的写锁了"。
//
// 两手准备：驱动的 `*Error` 带错误码，先看码；码认不出来（错误被包装过、
// 或换过驱动）就退回看文本。文本兜底不是偷懒——真机上报出来的原话就是
// `database is locked (5) (SQLITE_BUSY)`，而它一旦认不出，后果是"不重试"
// 而不是"重试错了"，退化方向是安全的。
func isBusyErr(err error) bool {
	var coder interface{ Code() int }
	if errors.As(err, &coder) {
		if code := coder.Code(); code == sqliteBusy || code == sqliteLocked {
			return true
		}
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked")
}

func readDBStatsOnce(ctx context.Context) (*DBStats, error) {
	stats := &DBStats{Path: models.DBPath}

	if size, err := DBFileSize(); err == nil {
		stats.FileSize = size
	}

	for _, q := range []struct {
		sql string
		dst *int64
	}{
		{`PRAGMA page_size`, &stats.PageSize},
		{`PRAGMA page_count`, &stats.PageCount},
		{`PRAGMA freelist_count`, &stats.Freelist},
		{`PRAGMA auto_vacuum`, &stats.AutoVacuum},
	} {
		if err := models.DB.WithContext(ctx).Raw(q.sql).Row().Scan(q.dst); err != nil {
			return nil, err
		}
	}

	if err := models.DB.WithContext(ctx).Raw(`
		SELECT count(*),
		       COALESCE(sum(CASE WHEN typeof(input) = 'text' THEN 1 ELSE 0 END), 0),
		       COALESCE(sum(CASE WHEN typeof(input) = 'blob' THEN 1 ELSE 0 END), 0),
		       COALESCE(sum(length(CAST(input AS BLOB))), 0)
		FROM chat_ios WHERE deleted_at IS NULL`).
		Row().Scan(&stats.Rows, &stats.PendingRows, &stats.FramedRows, &stats.ColumnBytes); err != nil {
		return nil, err
	}

	// 两张块表由 AutoMigrate 建，永远存在（即便是空表），所以这里不处理
	// "表不存在"——`SELECT count(*)` 也永远不会返回零行。
	if err := models.DB.WithContext(ctx).Raw(`SELECT count(*) FROM blocks`).
		Row().Scan(&stats.BlockRows); err != nil {
		return nil, err
	}
	if err := models.DB.WithContext(ctx).Raw(
		`SELECT count(*), COALESCE(sum(length(data)), 0) FROM block_groups`).
		Row().Scan(&stats.GroupRows, &stats.GroupBytes); err != nil {
		return nil, err
	}
	return stats, nil
}
