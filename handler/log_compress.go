package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
)

// 数据库压缩的运维接口：状态、策略、开跑、暂停、回滚。
//
// 这一组接口的形状与 `/api/logs/cleanup` 一致（读状态、改策略、触发一次），
// 但有两点不同，都是数据安全逼出来的：
//
//  1. **开跑与回滚都是异步的。** 整库迁移是分钟级的事，挂在 HTTP 请求上会被
//     网关掐断、被浏览器超时，而**请求的 ctx 一取消，迁移就跑到一半**。
//     所以后台由 service.StartLogCompress / StartLogDecompress 起（它们脱掉
//     取消、只留值）——迁移的半途而废必须是"进程被 kill"，不能是"用户关了个
//     标签页"。**占位也在那里、在返回之前做完**，见那两个函数的注释。
//  2. **原地迁移要先确认有备份。** 迁移是形态改写，出事时最省事的路是把备份
//     盖回去；没有备份就得靠 decompress，而那正是可能一起坏掉的那条路。

// compressStatus 是状态页要的全部东西：策略、进度、以及库的现状。
//
// 进度（State）与现状（DB）必须一起给：只看进度会以为"迁完了就完事了"，
// 而文件其实一字节都没缩（auto_vacuum=0，迁移只把页还进 freelist）。
type compressStatus struct {
	Policy          *models.LogCompressPolicy `json:"policy"`
	State           *models.LogCompressState  `json:"state"`
	DecompressState *models.LogCompressState  `json:"decompress_state"`
	Backup          models.LogCompressBackup  `json:"backup"`
	// DB 可能是 null：**量不到库的现状不该让整张卡报错**，进度（state）
	// 是另一条读法，照样给。见 service/db_stats.go。
	DB *service.DBStats `json:"db"`
	// Reclaim 是上一次空间回收（启动期转换/VACUUM，手动点的，或定时起的）的结果。
	Reclaim *models.LogReclaimState `json:"reclaim"`
	// ReclaimPolicy 是空间回收的自动推进策略。它与迁移策略（Policy）是两份，
	// 因为两者的触发时机完全不是一回事：迁移完一次就不再跑了，回收要跟着
	// 删除量一直跑。
	ReclaimPolicy *models.LogReclaimPolicy `json:"reclaim_policy"`
	Running       bool                     `json:"running"`
	// Reclaiming 是"此刻有一轮回收在跑"。与 Running 分开：它们是两个任务。
	Reclaiming bool `json:"reclaiming"`
	// ReclaimingKind 是"此刻在跑的**是**回收还是重整"（reclaim / vacuum，没在跑就是空）。
	//
	// 不能拿 Reclaim.Kind 顶替：那是一份**回执**，写的是上一次跑完的那趟任务
	// （running 记录要等收工才落库）。正在跑的时候它是张冠李戴的旧值。
	// 而这两条路在界面上是两套说法：重整不能中途停，耗时也差一个数量级。
	ReclaimingKind string `json:"reclaiming_kind"`
	// ReclaimStopping 是"按了停止、但那批还没跑完"的中间态。
	// 不把它单独给出来的话，界面只能显示"还在回收中"，用户会以为按钮没生效。
	ReclaimStopping bool `json:"reclaim_stopping"`
}

// GetCompressionStatus 返回压缩状态。
//
// 它在两种"库被自己占住"的情况下**不读库**，退回上一次读到的那一份，
// 内存里那几个字段照旧现算。真机实测的理由见 lastGoodStatus 的注释。
func GetCompressionStatus(c *gin.Context) {
	ctx := c.Request.Context()

	// 重整全程持 EXCLUSIVE，这几块一个字都读不出来。**先手判掉，不要等**：
	// 状态页每 2 秒轮询一次，而每一次读都要在 busy_timeout 上等满 5 秒
	// （db_stats.go 那条读更是 3 次尝试 ≈ 16 秒），请求会叠成一片。
	if service.ReclaimingKind() == models.ReclaimKindVacuum {
		if cached, ok := cachedCompressionStatus(); ok {
			common.Success(c, cached)
			return
		}
	}

	st, err := buildCompressionStatus(ctx)
	if err != nil {
		// 其它撞锁（迁移的批、回收的批、日志清理）：这几个任务持锁都短于
		// busy_timeout，读多半能等到，等不到的那几次退回上一次的数。
		if service.IsBusyErr(err) {
			if cached, ok := cachedCompressionStatus(); ok {
				common.Success(c, cached)
				return
			}
		}
		common.InternalServerError(c, err.Error())
		return
	}
	rememberCompressionStatus(st)
	common.Success(c, st)
}

