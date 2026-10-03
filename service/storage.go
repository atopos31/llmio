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
// （见 reclaimBatchMax，那条注释是这一整个文件里最该先读的）。
// 所以**大回收该用 VACUUM**（代价：2 倍磁盘 + 一个维护窗口），增量回收的用武之地是
// **小的、经常性的**那一部分——它不需要 2 倍磁盘，也不需要把服务停下来。

const (
	// reclaimBatchMax 是**一个事务里连打几条** `PRAGMA incremental_vacuum` 的
	// **上限**（实际批量按每批实测耗时自适应，见此段末尾与 nextBatchSize）。
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
	//
	// ── 但"取 512"这件事本身被真机推翻了 ──
	//
	// 上面整段的前提是"512 条 ≈ 1.1 秒，翻四倍也还在 busy_timeout 以内"。那个
	// 数只对当时那份库成立。换一份 freelist 1.83M 页的库（同样三列全明文、
	// 形态更散），512 条实测要 **8.9 秒**（111 轮 / 1000 秒真机回执），
	// 直接越过 busy_timeout 的 5 秒——持续回收承诺的"写请求排队但成功"
	// 当场破产：那个窗口里 11 条聊天请求，6 条 `SQLITE_BUSY` 500，
	// 活下来的 5 条也都花了 3.9–5.9 秒。
	//
	// 两份库同样是 512 条，差 8 倍。**没有任何一个常数能同时适配两者**，
	// 所以批量不再定死，按上一批的实测耗时外推（见 nextBatchSize）。
	// 下面几个是它的边界与起点。
	reclaimBatchMax = 512
	reclaimBatchMin = 16
	// reclaimBatchStart 是**每一趟**回收的起始批量，不复用上一趟的终值：
	// 库的形态每一趟都在变（VACUUM 之后 freelist 归零、迁移之后又涨回来），
	// 拿上一趟的结论当这一趟的起点没有根据。
	//
	// 取 64 而不是 512，是因为**第一批无从预知库有多慢，而它照样要付代价**：
	// 512 在慢库上第一批就是 8.9 秒，够打死一批写请求了。64 在慢库上约 1.1 秒、
	// 在快库上 0.14 秒，两边都在 busy_timeout 以内；快库上它几批之内自己就涨上去。
	reclaimBatchStart = 64
	// reclaimBatchTarget 是一批**想要**占多久，取 busy_timeout 的一半。
	//
	// 留一半余量给两件事：这一批比估的长（那 ±1.5 倍的抖动），以及写请求
	// 排在别的写请求后面而不是排在这批后面。它不是"越快越好"——批越小，
	// 每条语句分摊到的固定开销越高，收完整个洞要更久。
	reclaimBatchTarget = 2500 * time.Millisecond

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

	// reclaimContinuousPause 是**持续回收**里轮与轮之间的松手时间。
	//
	// 持续模式下一轮恒等于一批（见 reclaimBatchesPerRound），所以"一批一轮 +
	// 松手这么久"合起来是一条对写请求的**硬保证**，而不是修辞：
	//
	//	  写请求的耐心是 busy_timeout = 5 秒（超了就失败）。
	//	  一批的目标耗时是 reclaimBatchTarget = 2.5 秒，
	//	  所以任何一把写锁最多被握两秒半左右 —— 写请求等得过。
	//
	// **这条保证曾经不成立，而那就是线上故障的另一半。** 它原先挂在"一批 512 条
	// 标称 1.1 秒、实测最坏 4.4 秒"这个数上，可那个数只对当时那份库成立：换一份
	// freelist 1.83M 页的库，同样 512 条要 **8.9 秒**，越过 5 秒耐心，那个窗口里
	// 11 条聊天请求有 6 条 `SQLITE_BUSY` 500。所以保证现在不挂在"某个常数够小"
	// 上，而是挂在**按实测耗时自我调整的批量**上（见 nextBatchSize）——那是它唯一
	// 能成立的形式，因为"一批要多久"随库的形态走，事先算不出来。
	//
	// 这条保证是持续回收**比连点十次更好**的全部理由。原先一轮 90 秒、段间不松手
	// （见 reclaimBudget），那 90 秒里写请求的 5 秒耐心必然耗尽、直接失败；用户
	// 想收掉一个 5 GiB 的洞就得连点十次、每次付 90 秒的写入失败。现在点一次，
	// 写请求改成"排队但成功"。
	//
	// **为什么是 100 ms 而不是 1 秒**：窗口要够长，长到等待中的写请求能撞进来；
	// 但不必长到"一撞就中"。SQLite 的 busy handler 是递增睡眠重试（1/2/5/10/…/100 ms），
	// 所以一个 100 ms 的窗口最坏要两三个窗口才被撞上——那也还是**一批之内的事**，
	// 加在 2.5 秒上仍在 5 秒以内。而 1 秒的窗口要付的代价大得多：真机那一趟放了 2,628 批，
	// 每批歇 1 秒就是 **44 分钟**的纯等待（整趟从 15 分钟变成约 1 小时），
	// 100 ms 只要 4–5 分钟（变成约 19 分钟）。
	//
	// 那个"2,628 批"是算出来的不是量出来的：真机十轮 881 s 放掉 1,345,736 页，
	// 折算每轮约 268 批 / 每批约 0.34 s——**比探针表里的 1.1 s 快得多**，因为探针
	// 量的是搬迁最密的一段（同 reclaimBatchMax 的"地板"注释）。
	reclaimContinuousPause = 100 * time.Millisecond

	// 定时回收的默认门槛与周期。
	//
	// 门槛 256 MiB：低于它，一趟回收省下的空间与"占一次写锁、翻一遍整库"的
	// 打扰不成比例。周期 1 小时：回收是删了东西之后的收尾动作，实时性没有价值。
	defaultReclaimMinBytes = 256 << 20
	defaultReclaimCheckSec = 3600
	reclaimMaxCheckSec     = 86400
	reclaimMinCheckSec     = 60
	reclaimMaxMinBytes     = 1 << 40
)

