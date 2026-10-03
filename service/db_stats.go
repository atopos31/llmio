package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
//     由此派生出一条硬规矩：**这一句里只许出现 `typeof`**。它读记录头、
//     不读载荷；任何 `length()` / `列 <> ''` 都要把载荷读出来，成正比于字节数
//     （实测 1.36 GiB 要 0.80 秒），而这条路径在真机上一次 4.24 秒的长读
//     就足以把整站打成 500——`journal_mode=delete` 下读事务挡写。
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
	Path      string `json:"path"`
	FileSize  int64  `json:"file_size"`
	PageSize  int64  `json:"page_size"`
	PageCount int64  `json:"page_count"`
	Freelist  int64  `json:"freelist_count"`
	// DiskFree 是库所在卷的空闲字节数。给界面用来**提前算**重整（VACUUM）
	// 够不够——它要 2× 库大小的可用空间，而这个数只有服务端问得到。
	// 不提前给的话，用户点下去才被拒，而"差多少"还得再问一次。
	//
	// 它是一次 Windows API / statfs，比这组里任何一个 SQL 都便宜，
	// 加进来不会让这条本来就是"常数量级"的读变贵。
	DiskFree   int64 `json:"disk_free_bytes"`
	AutoVacuum int64 `json:"auto_vacuum"`
	Rows       int64 `json:"rows"`
	// PendingRows / FramedRows 数的是**行**，判据取三列的并集：一行里只要还有
	// 一列是明文就算待迁，只要有一列是帧就算已压。按单列数会让"input 迁完了、
	// 响应体还没"这种最常见的中间态读成"待迁移 0 行"，而那正是真机上发生过的
	// 那件事（界面报已完成，1.4 GiB 明文一行没动）。
	PendingRows int64 `json:"pending_rows"`
	FramedRows  int64 `json:"framed_rows"`
	BlockRows   int64 `json:"block_rows"`
	GroupRows   int64 `json:"block_group_rows"`
	GroupBytes  int64 `json:"block_group_bytes"`
	// 这里**没有**"这三列此刻占多少字节"。
	//
	// 曾经有（`input_column_bytes` / `output_column_bytes`），是为了让界面别像
	// 阶段 6 之前那样只报 62 MB——那时「实际占用」只算请求体列 + 组表，而库文件
	// 是 1.47 GiB，因为响应体两列的 1.36 GiB 根本没进那个口径。
	//
	// 但那个数**只能靠读载荷算**：`sum(length(CAST(列 AS BLOB)))`（帧另说，
	// 明文部分实测正比于字节数：1.36 GiB 要 0.80 秒）。它按秒轮询，就成了一次
	// 长读事务；库跑在 `journal_mode=delete` 下读事务挡写，聊天请求等锁 5 秒
	// 超时——整站 500。真机回执：回滚后的 7.6 GiB 库上这条聚合 4.24 秒。
	//
	// 界面需要的那件事有更准也更便宜的答案：**`file_size`（库文件多大）**。
	// 它本来就是常数量级的 pragma + os.Stat，而且比"三列加总"更全——把块表索引、
	// freelist、别的东西都算进去了。阶段 6 想修的那个"界面少报 1.4 GiB"，
	// 至此是由 file_size 直接回答的。
	//
	// 分块的字节（GroupBytes）留着：`block_groups` 是张小表（真机 57 MiB / 891 行），
	// 聚合它 0.03 秒，而且它没有别的口径能替代。

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
//
// **换了库就不算数。** 这份缓存是进程级的，而这组数说的是**某一个库文件**的
// 事：某一行的三列，某个 freelist 的页数。库路径一旦换了还接着用，症状是
// "形状都对、内容全是另一个库的"——它不会报错，只会让界面说假话。
// 生产里路径是写死的（`./db/llmio.db`），这条判断不花什么代价；测试里每个
// 用例一份 TempDir 下的库，正是靠它互不串味。`models/block.go` 的组缓存
// 出于同样的理由在 Init 时整个丢掉。
func cachedDBStats(maxAge time.Duration) (*DBStats, bool) {
	if dbStatsCache == nil || dbStatsCache.Path != models.DBPath {
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
		if !IsBusyErr(err) {
			return nil, err
		}
	}
	return nil, last
}

// IsBusyErr 判"这一句是不是撞上别人的写锁了"。
//
// 两手准备：驱动的 `*Error` 带错误码，先看码；码认不出来（错误被包装过、
// 或换过驱动）就退回看文本。文本兜底不是偷懒——真机上报出来的原话就是
// `database is locked (5) (SQLITE_BUSY)`，而它一旦认不出，后果是"不重试"
// 而不是"重试错了"，退化方向是安全的。
//
// 导出是给 handler 用的：状态接口在这条错上有一条退路（拿上一次的数顶一顶），
// 判据必须与这里的重试判据**同一套**——分头写迟早会漂。
func IsBusyErr(err error) bool {
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

	// 空闲磁盘：一次 statfs，比上面任何一句 SQL 都便宜，所以和它们一样每轮真读。
	// 量不到就留 0——**界面据此判"重整够不够"，0 的含义是"不够/不知道"，
	// 那个方向的错是"按钮禁用"，而不是"点了才发现"**。反过来把量不到当成
	// "空间充足"才会让人白点一次，还可能在写盘中途满盘。
	if free, err := diskFree(filepath.Dir(models.DBPath)); err == nil {
		stats.DiskFree = free
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

	// 判据直接复用迁移那一套（plaintextFilter / framedFilter）：两处口径要是
	// 分头写，界面就会给出一个与迁移自己认的候选集不一样的"待迁移行数"，
	// 而这两个数**必须**对得上——它们说的是同一件事。
	//
	// **这一句里不许出现任何读载荷的东西。** 两条判据只读 `typeof`（记录头里，
	// 真机 7.6 GiB 全表 0.076 秒），这一条是硬要求，不是优化：这个接口在任务
	// 进行中是每 2 秒被轮询一次的，而 `journal_mode=delete` 下长读事务挡写。
	// 曾经的形状里有个 `sum(length(CAST(列 AS BLOB)))`，在回滚后的库上要 4.24 秒
	// ——聊天请求的写等锁超时，整站 500。那段历史写在 DBStats 的字段说明里。
	//
	// 判据能瘦成纯 `typeof`，靠的是那条不变量：这三列里 **TEXT 只表示非空明文**
	// （空值一律 NULL），详细推导见 log_compress.go 六个判据常量上面那段。
	rowQ := fmt.Sprintf(`
		SELECT count(*),
		       COALESCE(sum(CASE WHEN %s THEN 1 ELSE 0 END), 0),
		       COALESCE(sum(CASE WHEN %s THEN 1 ELSE 0 END), 0)
		FROM chat_ios WHERE deleted_at IS NULL`, plaintextFilter, framedFilter)
	if err := models.DB.WithContext(ctx).Raw(rowQ).
		Row().Scan(&stats.Rows, &stats.PendingRows, &stats.FramedRows); err != nil {
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