// lastGoodStatus 是最近一次**完整读到**的状态。
//
// 这一条是**真机量出来的**：整库重整（VACUUM）在 7 GiB 的库上要跑 57 秒，而这
// 57 秒里它持 EXCLUSIVE，`configs` 一句都读不出来。状态页每 2 秒轮询一次，
// 于是每一次都等满 `busy_timeout` 然后 500：
//
//	GET /api/logs/compression -> 500 "Failed to load compression policy: database is locked (5) (SQLITE_BUSY)"
//
// 用户点完「立即重整」，卡片立刻变成一片报错——连"正在重整"都看不到，更看不到
// 那个"不能中途停止"的说明。而这条路上**答案根本不缺**：
//
//   - "此刻在干什么"（running / reclaiming / reclaiming_kind）全在内存里，
//     是 service 那几个原子量，不需要读库；
//   - "策略与回执"在这几十秒里**一个字都不会变**：它们只在任务收工、或用户在
//     控制台保存时才写（写路径都在这几个函数里，写完顺手更新缓存）。
//
// 所以退路是拿上一次那几块顶一顶，而"它没变"这个前提由写路径保证。
// 与 db_stats.go 的 `Stale` 是同一个思路，只是粒度更粗：那边是"这一组数旧了"，
// 这边是"整张卡旧了"。旧到什么时候？顶多一趟重整那么久——它一收工，
// 下一次轮询就真读了。
var (
	statusMu       sync.Mutex
	lastGoodStatus *compressStatus
	// lastGoodStatusPath 记的是这份快照说的是**哪个库**。快照里那些东西
	// （策略、回执、库现状）都是某一个库文件的属性，库换了就一句都用不上——
	// 而它们不会以"读不出来"的形式暴露，只会让界面拿着另一个库的数说话。
	// 与 db_stats.go 那条缓存同一个理由。
	lastGoodStatusPath string
)

// buildCompressionStatus 把状态页要的东西读齐。任何一块读不到就整块失败——
// 一份"缺了几块"的状态比一次明确的错误更容易让人做错判断，而调用方有退路。
func buildCompressionStatus(ctx context.Context) (compressStatus, error) {
	policy, err := service.GetLogCompressPolicy(ctx)
	if err != nil {
		return compressStatus{}, fmt.Errorf("Failed to load compression policy: %w", err)
	}
	state, err := service.GetLogCompressState(ctx)
	if err != nil {
		return compressStatus{}, fmt.Errorf("Failed to load compression state: %w", err)
	}
	decompressState, err := service.GetLogDecompressState(ctx)
	if err != nil {
		return compressStatus{}, fmt.Errorf("Failed to load decompress state: %w", err)
	}
	reclaim, err := service.GetLogReclaimState(ctx)
	if err != nil {
		return compressStatus{}, fmt.Errorf("Failed to load reclaim state: %w", err)
	}
	reclaimPolicy, err := service.GetLogReclaimPolicy(ctx)
	if err != nil {
		return compressStatus{}, fmt.Errorf("Failed to load reclaim policy: %w", err)
	}
	// 量不到就是 null，不报错（见 service/db_stats.go 的开头）。
	db := service.ReadDBStats(ctx)
	// 备份判定用的库大小另走文件系统：那是个 os.Stat，不会撞锁，
	// 也不该被上面那组数的 5 秒冷却期拖旧。
	size, _ := service.DBFileSize()

	return compressStatus{
		Policy:          policy,
		State:           state,
		DecompressState: decompressState,
		Backup:          service.VerifyBackup(backupPath(), size),
		DB:              db,
		Reclaim:         reclaim,
		ReclaimPolicy:   reclaimPolicy,
		Running:         service.CompressRunning(),
		Reclaiming:      service.ReclaimRunning(),
		ReclaimingKind:  service.ReclaimingKind(),
		ReclaimStopping: service.ReclaimStopping(),
	}, nil
}

// rememberCompressionStatus 存一份快照。
//
// 那几个指针字段要**逐个深拷**：缓存里的那一份会被反复发出去，而调用方（以及
// 后面的 `Stale` 标记）会往上写。共享一个底层结构体的话，一次写就会改到
// "历史快照"，症状是回执在某次退路应答里莫名其妙地变了。
func rememberCompressionStatus(st compressStatus) {
	snapshot := st
	if st.Policy != nil {
		v := *st.Policy
		snapshot.Policy = &v
	}
	if st.State != nil {
		v := *st.State
		snapshot.State = &v
	}
	if st.DecompressState != nil {
		v := *st.DecompressState
		snapshot.DecompressState = &v
	}
	if st.Reclaim != nil {
		v := *st.Reclaim
		snapshot.Reclaim = &v
	}
	if st.ReclaimPolicy != nil {
		v := *st.ReclaimPolicy
		snapshot.ReclaimPolicy = &v
	}
	if st.DB != nil {
		v := *st.DB
		snapshot.DB = &v
	}
	statusMu.Lock()
	lastGoodStatus = &snapshot
	lastGoodStatusPath = models.DBPath
	statusMu.Unlock()
}