var reclaimInFlight atomic.Bool

// reclaimStopRequested 是「停止」按钮。放内存里而不是每轮去读 Config：
// 停止是操作员的即时动作，不该等到下一轮落库才被看见。
var reclaimStopRequested atomic.Bool

// reclaimKind 记"此刻在跑的是哪一条路"（models.ReclaimKindReclaim / ReclaimKindVacuum）。
//
// reclaimInFlight 只答"有没有"，不答"是什么"，而这两件事在界面上是两套说法：
// **重整不能中途停**（它是一条 VACUUM 语句，没有"批间"这个时机），耗时也完全不同
// （分钟 vs 秒级）。没有这个标志，用户点完重整看到的是「回收中」加一个按不动的
// 「停止」——两句话都是错的。
//
// 放在内存里同理：它是**此刻**的事实，不是回执。回执里那份 Kind 属于**上一次
// 跑完的**那趟任务（running 记录要等收工才落库），拿它当"正在干什么"会张冠李戴。
var reclaimKind atomic.Value

// ReclaimRunning 报"有没有一轮回收在跑"。
func ReclaimRunning() bool { return reclaimInFlight.Load() }

// ReclaimingKind 报"此刻在跑的是哪一条路"，没在跑就是空串。
//
// 认不出的值时按增量回收答。那个分支只有两种来路：将来加了第三种维护任务，
// 或者这里读到一个没写过的值——两种情况下界面拿到的都该是**它认识的那一套**
// （"回收中 + 可停止"），而不是一片空白。真按错了的代价是「停止」按钮报一句
// 说清的错（见 handler.StopReclaim），比整块状态说不清轻得多。
func ReclaimingKind() string {
	if !reclaimInFlight.Load() {
		return ""
	}
	if k, ok := reclaimKind.Load().(string); ok && k == models.ReclaimKindVacuum {
		return models.ReclaimKindVacuum
	}
	return models.ReclaimKindReclaim
}

// ReclaimStopping 报"有没有人按了停止、但它还没退出来"。
//
// 界面需要这个中间态：批与批之间才生效，所以按下之后到真正退出之间有一段
// （最多一批，约 1–4 秒）。这段时间里按钮不该恢复成"再点一次"——
// 那会让用户以为没生效，然后再按一下，而后端会把第二下挡掉（ErrReclaimRunning），
// 界面就成了"点了没反应"。
func ReclaimStopping() bool { return reclaimInFlight.Load() && reclaimStopRequested.Load() }

// StopReclaim 请求停止持续回收（批间生效）。返回 false 表示当前没有回收在跑。
//
// **重整（vacuum）不接受停止**，也返回 false。它不是"停不了就算了"——VACUUM 是一条
// 语句，没有批间这个时机，标志设了没人读；答 true 等于骗用户按了个没用的按钮。
// 界面据 ReclaimingKind 根本不画那个按钮，这里是给直接调接口的人兜底。
func StopReclaim() bool {
	if !reclaimInFlight.Load() || ReclaimingKind() == models.ReclaimKindVacuum {
		return false
	}
	reclaimStopRequested.Store(true)
	return true
}

