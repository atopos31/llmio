package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/pkg/env"
	"gorm.io/gorm"
)

// 本文件是**存储层**的运维动作：把 SQLite 的页还给文件系统。
//
// 它和压缩迁移是一件事的两半。迁移把 470 KiB 的明文行换成几十字节的引用帧，
// 省下的是**页**；而 SQLite 默认（auto_vacuum=0）只把页还进 freelist，
// **文件一字节都不会缩**。实测：一份 7.05 GiB 的库迁完，文件仍是 7.05 GiB。
//
// 两条路，代价差**一个**数量级（全部真机实测，见 docs/db-compression-phase5.md）。
//
//	                 耗时      额外磁盘   锁
//	VACUUM           56 s      1.0× 库    全程排他
//	增量回收          881 s     不需要     每批一个事务（段间不松手）
//
// 两行是同一份源库的两个状态：VACUUM 那行在 7.06 GiB / 12,483 行的迁后库上量，
// 增量回收那行在 6.61 GiB、freelist 1,345,736 页（占文件 78%）的库上量，收干净是
// 10 轮 14.7 分钟，文件 6.61 → 1.47 GiB。
//
// **VACUUM 比增量回收快约 16 倍**：VACUUM 是顺序重写整库，增量回收是"把文件尾部的页
// 搬到洞里去"，全库散布的 freelist 意味着海量随机 I/O——而且生产驱动上一次只放一页
// （见 reclaimPagesPerTx，那条注释是这一整个文件里最该先读的）。
// 所以**大回收该用 VACUUM**（代价：2 倍磁盘 + 一个维护窗口），增量回收的用武之地是
// **小的、经常性的**那一部分——它不需要 2 倍磁盘，也不需要把服务停下来。

const (
	// reclaimPagesPerTx 是**一个事务里连打几条** `PRAGMA incremental_vacuum`。
	//
	// 这个常数原先叫 `reclaimChunkPages`，含义是"一次调用放掉 N 页"。真机验收把它
	// 推翻了，两件事都要记在这里：
	//
	// 1. **生产驱动（glebarez/sqlite）忽略那个参数。** `incremental_vacuum(1)`、
	//    `(4096)`、无参形态，都**恰好放掉一页**；N∈{1,2,16,64,1024,4096} 逐一验过。
	//    同一份文件换 modernc.org/sqlite 直连则一次放掉约 4,093 页——**差别在驱动，
	//    不在 GORM**（同一个库用裸 database/sql 走 glebarez 也是 1 页）。
	//    ⇒ "每次 N 页"这个旋钮**在生产驱动上不存在**。
	//    （所以拆成多次调用去凑 N 是**错的**：7905 次调用放掉 7905 页，
	//     折 87 页/s，5 GiB 的洞要跑 4 个小时。）
	//
	// 2. 能旋的是**一个事务里连打多少次**：逐次调用的固定开销（建 journal / 落盘 /
	//    删 journal）与"搬了几页"无关，合进一个事务就把它摊到 K 页上。
	//    同一份 7.06 GiB 库、freelist 1.37M 页，每档跑 4 轮实测：
	//
	//	     每事务   单事务耗时     页/秒
	//	      256    2.0 s         ~128    固定开销占大头
	//	      512    1.1–1.3 s     ~430    ← 取它
	//	     1024    2.0 / 8.1 s   ~500 / 127
	//	     2048    3.3–4.2 s     ~560
	//	     4096    7.0–9.5 s     ~470
	//	    16384   31–34 s        ~500
	//
	// 但**这张表是"单事务计时"，所以它给的是地板而不是天花板**。真机上一整轮
	// （90 秒预算、批间还要查一次计数）跑出来是这个样子：
	//
	//	     轮   页/s        轮   页/s        轮   页/s
	//	      1    616         5  1,428         9  1,865
	//	      2  1,604         6  1,527        10  4,152 ← 这一轮把 freelist 放空了，
	//	      3    889         7  1,121                    61.7 s 就收工
	//	      4    914         8  1,982
	//
	// 十轮共 1,345,736 页 / 881 s，**平均 1,527 页/s（6.0 MiB/s）**，比上表最快的一档
	// 还快 3 倍，末轮更是快 8 倍。**[推]** 差异来自"每放一页要把文件最后一页搬进洞里"：
	// 那一页是活的就要读+写+记 ptrmap+写日志，是空的就只需截断。越往后文件尾部越可能
	// 整段是空的（空洞连成片），于是末轮出现 4,152 页/s 这种数量级；探针那张表量的
	// 恰好是**搬迁最密**的那一段，所以它报的是地板。
	// （这条推论**没做过对照实验**：没有分别在"尾部全空"和"尾部全活"的库上量过。）
	//
	// 取 512 而不是更快的 2048，是因为**窗口的方差比窗口本身更要命**：这几档的页/秒
	// 都带一个 ±1.5 倍的抖动（同一档重复跑能差一倍），512 标称 1.1 s、翻四倍也还在
	// busy_timeout（5 秒）以内，2048 标称 3.9 s 就没有这个余量了。换来的只是 30% 吞吐。
	//
	// 于是"每 90 秒一轮 ≈ 放掉 470 MiB"是这套常数的直接推论（前 9 个满轮实测平均
	// 472 MiB/轮）——**一个 5 GiB 的洞靠它要 10 轮、约 15 分钟**。真要把 5 GiB
	// 一次收干净，那是 VACUUM 的活（56 s，离线，2 倍磁盘）。
	reclaimPagesPerTx = 512

	// reclaimBudget 是一次调用最多占多久。到点就停，剩下的下次再放。
	//
	// 它不是性能旋钮，是**占用率旋钮**（同迁移的 BatchIntervalMs）：一轮里事务是
	// 首尾相接的，**段与段之间几乎不松手**（松手的时间以微秒计），所以"这 90 秒里
	// 写请求基本都在排队并最终超时失败"是实话。要留缝就得靠这个预算把一轮切短，
	// 让用户/调度器在两轮之间决定还要不要接着放。
	reclaimBudget = 90 * time.Second

	// reclaimDiskMargin 是启动期 VACUUM 预检要求的最低空闲。
	// VACUUM 全程要一份和库等大的临时副本（实测峰值额外占用 1.00× 库）。
	reclaimDiskMargin = 64 << 20
)