// cachedCompressionStatus 拿上一次那一份，并把"此刻"那几个字段换成现算的。
//
// 换掉它们不是锦上添花：整条退路的**全部价值**就在这四个字段上——用户要看的
// 正是"它在重整中、而且停不了"。库里那几块旧一会儿没关系（它们本来就没变），
// 但"此刻在跑什么"必须是这一秒的。
func cachedCompressionStatus() (compressStatus, bool) {
	statusMu.Lock()
	st := lastGoodStatus
	path := lastGoodStatusPath
	statusMu.Unlock()
	if st == nil || path != models.DBPath {
		return compressStatus{}, false
	}
	out := *st
	// 深拷：下面要往 DB 上写 Stale，不能改到快照本体。
	if st.DB != nil {
		db := *st.DB
		db.Stale = true
		out.DB = &db
	}
	out.Running = service.CompressRunning()
	out.Reclaiming = service.ReclaimRunning()
	out.ReclaimingKind = service.ReclaimingKind()
	out.ReclaimStopping = service.ReclaimStopping()
	return out, true
}

// UpdateReclaimPolicy 改空间回收的自动推进策略。越界参数会被夹回（不报错）。
func UpdateReclaimPolicy(c *gin.Context) {
	var req models.LogReclaimPolicy
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := service.SaveLogReclaimPolicy(c.Request.Context(), &req); err != nil {
		common.InternalServerError(c, "Failed to save reclaim policy: "+err.Error())
		return
	}
	policy, err := service.GetLogReclaimPolicy(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to reload reclaim policy: "+err.Error())
		return
	}
	common.Success(c, policy)
}

// UpdateCompressionPolicy 改策略。参数越界会被夹回合法区间（不报错）。
func UpdateCompressionPolicy(c *gin.Context) {
	var req models.LogCompressPolicy
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := service.SaveLogCompressPolicy(c.Request.Context(), &req); err != nil {
		common.InternalServerError(c, "Failed to save compression policy: "+err.Error())
		return
	}
	policy, err := service.GetLogCompressPolicy(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to reload compression policy: "+err.Error())
		return
	}
	common.Success(c, policy)
}

type compressionRunRequest struct {
	// Full 从水位 0 重扫全表。已经迁过的行会被候选过滤（typeof）跳过，
	// 所以它不是"再压一遍"，而是"水位不可信了，重扫一遍确认"。
	Full bool `json:"full"`
	// AcknowledgeNoBackup 是原地迁移的确认。没有它、又没探到备份时，
	// 这个接口**拒绝开跑**。
	AcknowledgeNoBackup bool `json:"acknowledge_no_backup"`
}