// ReclaimOptions 是一次回收的调用参数。
type ReclaimOptions struct {
	// Continuous 为真时一直跑到放完（或被打断），而不是跑满一轮就交还。
	Continuous bool
	// Source 记进回执，见 models.LogReclaimState.Source。
	Source string
}

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
		Kind:           models.ReclaimKindVacuum,
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
		// VACUUM 要 EXCLUSIVE 锁：别的连接只要持着 SHARED（一次查询就够）它就得等。
		// 启动期那条路没有并发，默认的 5 秒绰绰有余；但**运行时**那条路
		// （用户在控制台点「立即重整」）聊天请求不断进来，5 秒往往不够——
		// 而 VACUUM 抢不到锁是直接失败，不是变慢。所以这条连接上放宽到 60 秒：
		// 它等的是一个正在返回的响应，不是一个永远不来的东西。
		//
		// 这是**等锁**的耐心，不是 VACUUM 本身的耗时。一旦拿到锁它就全程排他，
		// 那段时间里别的写请求会按各自的 5 秒耐心失败——那是 VACUUM 的固有代价，
		// 界面必须说清楚（见 handler.VacuumStorage）。
		if err := tx.Exec(`PRAGMA busy_timeout = 60000`).Error; err != nil {
			return fmt.Errorf("放宽 busy_timeout: %w", err)
		}
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

// ── 重整（VACUUM）────────────────────────────────────────────────────────
//
// 它是增量回收的**对症版本**。本文件开头那张表说得很直接：同一个"把文件变小"
// 的目标，VACUUM 56 秒、增量回收 881 秒，差 16 倍，但前者要 2× 库大小的空闲
// 磁盘**和**一个全程排他的窗口。所以两者不是替代关系，是分工：
//
//	大空洞（迁移、回滚、批量删除之后）→ VACUUM，一次还干净
//	小的、经常性的那部分            → 增量回收，不必 2× 磁盘、不必停服务
//
// 真机上的教训是**这条分工原先只有增量那一半**：用户把 7 GiB 的库压到 155 MiB，
// 面对 6.2 GB 的 freelist，控制台上唯一的按钮是增量回收——按实测的 57 页/s
// 要跑 7 个多小时，而且每一批都在跟聊天请求抢写锁（见 reclaimBatchTarget）。

// VacuumPlan 是动手之前能算出来的那几个数，供预检与回话。
type VacuumPlan struct {
	// FileSize 是库文件当前大小。
	FileSize int64 `json:"file_size"`
	// Free 是库所在卷的空闲字节数。
	Free int64 `json:"free_bytes"`
	// Need 是这一次 VACUUM 要的空闲字节（2× 库 + 余量，见 enoughDisk）。
	Need int64 `json:"need_bytes"`
}

// ErrVacuumNoSpace 表示空闲磁盘不够这一次 VACUUM 周转。
//
// 它是**预检**拦下来的，不是跑到一半才失败：VACUUM 中途磁盘满会把库留在一个
// 说不清的状态（日志写了一半、临时文件写不下），而这恰恰是唯一能在动手之前
// 知道的事——所以宁可一次都别开始。
var ErrVacuumNoSpace = errors.New("storage: 空闲磁盘不够这一次重整")

// planVacuum 量出预检要的三个数。
func planVacuum() (*VacuumPlan, error) {
	size, err := DBFileSize()
	if err != nil {
		return nil, err
	}
	free, err := diskFree(filepath.Dir(models.DBPath))
	if err != nil {
		return nil, err
	}
	return &VacuumPlan{
		FileSize: size,
		Free:     free,
		Need:     2*size + reclaimDiskMargin,
	}, nil
}

// StartVacuum 预检 + 占位，然后 VACUUM 在后台跑。
//
// 预检与占位都是**同步**做的，理由同 StartReclaim：接口答的"开始了"必须在
// 响应写出去之前就是真的，否则连点两下两个都答成功、第二个把第一个覆盖掉。
// 返回的 plan 无论成败都填好，让 handler 能把"要多少、有多少"直接说给用户听
// ——磁盘不够的时候，这两句话比一句"失败"有用得多。
//
// 与启动期那条路的区别只有两点：它要跟聊天请求抢锁（见 vacuumOnOneConnection
// 里那句 busy_timeout），以及它写一条 Source=manual 的回执。
func StartVacuum(ctx context.Context) (*VacuumPlan, error) {
	plan, err := planVacuum()
	if err != nil {
		return nil, err
	}
	if !enoughDisk(plan.Free, plan.FileSize) {
		return plan, ErrVacuumNoSpace
	}
	if err := beginReclaim(models.ReclaimKindVacuum); err != nil {
		return plan, err
	}
	go func() {
		defer reclaimInFlight.Store(false)
		// 脱掉取消，理由同 StartReclaim：半途而废必须是"进程被 kill"，不能是
		// "用户关了个标签页"——一个跑到一半的 VACUUM 正是最不该被打断的东西。
		if _, err := vacuumStorage(context.WithoutCancel(ctx)); err != nil {
			slog.Error("storage: 重整没能跑起来", "error", err)
		}
	}()
	return plan, nil
}