var reclaimInFlight atomic.Bool

// ReclaimRunning 报"有没有一轮回收在跑"。
func ReclaimRunning() bool { return reclaimInFlight.Load() }

// ── 启动期 ────────────────────────────────────────────────────────────────

// PrepareStorage 在**监听端口之前**跑一次。它是"启动期不该有副作用"这条
// 规矩的两个例外，两个都是显式开关（默认全不动）：
//
//  1. `DB_AUTO_VACUUM_REBUILD=on`：把既有库转成 auto_vacuum=INCREMENTAL。
//     转换 = 同一条连接上 `PRAGMA auto_vacuum=2; VACUUM`，实测 7.05 GiB 库
//     **55–59 秒**，文件只多 8.8 MiB（+0.12%）。默认 `auto` 只对**空库**做
//     （见 models.PrepareEmptyStorage），既有库**一个字节都不碰**。
//  2. `DB_VACUUM=true`：既有的启动期 VACUUM，语义不变。
//
// 两者都**失败即跳过**，绝不让服务起不来（存储层的优化没有这个资格）。
// 原先 `DB_VACUUM` 是 `panic(err)`，而它连磁盘够不够都没看过。
func PrepareStorage(ctx context.Context) {
	size, err := DBFileSize()
	if err != nil {
		// 库文件读不到不是这里该管的事：models.Init 已经把库建出来了。
		slog.Warn("storage: 量不到库文件大小，跳过存储层维护", "error", err)
		return
	}

	vacuumRequested := env.GetWithDefault(models.DBVacuumEnv, false)
	rebuild := models.StorageRebuildMode()

	av, err := models.ReadAutoVacuum(models.DB)
	if err != nil {
		slog.Warn("storage: 读 auto_vacuum 失败，跳过存储层维护", "error", err)
		return
	}

	// 转换同时满足"要 VACUUM"——省掉第二次全库重写。
	convert := rebuild == "on" && av != models.AutoVacuumIncremental
	if !convert && !vacuumRequested {
		return
	}

	// 预检放在**动手之前**：VACUUM 中途磁盘满会把库留在一个尴尬的状态，
	// 而这是唯一能在动手之前知道的事。
	free, err := diskFree(filepath.Dir(models.DBPath))
	if err != nil {
		slog.Warn("storage: 查不到磁盘空闲，跳过存储层维护", "error", err)
		saveLogReclaimState(models.LogReclaimState{
			Status: "failed", StopReason: "failed",
			LastError: fmt.Sprintf("查磁盘空闲失败：%v", err),
		})
		return
	}
	need := 2*size + reclaimDiskMargin
	if !enoughDisk(free, size) {
		slog.Warn("storage: 磁盘不够，跳过存储层维护",
			"free", free, "need", need, "db_size", size)
		saveLogReclaimState(models.LogReclaimState{
			Status: "done", StopReason: "insufficient_space",
			FileSizeBefore: size, FileSizeAfter: size,
			LastError: fmt.Sprintf("空闲 %d < 需要 %d", free, need),
		})
		return
	}

	before, _ := readStorageCounters()
	start := time.Now()
	rec := models.LogReclaimState{
		Status:         "running",
		Source:         "startup",
		FileSizeBefore: size,
		StartedAt:      start.Format(time.RFC3339),
	}

	err = vacuumOnOneConnection(ctx, convert)
	rec.DurationMs = time.Since(start).Milliseconds()
	rec.FinishedAt = time.Now().Format(time.RFC3339)
	after, _ := readStorageCounters()
	rec.FileSizeAfter = after.fileSize
	rec.FreelistBefore, rec.FreelistAfter = before.freelist, after.freelist
	rec.FreedPages = before.freelist - after.freelist
	rec.FreedBytes = rec.FreedPages * before.pageSize

	switch {
	case err != nil:
		rec.Status, rec.StopReason = "failed", "failed"
		rec.LastError = err.Error()
		slog.Error("storage: 启动期整理失败（服务照常启动）", "error", err)
	case convert:
		rec.Status, rec.StopReason = "done", "converted"
		slog.Info("storage: 已转成 auto_vacuum=INCREMENTAL",
			"duration", time.Since(start), "file_size", rec.FileSizeAfter)
	default:
		rec.Status, rec.StopReason = "done", "vacuumed"
		slog.Info("storage: 启动期 VACUUM 完成",
			"duration", time.Since(start), "file_size", rec.FileSizeAfter)
	}
	saveLogReclaimState(rec)
}