// RunCompression 触发一轮迁移。异步返回，进度看状态接口。
func RunCompression(c *gin.Context) {
	var req compressionRunRequest
	// 允许空 body：不带参数就是"按默认跑一轮"。
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			common.BadRequest(c, "Invalid request: "+err.Error())
			return
		}
	}
	ctx := c.Request.Context()

	if service.CompressRunning() {
		common.BadRequest(c, "已有一轮迁移在执行，请等它结束或先暂停")
		return
	}

	// 备份门槛（§1.5）。看似啰嗦，但"迁移不可逆"这件事值得多问一句：
	// 真出事时，从备份盖回去是唯一不需要相信压缩代码那条路。
	//
	// 这里只要一个文件大小，所以**只量文件大小**。原先它走的是整条
	// `readCompressDBStats`（含一次全表聚合），于是迁移正在写库的时候点「开始」
	// 会撞 SQLITE_BUSY → 500 → 迁移根本起不来。真机反馈就是这个形状。
	//
	// 那么为什么这一句 os.Stat 失败仍然报 500？因为它与上面那次的失败**不是
	// 一类事**：那一次是"库正忙，读不到"（可重试、可降级、不该拦人），这一次是
	// "库文件不见了或读不到"——进程正拿着它，这只可能是权限或文件被移走，
	// 是**真实故障**。而且没有它就算不出备份够不够新，那道门也就形同虚设。
	size, err := service.DBFileSize()
	if err != nil {
		common.InternalServerError(c, "Failed to read database size: "+err.Error())
		return
	}
	backup := service.VerifyBackup(backupPath(), size)
	if backup.Source != "manual" {
		if !req.AcknowledgeNoBackup {
			common.BadRequest(c, "没有探测到可用备份（"+backup.Path+"，"+backup.Source+"）——"+
				"迁移会原地改写历史行的存储形态。请先备份，或把 acknowledge_no_backup 设为 true")
			return
		}
		// 存证要记的是**当时的事实**：这一趟是在没有可用备份的情况下跑的。
		// 记成 "manual" 会让后来翻记录的人以为有备份可退。
		backup.Source = "forced"
	}
	if err := service.RecordBackup(ctx, backup); err != nil {
		// 存证失败不阻断：它不是迁移的前提条件，下一轮还能补记。
		slog.Error("record compression backup failed", "error", err)
	}

	// 「已完成」之后再点一次 = 整表重扫，不用调用方自己记得带 full。
	// 判据在 service 里（它知道状态机），理由见 ShouldRescanFromZero。
	full := req.Full || service.ShouldRescanFromZero(ctx)
	// 异步：迁移是分钟级的，挂在请求上会被网关掐断。
	//
	// **占位由 StartLogCompress 在返回之前做完**，所以这里的 `started: true`
	// 是可信的：响应写完的那一刻 CompressRunning() 已经是 true，状态接口不会
	// 出现"响应说在跑、状态说没在跑"的窗口。上面那道预检只是快速路径——
	// 它是"先看一眼"，真正裁决的是这次占位（两者之间隔着备份门那几步）。
	if err := service.StartLogCompress(c.Request.Context(), full); err != nil {
		common.BadRequest(c, "已有一轮迁移在执行，请等它结束或先暂停")
		return
	}

	common.Success(c, map[string]any{"started": true, "full": full, "backup": backup})
}

// PauseCompression 请求暂停。批间生效，所以返回时可能还有一批在跑完的路上。
func PauseCompression(c *gin.Context) {
	state, err := service.PauseLogCompress(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to pause compression: "+err.Error())
		return
	}
	common.Success(c, state)
}

type decompressRequest struct {
	// Full 必须为 true 才允许跑：帧散布在全表，从水位续跑会漏掉前一半。
	Full bool `json:"full"`
	// Confirm 要等于字面量 "decompress"。
	//
	// 这个接口把整个库**变大**（1.9 GiB → 5.6 GiB），而且不 VACUUM 的话
	// 文件一字节都不会缩。它是一次真实的、可观测的运维动作，不该被误触。
	Confirm string `json:"confirm"`
}

// RollbackCompression 是 L2 降级：把库里的帧全部还原成明文。
func RollbackCompression(c *gin.Context) {
	var req decompressRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if req.Confirm != "decompress" {
		common.BadRequest(c, `需要确认：请把 confirm 设为 "decompress"`)
		return
	}
	if !req.Full {
		common.BadRequest(c, "回滚必须 full=true：帧散布在全表，从水位续跑会漏掉一半")
		return
	}
	if service.CompressRunning() {
		common.BadRequest(c, "已有一轮迁移在执行，请先暂停并等它退出")
		return
	}

	// 与「开始迁移」同一条规矩：占位在返回之前完成，`started: true` 才可信。
	if err := service.StartLogDecompress(c.Request.Context(), true); err != nil {
		common.BadRequest(c, "已有一轮迁移在执行，请先暂停并等它退出")
		return
	}

	common.Success(c, map[string]any{"started": true})
}

// ReclaimStorage 触发一次增量回收，异步返回。
//
// 异步的理由与迁移一样（分钟级的事不该挂在请求上），但它比迁移更需要说清
// 代价：回收**全程持写锁**，虽然读不受影响，写请求会排队、超时即失败。
// 所以这个接口不是"随手点一下"，前端要把它标成维护动作。
//
// 占位（"有一轮在跑"）由 service.StartReclaim **同步**做完，所以这里的
// `started: true` 是可信的：返回之后再来一下必然被上面那道门挡掉。
func ReclaimStorage(c *gin.Context) {
	var req reclaimRequest
	// 允许空 body：不带参数就是"跑一轮"（与这个端点原来的行为一致）。
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			common.BadRequest(c, "Invalid request: "+err.Error())
			return
		}
	}
	opts := service.ReclaimOptions{Continuous: req.Continuous, Source: "manual"}
	if err := service.StartReclaim(c.Request.Context(), opts); err != nil {
		common.BadRequest(c, "已有一轮回收在执行，请等它结束")
		return
	}
	common.Success(c, map[string]any{"started": true, "continuous": req.Continuous})
}