// vacuumStorage 是 VACUUM 本身，不带占位（占位由 StartVacuum 负责）。
func vacuumStorage(ctx context.Context) (*models.LogReclaimState, error) {
	// 与迁移、增量回收、日志清理互斥。VACUUM 要的是整库排他，比谁都霸道。
	if !maintenanceMu.TryLock() {
		return nil, ErrMaintenanceBusy
	}
	defer maintenanceMu.Unlock()

	before, err := readStorageCounters()
	if err != nil {
		return nil, err
	}
	rec := models.LogReclaimState{
		Kind:           models.ReclaimKindVacuum,
		Status:         "running",
		Source:         "manual",
		FileSizeBefore: before.fileSize,
		FreelistBefore: before.freelist,
		StartedAt:      time.Now().Format(time.RFC3339),
	}

	start := time.Now()
	err = vacuumOnOneConnection(ctx, false)
	rec.DurationMs = time.Since(start).Milliseconds()
	// finishReclaim 先跑：失败也要把"现在文件多大"量出来，回执里才有得看。
	finishReclaim(&rec, before)
	if err != nil {
		rec.Status, rec.StopReason = "failed", "failed"
		rec.LastError = err.Error()
		saveLogReclaimState(rec)
		slog.Error("storage: 重整失败", "error", err, "耗时", rec.DurationMs)
		return &rec, err
	}
	rec.Status, rec.StopReason = "done", "vacuumed"
	saveLogReclaimState(rec)
	slog.Info("storage: 重整（VACUUM）完成",
		"文件", rec.FileSizeBefore, "→", rec.FileSizeAfter,
		"放掉页", rec.FreedPages, "耗时", rec.DurationMs)
	return &rec, nil
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

// ReclaimFreePages 同步跑**一轮**回收，跑完才返回。
//
// 内部与测试用这一条；HTTP 端点用 StartReclaim（见它，占位必须发生在
// 响应之前，理由写在那里）。
func ReclaimFreePages(ctx context.Context) (*models.LogReclaimState, error) {
	return reclaimGuarded(ctx, ReclaimOptions{Source: "manual"})
}

// ReclaimUntilDone 同步跑到放完（或被要求停），中途每批松手一次。
//
// "持续回收"的语义就是它：一个 5 GiB 的洞要十轮，用户不该为此点十次。
// 关于它对写请求的保证（以及为此付出的时间），见 reclaimContinuousPause。
func ReclaimUntilDone(ctx context.Context) (*models.LogReclaimState, error) {
	return reclaimGuarded(ctx, ReclaimOptions{Continuous: true, Source: "manual"})
}

func reclaimGuarded(ctx context.Context, opts ReclaimOptions) (*models.LogReclaimState, error) {
	if err := beginReclaim(models.ReclaimKindReclaim); err != nil {
		return nil, err
	}
	defer reclaimInFlight.Store(false)
	return reclaimFreePages(ctx, opts)
}

// beginReclaim 抢下"这一趟归我"，并清掉上一趟可能留下的停止标志。
//
// 一次只能有一个：抢锁只挡得住"同时"，挡不住"交替"——两趟首尾相接时第二趟的
// TryLock 是能成功的（第一趟已经放锁了），所以另有一道 CAS。
//
// **清标志必须发生在抢到 CAS 之后**：抢不到就说明有一趟正在跑，那个标志是
// 操作员发给它的，这里动它等于吞掉一次「停止」。
//
// kind 是这一趟走的是哪条路（见 ReclaimingKind）。它与 CAS 在同一处落地，
// 因为"占上了"与"占上的是什么"必须是同一个瞬间的事实。
func beginReclaim(kind string) error {
	if !reclaimInFlight.CompareAndSwap(false, true) {
		return ErrReclaimRunning
	}
	reclaimKind.Store(kind)
	reclaimStopRequested.Store(false)
	return nil
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
func StartReclaim(ctx context.Context, opts ReclaimOptions) error {
	if err := beginReclaim(models.ReclaimKindReclaim); err != nil {
		return err
	}
	go func() {
		defer reclaimInFlight.Store(false)
		// 请求的 ctx 在响应写完之后就被取消了。半途而废必须是"进程被 kill"，
		// 不能是"用户关了个标签页"——所以脱掉取消，只留值。
		if _, err := reclaimFreePages(context.WithoutCancel(ctx), opts); err != nil {
			slog.Error("storage: 增量回收没能跑起来", "error", err)
		}
	}()
	return nil
}

// reclaimFreePages 是回收本身，不带占位（占位由上面几个入口负责）。
//
// 它是一层**轮循环**，套着原来那个批循环。单轮模式下这层循环只转一圈，行为与
// 重构之前逐字一致；持续模式下它每转一圈就是一批（见 reclaimBatchesPerRound），
// 转到放完、被打断、或轮间的锁被别人抢走为止。
func reclaimFreePages(ctx context.Context, opts ReclaimOptions) (*models.LogReclaimState, error) {
	// 与迁移、日志清理互斥。**必须在开事务之前拿**：回收自己开事务
	// （每批一个，见 reclaimBatch），而它要挡住的正是别人此时开写事务。
	if !maintenanceMu.TryLock() {
		return nil, ErrMaintenanceBusy
	}
	// 锁在持续模式下会被**放掉再拿回来**（每次轮间松手），所以不能用 defer
	// 一把梭——那样松手之后会二次 Unlock 直接 panic。
	locked := true
	defer func() {
		if locked {
			maintenanceMu.Unlock()
		}
	}()

	before, err := readStorageCounters()
	if err != nil {
		return nil, err
	}
	rec := models.LogReclaimState{
		Kind:           models.ReclaimKindReclaim,
		Status:         "running",
		Source:         opts.Source,
		Continuous:     opts.Continuous,
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
	// 这一趟的额定批量。**每趟从起点重来**，理由见 reclaimBatchStart。
	batchSize := reclaimBatchStart
	for {
		roundStart := time.Now()
		maxBatches := reclaimBatchesPerRound(opts.Continuous)
		batches := 0
		verdict := ""

		for {
			if reclaimStopRequested.Load() {
				verdict = "stopped"
				break
			}
			batch := reclaimNextBatch(freelist, batchSize)
			if batch == 0 {
				// 起始就没有空洞。上面的 auto_vacuum 检查已经挡掉了"根本没开"，
				// 走到这里说明是"开着的、但此刻没洞可放"。
				verdict = "empty"
				break
			}
			// 这一批花了多久，要**只量 reclaimBatch 本身**：把它和下面的
			// readStorageCounters、判据分开计时，否则量到的是"这一批 + 一次
			// 全表 pragma 查询 + 等锁"，而自适应要的是批量的代价。
			batchStart := time.Now()
			err := reclaimBatch(ctx, batch)
			took := time.Since(batchStart)
			if err != nil {
				verdict = "failed"
				rec.LastError = err.Error()
				slog.Error("storage: 增量回收失败", "error", err)
				break
			}
			// 换代在前、记账在后：下一批的大小由这一批的实测耗时决定。
			batchSize = nextBatchSize(batchSize, batch, took)
			rec.Calls += batch
			batches++

			now, err := readStorageCounters()
			if err != nil {
				verdict = "failed"
				rec.LastError = err.Error()
				break
			}
			prev := freelist
			freelist = now.freelist

			verdict = reclaimVerdict(prev, freelist, time.Since(roundStart))
			if verdict == "stalled" {
				rec.LastError = fmt.Sprintf("这批放完 freelist 反而没减少：%d → %d", prev, freelist)
				slog.Warn("storage: 增量回收原地踏步，收工", "freelist", freelist)
			}
			// 批数先到也算这一轮到点（持续模式恒等于一批，见 reclaimBatchesPerRound）。
			if verdict == "" && maxBatches > 0 && batches >= maxBatches {
				verdict = "budget"
			}
			if verdict != "" {
				break
			}
		}
		rec.Rounds++

		// 只有"到点"是**可能**接着跑的：empty / stalled / failed / stopped
		// 都是这一趟真正的终点。
		if verdict != "budget" || !opts.Continuous {
			rec.StopReason = verdict
			if verdict == "failed" {
				rec.Status = "failed"
			} else {
				rec.Status = "done"
			}
			break
		}

		// 轮间松手。**必须先放掉维护锁再歇**：持续回收可能跑十几分钟，握着锁
		// 歇气会让迁移的每一批都堵在这儿（它用的是阻塞 Lock，见 applyCompressBatch）。
		maintenanceMu.Unlock()
		locked = false
		keepGoing := waitReclaimPause(ctx)
		if !keepGoing {
			rec.Status, rec.StopReason = "done", "stopped"
			break
		}
		// 歇完重新抢。抢不到说明别的维护任务插进来了——那是它该得的（迁移比
		// 回收急：它改写数据形态，回收只是把文件缩小），这一趟就此收工，
		// 理由记成 busy 而不是偷偷继续等。
		if !maintenanceMu.TryLock() {
			rec.Status, rec.StopReason = "done", "busy"
			break
		}
		locked = true

		next, err := readStorageCounters()
		if err != nil {
			rec.Status, rec.StopReason = "failed", "failed"
			rec.LastError = err.Error()
			break
		}
		freelist = next.freelist
		if freelist == 0 {
			rec.Status, rec.StopReason = "done", "empty"
			break
		}
	}

	rec.BatchSize = batchSize
	finishReclaim(&rec, before)
	rec.DurationMs = time.Since(start).Milliseconds()
	saveLogReclaimState(rec)
	slog.Info("storage: 增量回收收工",
		"原因", rec.StopReason, "持续", rec.Continuous, "轮数", rec.Rounds,
		"放掉页", rec.FreedPages, "批量收敛到", rec.BatchSize,
		"文件", rec.FileSizeBefore, "→", rec.FileSizeAfter, "耗时", rec.DurationMs)
	return &rec, nil
}

// reclaimBatchesPerRound 是一轮里最多打几批。
//
//	0 → 不按批数封顶，只按时间预算（单轮模式，一轮最多 reclaimBudget）
//	1 → 持续模式：**一批一轮**
//
// 持续模式为什么要切得这么碎，而不是"一轮 90 秒、轮间歇 1 秒"？因为只有切到
// 一批，才能给写请求一条硬保证（推导见 reclaimContinuousPause）：一把写锁最多
// 被握一批的时间，而一批的实测最坏值是 4.4 秒 < busy_timeout 的 5 秒。
// 90 秒一轮里事务首尾相接、段间不松手，写请求那 5 秒耐心必然耗尽——那是
// **"写请求失败"**，不是"写请求变慢"。持续回收的全部价值就在这条保证上。
func reclaimBatchesPerRound(continuous bool) int {
	if continuous {
		return 1
	}
	return 0
}

// waitReclaimPause 在轮与轮之间松手 reclaimContinuousPause 那么久。
// 返回 false 表示被要求停（或 ctx 结束），不该再开下一轮。
//
// 分片睡而不是一次睡够：一次 time.Sleep 的话「停止」要等到睡完才生效，
// 而这段时间里维护锁是**放着的**——白等，且用户看着按钮没反应。
func waitReclaimPause(ctx context.Context) bool {
	deadline := time.Now().Add(reclaimContinuousPause)
	for {
		if reclaimStopRequested.Load() {
			return false
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return true
		}
		step := 50 * time.Millisecond
		if remaining < step {
			step = remaining
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(step):
		}
	}
}

// reclaimNextBatch 决定这一批打多少条语句：要么打满，要么只打剩下的。
//
// 单独拎出来是因为"剩下的不足一批"这条分支在小库上一闪而过（测试库几十页，
// 走一次就空了），而它错了的后果是**每轮都白打几百条空转语句**——空转也在写事务里。
//
// size 是这一批的**额定**大小，由 nextBatchSize 按实测耗时给（见那里）。
// 返回值小于 size 是"没打满"，这件事本身有意义——nextBatchSize 靠它判断
// 这一批的耗时能不能拿来外推。
func reclaimNextBatch(freelist int64, size int) int {
	if freelist <= 0 {
		return 0
	}
	if freelist < int64(size) {
		return int(freelist)
	}
	return size
}

// nextBatchSize 按"上一批实际花了多久"定这一批打多少条。
//
// 这是那个线上故障的直接修法（起因见 reclaimBatchMax）：批量必须**跟着库走**，
// 因为同样 512 条在两份形态相近的库上能差 8 倍，而批量是唯一能动的旋钮
// ——`incremental_vacuum(N)` 的参数被生产驱动忽略，一条恰好放一页。
//
// 做法是按耗时反比外推：想让耗时落到 target，大小就该乘 target/took。
// 两条约束，都是有原因的：
//
//   - **阻尼不对称：缩得快、涨得慢。** 缩是止损——这一批已经握锁握久了，
//     下一批必须立刻变小，所以一次允许减半。涨是**试探**，而试探过了头的代价
//     （一批握满写锁、打死写请求）比试探不足的代价（多收几批）大得多，
//     所以一次只涨 1.5 倍。慢库上 64 → 96 → 144 就稳住了，中途摸到的最高点
//     远在 busy_timeout 以内；对称的"一次一倍"会先冲到 256（慢库上约 4.5 秒），
//     贴着 5 秒的边。
//   - **没打满的批次不参与调整**（batch < size）。freelist 快空时这一批短，
//     是因为活少而不是因为这批"效率高"；拿它外推会把批量越推越大，
//     而下一趟的开头就握着一个大得离谱的批。took <= 0 一并挡掉（时钟精度所限，
//     小批量上真会量到 0）。
func nextBatchSize(size, batch int, took time.Duration) int {
	if batch < size || took <= 0 {
		return size
	}
	scaled := int(float64(batch) * float64(reclaimBatchTarget) / float64(took))
	scaled = min(max(scaled, size/2), size*3/2)
	return min(max(scaled, reclaimBatchMin), reclaimBatchMax)
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
// 一次只放一页（见 reclaimBatchMax），也完全可能哪天变成一页都不放。真到那一步，
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
// **必须在一个事务里**，理由就是 reclaimBatchMax 那张表：逐条执行时每一条都是
// 独立事务（建 journal / 落盘 / 删 journal），那份固定开销与"搬了几页"无关，
// 折下来约 9–10 ms/页、100 页/s——5 GiB 的洞要跑四个钟头。合起来是实测的
// 616–4,152 页/s（见 reclaimBatchMax），也就是**几十倍**的差别。
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

// ── 定时回收 ──────────────────────────────────────────────────────────────

// DefaultLogReclaimPolicy 默认**全关**。
//
// 回收全程持写锁——那是一段写请求会被排队的真实时间，最坏一批 4.4 秒。
// "这台机器什么时候可以占用库"是运维判断而不是技术判断，默认替用户做主，
// 就等于把一个会被感知到的动作变成默认行为。
func DefaultLogReclaimPolicy() *models.LogReclaimPolicy {
	return &models.LogReclaimPolicy{
		Enabled:          false,
		MinBytes:         defaultReclaimMinBytes,
		CheckIntervalSec: defaultReclaimCheckSec,
	}
}

func GetLogReclaimPolicy(ctx context.Context) (*models.LogReclaimPolicy, error) {
	policy := DefaultLogReclaimPolicy()
	config, err := gorm.G[models.Config](models.DB).
		Where("key = ?", models.KeyLogReclaimPolicy).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return policy, nil
		}
		return nil, err
	}
	if config.Value == "" {
		return policy, nil
	}
	if err := json.Unmarshal([]byte(config.Value), policy); err != nil {
		return nil, fmt.Errorf("unmarshal log reclaim policy: %w", err)
	}
	clampLogReclaimPolicy(policy)
	return policy, nil
}

// clampLogReclaimPolicy 把越界的参数拉回合法区间。
//
// 门槛下界是 0（"不管多小都收"是个合法选择，只是默认不这么给）；上界 1 TiB 只是
// 防呆，任何比库还大的门槛都等价于"永远不收"，那是用户自己选的。
func clampLogReclaimPolicy(p *models.LogReclaimPolicy) {
	if p.MinBytes < 0 {
		p.MinBytes = 0
	}
	if p.MinBytes > reclaimMaxMinBytes {
		p.MinBytes = reclaimMaxMinBytes
	}
	if p.CheckIntervalSec < reclaimMinCheckSec {
		p.CheckIntervalSec = reclaimMinCheckSec
	}
	if p.CheckIntervalSec > reclaimMaxCheckSec {
		p.CheckIntervalSec = reclaimMaxCheckSec
	}
}

func SaveLogReclaimPolicy(ctx context.Context, policy *models.LogReclaimPolicy) error {
	clampLogReclaimPolicy(policy)
	raw, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	if err := models.SaveConfigValue(ctx, models.KeyLogReclaimPolicy, string(raw)); err != nil {
		return err
	}
	// 存下去了就叫醒调度器。**这一步不是优化，是正确性**：调度器睡的是
	// "刚才读到的那个周期"，而它可能是 24 小时。没有这一下，用户把开关
	// 打开之后，最坏要等一整天它才第一次看一眼——那时候他早就判定这功能坏了。
	wakeReclaimScheduler()
	return nil
}

// reclaimWake 是"回收策略刚被改过"的一次性提醒。
//
// 容量 1 且发送端不阻塞：连改两次只算一次提醒（调度器醒来会重新读策略，
// 读到的一定是最后那份），而写策略这条路上不该出现任何等待。
var reclaimWake = make(chan struct{}, 1)

func wakeReclaimScheduler() {
	select {
	case reclaimWake <- struct{}{}:
	default:
	}
}

// waitReclaimTick 睡到下一个检查点，被"策略改了"或 ctx 结束时提前返回。
//
// 返回值是"要不要继续跑"：ctx 结束就该退出调度器。
//
// 被叫醒之后**立刻往下走**（而不是重算一次等待）：用户按下开关，期望的是
// "现在就开始管"，把他排进下一个周期等于没叫醒。
func waitReclaimTick(ctx context.Context, wait time.Duration) bool {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	case <-reclaimWake:
	}
	return true
}

// reclaimDue 是"这一眼要不要动手"里**不用抢锁**的那部分判断，抽成纯函数好逐条验。
// 返回空串表示该跑，否则返回不跑的理由（进 debug 日志，也是排障时唯一的线索：
// 一个"开着却从来不动"的定时回收，没有这句话就只能靠猜）。
//
//   - `disabled`       —— 开关关着
//   - `no_auto_vacuum` —— `PRAGMA incremental_vacuum` 在这个库上是 0.000 秒空操作
//     （实测），跑了只会每周期记一条"已收工、放掉 0 页"
//   - `below_threshold` —— 门槛的全部意义就是拦住这些"跑了也不值"的周期
func reclaimDue(policy *models.LogReclaimPolicy, autoVacuum int64, freelistBytes int64) string {
	switch {
	case policy == nil || !policy.Enabled:
		return "disabled"
	case autoVacuum != models.AutoVacuumIncremental:
		return "no_auto_vacuum"
	case freelistBytes < policy.MinBytes:
		return "below_threshold"
	}
	return ""
}

// StartLogReclaimScheduler 定期看一眼要不要自动回收。
//
// 与迁移调度器（30 秒）不同，它的周期由策略决定、默认 1 小时：迁移是"点一下
// 就开始跑"的交互式任务，被 kill 之后要能很快接上；而回收是**删除之后的收尾**，
// 早十分钟晚十分钟没有任何区别，看一次却要读一次 freelist。
//
// 每一轮都**重新读策略、重新定时**，不用一个固定的 ticker：周期是可配的，
// 固定 ticker 会让"改成 5 分钟"要等到下一个整点才生效。
//
// 光"重新读"还不够，**存策略时要叫醒它**（见 SaveLogReclaimPolicy）：重新读只
// 让新周期从下一轮起算，而这一轮睡的还是旧周期。真机上是这么发现的——策略从
// 默认的 1 小时改成 60 秒、开关打开，等了两分钟一动不动：调度器在进程启动时
// 就把"1 小时"算好了。
//
// 它起的回收**总是持续模式**：定时回收的意义就是"没人守着也把洞收干净"，
// 一次只收 90 秒的话，一个 5 GiB 的洞要十个周期——那还不如不自动。
func StartLogReclaimScheduler(ctx context.Context) {
	go func() {
		for {
			policy, err := GetLogReclaimPolicy(ctx)
			if err != nil {
				slog.Error("storage: 读回收策略失败", "error", err)
				// 读不出来就按默认周期歇一轮。定时回收不是关键路径，
				// 不该因为一次读失败就退出——下一轮还能好。
				policy = DefaultLogReclaimPolicy()
			}
			if !waitReclaimTick(ctx, time.Duration(policy.CheckIntervalSec)*time.Second) {
				return
			}

			// 在飞的回收（手动或上一轮定时）优先，别去抢。
			if reclaimInFlight.Load() {
				continue
			}
			counters, err := readStorageCounters()
			if err != nil {
				slog.Warn("storage: 定时回收量库失败，跳过这一眼", "error", err)
				continue
			}
			freelistBytes := counters.freelist * counters.pageSize
			// 先看那三条**不需要抢锁**的理由，再单独问一句"维护锁空着吗"。
			// 顺序是有意的：一个关着的定时回收不该每个周期都去抢一次锁——哪怕
			// 抢到就放，那也是一次白拿白放，而这条路径上还有别的东西在等它。
			if reason := reclaimDue(policy, counters.autoVacuum, freelistBytes); reason != "" {
				slog.Debug("storage: 这一眼不回收", "原因", reason, "可回收字节", freelistBytes)
				continue
			}
			// 只问一句"空着吗"，问完立刻放掉：真正干活的那条路
			// （StartReclaim → reclaimFreePages）会自己再抢一次，而且它抢的是
			// **整趟**要用的那把——在这里替它拿着毫无意义。
			if !maintenanceMu.TryLock() {
				slog.Debug("storage: 这一眼不回收", "原因", "busy", "可回收字节", freelistBytes)
				continue
			}
			maintenanceMu.Unlock()
			slog.Info("storage: 定时回收起跑", "可回收字节", freelistBytes, "门槛", policy.MinBytes)
			if err := StartReclaim(ctx, ReclaimOptions{Continuous: true, Source: "scheduled"}); err != nil {
				// 抢不到不是错：刚刚另一个入口起来了。下一轮再说。
				if !errors.Is(err, ErrReclaimRunning) {
					slog.Error("storage: 定时回收没能起跑", "error", err)
				}
			}
		}
	}()
}