// enoughDisk 判"这些空闲够不够 VACUUM 这份库"。
//
// 单独成一个函数只为了能直接验：真去造一个"磁盘快满"的现场要挂载点级别的操作，
// 而这条判据错了的后果是**VACUUM 中途磁盘满**——库被留在一个说不清的状态。
// 门槛是 2× 库 + 余量：VACUUM 全程持有一份和库等大的临时副本（实测峰值 1.00× 库）。
func enoughDisk(free, dbSize int64) bool {
	return free >= 2*dbSize+reclaimDiskMargin
}

// vacuumOnOneConnection 在**同一条连接**上执行 `PRAGMA auto_vacuum=?; VACUUM`。
//
// 必须同一条连接：实测非空库上单独设 pragma 是被静默忽略的（同连接、新连接都
// 读回 0，连文件头都没写），只有**同一个进程、同一条连接**上的第一次 VACUUM
// 才按它重建——于是 auto_vacuum 才落进文件头、对新连接生效。
//
// VACUUM 不能在事务里跑，所以这里不用 Transaction，用 Connection（钉住一条
// 连接，但不开会话事务）。
func vacuumOnOneConnection(ctx context.Context, convert bool) error {
	return models.DB.WithContext(ctx).Connection(func(tx *gorm.DB) error {
		if convert {
			if err := tx.Exec(`PRAGMA auto_vacuum = 2`).Error; err != nil {
				return fmt.Errorf("设 auto_vacuum: %w", err)
			}
		}
		if err := tx.Exec(`VACUUM`).Error; err != nil {
			return fmt.Errorf("VACUUM: %w", err)
		}
		return nil
	})
}