// VacuumStorage 触发一次重整（VACUUM），异步返回。
//
// 它比回收**更该说清代价**，因为代价的形状不一样：回收是"写请求排队、多半能
// 成功"，而 VACUUM 是"这几十秒里写请求直接失败"——它全程持 EXCLUSIVE，连读都
// 进不来。换来的是快一个数量级：同一份 7 GiB 的库，实测 VACUUM 55–59 秒，
// 增量回收要十轮 14.7 分钟。
//
// 所以它只该在大空洞之后点（迁移、回滚、批量删除），前端要把它标成维护动作，
// 并说清"约多久 + 期间写入会失败"。与「开始回收」是**两个按钮**：那一个不需要
// 额外磁盘、也不必停服务，这一个两样都要，但一次就还干净。
//
// 磁盘预检在 service.StartVacuum 里**同步**做完，不够就直接拒——那不是"跑到
// 一半才发现"的失败，而是动手之前就知道的事，而中途满盘会把库留在说不清的
// 状态。前端可以从状态接口的 db.disk_free_bytes 提前算出来，别让用户白点。
func VacuumStorage(c *gin.Context) {
	plan, err := service.StartVacuum(c.Request.Context())
	if err != nil {
		switch {
		case errors.Is(err, service.ErrVacuumNoSpace):
			common.BadRequest(c, fmt.Sprintf(
				"空闲磁盘不够这一次重整：需要 %s（2 倍库大小 + 余量），当前可用 %s",
				humanBytes(plan.Need), humanBytes(plan.Free)))
		case errors.Is(err, service.ErrReclaimRunning):
			common.BadRequest(c, "已有维护任务在执行，请等它结束")
		default:
			common.InternalServerError(c, "重整没能开始: "+err.Error())
		}
		return
	}
	common.Success(c, map[string]any{"started": true, "plan": plan})
}

// humanBytes 只为错误文案服务。用户看到的"需要 6.1 GB"比"需要 6553600000 字节"
// 有用得多——尤其在他要判断"是不是该先去清点别的东西"的时候。
//
// 用 1024 进制（GiB 的数值），但写 "GB"：这是运维界面的惯例，写 GiB 反而要
// 多解释一句。保留一位小数，四位有效数字对"够不够"这个判断足够。
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	v := float64(n)
	i := -1
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

// StopReclaim 请求停止持续回收。批间生效，所以返回成功不等于已经停了。
//
// 与「暂停迁移」不同，它没有"停下之后库处于什么形态"的问题：回收不动数据，
// 停在任何一刻库都是完整的，只是文件没缩到位。
//
// **重整（VACUUM）停不了**：它是一条语句，没有"批间"这个时机，标志设了没人读。
// 这里特意与 StopReclaim 的返回 false 分开报，因为"没有回收在跑"是**误报**——
// 用户明明看着它在跑。见 service.StopReclaim。
func StopReclaim(c *gin.Context) {
	if !service.StopReclaim() {
		if service.ReclaimingKind() == models.ReclaimKindVacuum {
			common.BadRequest(c, "重整不能中途停止：它是一条 VACUUM 语句，没有批间这个时机")
			return
		}
		common.BadRequest(c, "当前没有回收在跑")
		return
	}
	common.Success(c, map[string]any{"stopping": true})
}

type reclaimRequest struct {
	// Continuous 为真时跑到放完（每批松手一次，见 service.reclaimContinuousPause），
	// 而不是跑满一轮就把控制权交回来让用户再点。
	Continuous bool `json:"continuous"`
}

// GetDecompressStatus 单独查回滚进度（回滚与迁移是两条独立的水位）。
func GetDecompressStatus(c *gin.Context) {
	state, err := service.GetLogDecompressState(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to load decompress state: "+err.Error())
		return
	}
	common.Success(c, state)
}

// ── 内部 ──────────────────────────────────────────────────────────────────

// backupPath 是"约定的备份位置"：库文件旁边加个 .bak。
//
// 不做可配置：路径一旦可配，运维就得同时记住"备份在哪"和"配置在哪"，
// 而这两件事都会在出事的时候想不起来。约定一个位置，状态页直接显示探到了什么。
func backupPath() string {
	if models.DBPath == "" {
		return ""
	}
	return models.DBPath + ".bak"
}

// 量库现状的代码搬去了 service/db_stats.go。搬家的理由不是分层洁癖：
// 它现在带缓存与重试，而"缓存多久"是个业务判断；更要紧的是「开始迁移」
// 那条路只该量一个文件大小，不该被一组全表聚合拖住（见上面的 RunCompression）。