// ── 增量回收 ──────────────────────────────────────────────────────────────
//
// 这一节有三个入口，分两层：
//
//	StartReclaim      占位 + 后台跑     ← HTTP 端点用
//	ReclaimFreePages  占位 + 同步跑完   ← 测试与内部用
//	reclaimFreePages  只干活，不占位    ← 上面两个共用
//
// 占位（"此刻有一轮在跑"）与干活分开，是为了让 HTTP 那条路能在**写响应之前**
// 就把位占上（理由见 StartReclaim）。

// ReclaimFreePages 同步跑一轮回收，跑完才返回。
//
// 内部与测试用这一条；HTTP 端点用 StartReclaim（见它，占位必须发生在
// 响应之前，理由写在那里）。
func ReclaimFreePages(ctx context.Context) (*models.LogReclaimState, error) {
	// 一轮只能有一个：抢锁只挡得住"同时"，挡不住"交替"——两轮首尾相接时
	// 第二轮的 TryLock 是能成功的（第一轮已经放锁了）。
	if !reclaimInFlight.CompareAndSwap(false, true) {
		return nil, ErrReclaimRunning
	}
	defer reclaimInFlight.Store(false)
	return reclaimFreePages(ctx)
}

// StartReclaim 占位之后就返回，回收在后台跑。
//
// **占位（CAS）必须在返回之前同步做完**，这不是洁癖，是两个真问题：
//
//  1. 接口答的 `started: true` 得是真的。若 CAS 留在 goroutine 里，"已经有一轮
//     在跑"这件事在响应写出去的时候还没成立——那一刻连点两下会**两个都返回
//     成功**，第二下把第一下的结果覆盖掉（两份都写回收记录）。
//  2. 前端要据此立刻显示"回收中"。留一个空档的话，轮询得靠一个额外的客户端
//     标志兜住（迁移那条路就是这么兜的），而这里能做得更干净。
func StartReclaim(ctx context.Context) error {
	if !reclaimInFlight.CompareAndSwap(false, true) {
		return ErrReclaimRunning
	}
	go func() {
		defer reclaimInFlight.Store(false)
		// 请求的 ctx 在响应写完之后就被取消了。半途而废必须是"进程被 kill"，
		// 不能是"用户关了个标签页"——所以脱掉取消，只留值。
		if _, err := reclaimFreePages(context.WithoutCancel(ctx)); err != nil {
			slog.Error("storage: 增量回收没能跑起来", "error", err)
		}
	}()
	return nil
}

// reclaimFreePages 是回收本身，不带占位（占位由上面两个入口负责）。
func reclaimFreePages(ctx context.Context) (*models.LogReclaimState, error) {
	// 与迁移、日志清理互斥。**必须在开事务之前拿**：回收自己现在也开事务了
	// （每批一个，见 reclaimBatch），而它要挡住的正是别人此时开写事务。
	if !maintenanceMu.TryLock() {
		return nil, ErrMaintenanceBusy
	}
	defer maintenanceMu.Unlock()

	before, err := readStorageCounters()
	if err != nil {
		return nil, err
	}
	rec := models.LogReclaimState{
		Status:         "running",
		Source:         "manual",
		FileSizeBefore: before.fileSize,
		FreelistBefore: before.freelist,
		StartedAt:      time.Now().Format(time.RFC3339),
	}

	// auto_vacuum 不是 INCREMENTAL 时，`PRAGMA incremental_vacuum` 是个
	// **0.000 秒的空操作**（实测），文件一字节都不会缩。不说出来的话，
	// 用户看到的是"点了回收、瞬间完成、什么都没变"。
	if before.autoVacuum != models.AutoVacuumIncremental {
		rec.Status, rec.StopReason = "done", "no_auto_vacuum"
		finishReclaim(&rec, before)
		saveLogReclaimState(rec)
		return &rec, nil
	}

	start := time.Now()
	// freelist 每批重新量一次。起始那份用 before 里的，省一次查询——它和
	// readStorageCounters 量的是同一件事。
	freelist := before.freelist
	for {
		batch := reclaimNextBatch(freelist)
		if batch == 0 {
			// 起始就没有空洞。上面的 auto_vacuum 检查已经挡掉了"根本没开"，
			// 走到这里说明是"开着的、但此刻没洞可放"。
			rec.Status, rec.StopReason = "done", "empty"
			break
		}
		if err := reclaimBatch(ctx, batch); err != nil {
			rec.Status, rec.StopReason = "failed", "failed"
			rec.LastError = err.Error()
			slog.Error("storage: 增量回收失败", "error", err)
			break
		}
		rec.Calls += batch

		now, err := readStorageCounters()
		if err != nil {
			rec.Status, rec.StopReason = "failed", "failed"
			rec.LastError = err.Error()
			break
		}
		if stop := reclaimVerdict(freelist, now.freelist, time.Since(start)); stop != "" {
			if stop == "stalled" {
				rec.LastError = fmt.Sprintf("这批放完 freelist 反而没减少：%d → %d", freelist, now.freelist)
				slog.Warn("storage: 增量回收原地踏步，收工", "freelist", now.freelist)
			}
			rec.Status, rec.StopReason = "done", stop
			break
		}
		freelist = now.freelist
	}

	finishReclaim(&rec, before)
	rec.DurationMs = time.Since(start).Milliseconds()
	saveLogReclaimState(rec)
	slog.Info("storage: 增量回收收工",
		"原因", rec.StopReason, "放掉页", rec.FreedPages,
		"文件", rec.FileSizeBefore, "→", rec.FileSizeAfter, "耗时", rec.DurationMs)
	return &rec, nil
}

// reclaimNextBatch 决定这一批打多少条语句：要么打满，要么只打剩下的。
//
// 单独拎出来是因为"剩下的不足一批"这条分支在小库上一闪而过（测试库几十页，
// 走一次就空了），而它错了的后果是**每轮都白打几百条空转语句**——空转也在写事务里。
func reclaimNextBatch(freelist int64) int {
	if freelist <= 0 {
		return 0
	}
	if freelist < reclaimPagesPerTx {
		return int(freelist)
	}
	return reclaimPagesPerTx
}

// reclaimVerdict 是"这一批放完之后怎么办"的全部判断，抽成纯函数好逐条验。
// 返回空串表示接着放，否则返回收工理由：
//
//	empty    放完了
//	stalled  放了却一页没少——引擎行为反常，必须报出来
//	budget   到点收工，剩下的下次再放
//
// **优先级是有意的**：`empty` 压过一切（放完了就该报放完了，跟花了多久无关）；
// `stalled` 压过 `budget`——两者同时成立时，"跑了 90 秒一页没放"比"到点了"更该被看见。
//
// `stalled` 这条守卫是被真机吓出来的：这个 pragma 曾经在悄悄忽略参数的情况下
// 一次只放一页（见 reclaimPagesPerTx），也完全可能哪天变成一页都不放。真到那一步，
// 界面上"到点收工、剩余下次再放"会看不出任何毛病——它看起来和正常收工一模一样。
func reclaimVerdict(freelistBefore, freelistAfter int64, elapsed time.Duration) string {
	switch {
	case freelistAfter == 0:
		return "empty"
	case freelistAfter >= freelistBefore:
		return "stalled"
	case elapsed >= reclaimBudget:
		return "budget"
	}
	return ""
}

// reclaimBatch 在一个事务里连打 batch 条 `PRAGMA incremental_vacuum`。
//
// **必须在一个事务里**，理由就是 reclaimPagesPerTx 那张表：逐条执行时每一条都是
// 独立事务（建 journal / 落盘 / 删 journal），那份固定开销与"搬了几页"无关，
// 折下来约 9–10 ms/页、100 页/s——5 GiB 的洞要跑四个钟头。合起来是实测的
// 616–4,152 页/s（见 reclaimPagesPerTx），也就是**几十倍**的差别。
//
// 参数恒写 1 是刻意的：生产驱动忽略它、一条恰好放一页（同前），写 1 保证的是
// **语义**——将来换了驱动、这个参数真被认了，这一批仍然恰好放 batch 页，
// 而不是一次放掉 batch×batch 页。
func reclaimBatch(ctx context.Context, batch int) error {
	return models.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i := 0; i < batch; i++ {
			if err := tx.Exec(`PRAGMA incremental_vacuum(1)`).Error; err != nil {
				return fmt.Errorf("第 %d 条 incremental_vacuum: %w", i+1, err)
			}
		}
		return nil
	})
}

func finishReclaim(rec *models.LogReclaimState, before storageCounters) {
	after, err := readStorageCounters()
	rec.FinishedAt = time.Now().Format(time.RFC3339)
	if err != nil {
		// 收尾这一量失败不该把整件事报成失败：放掉多少页是事实，
		// 而"文件现在多大"只是画面。
		slog.Warn("storage: 回收后量库失败", "error", err)
		rec.FileSizeAfter = before.fileSize
		rec.FreelistAfter = before.freelist
		return
	}
	rec.FileSizeAfter = after.fileSize
	rec.FreelistAfter = after.freelist
	rec.FreedPages = before.freelist - after.freelist
	rec.FreedBytes = rec.FreedPages * before.pageSize
	// 收尾这一次必须重新读 auto_vacuum：`converted` 那条路上它刚从 0 变成 2。
	rec.PageSize = after.pageSize
}

// storageCounters 是回收要用的那几个数。单独抽出来而不是复用 DBStats：
// 这里要的是**此刻**的数，不能走那 5 秒冷却（回收前后各量一次，差就是结果）。
type storageCounters struct {
	pageSize   int64
	freelist   int64
	fileSize   int64
	autoVacuum int64
}

func readStorageCounters() (storageCounters, error) {
	var c storageCounters
	size, err := DBFileSize()
	if err != nil {
		return c, err
	}
	c.fileSize = size
	for _, q := range []struct {
		sql string
		dst *int64
	}{
		{`PRAGMA page_size`, &c.pageSize},
		{`PRAGMA freelist_count`, &c.freelist},
		{`PRAGMA auto_vacuum`, &c.autoVacuum},
	} {
		if err := models.DB.Raw(q.sql).Row().Scan(q.dst); err != nil {
			return c, err
		}
	}
	return c, nil
}

// ── 状态 ──────────────────────────────────────────────────────────────────

// ErrReclaimRunning 表示已经有一轮回收在跑。
var ErrReclaimRunning = errors.New("storage: 已经有一轮回收在跑")

// ErrReclaimNotNeeded 表示这次回收没动手（auto_vacuum 没开）。
var ErrReclaimNotNeeded = errors.New("storage: auto_vacuum 不是 INCREMENTAL，增量回收是空操作")

// GetLogReclaimState 读上一次回收的记录。
func GetLogReclaimState(ctx context.Context) (*models.LogReclaimState, error) {
	state := &models.LogReclaimState{Status: "idle"}
	config, err := gorm.G[models.Config](models.DB).
		Where("key = ?", models.KeyLogReclaimState).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return state, nil
		}
		return nil, err
	}
	if config.Value == "" {
		return state, nil
	}
	if err := json.Unmarshal([]byte(config.Value), state); err != nil {
		return nil, fmt.Errorf("unmarshal log reclaim state: %w", err)
	}
	if state.Status == "" {
		state.Status = "idle"
	}
	return state, nil
}

// saveLogReclaimState 记一条回收记录。**失败只记日志**：
// 一次存储层整理没被记下来，不该让调用它的那条路失败。
func saveLogReclaimState(rec models.LogReclaimState) {
	raw, err := json.Marshal(rec)
	if err != nil {
		slog.Error("storage: 序列化回收记录失败", "error", err)
		return
	}
	if err := models.SaveConfigValue(context.Background(),
		models.KeyLogReclaimState, string(raw)); err != nil {
		slog.Error("storage: 写回收记录失败", "error", err)
	}
}
