package handler

import (
	"context"
	"log/slog"

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
//     所以这里另起 goroutine 并显式用 `context.Background()`——迁移的半途而废
//     必须是"进程被 kill"，不能是"用户关了个标签页"。
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
	// Reclaim 是上一次空间回收（启动期转换/VACUUM，或手动点的那一下）的结果。
	Reclaim *models.LogReclaimState `json:"reclaim"`
	Running bool                    `json:"running"`
	// Reclaiming 是"此刻有一轮回收在跑"。与 Running 分开：它们是两个任务。
	Reclaiming bool `json:"reclaiming"`
}

// GetCompressionStatus 返回压缩状态。
func GetCompressionStatus(c *gin.Context) {
	ctx := c.Request.Context()

	policy, err := service.GetLogCompressPolicy(ctx)
	if err != nil {
		common.InternalServerError(c, "Failed to load compression policy: "+err.Error())
		return
	}
	state, err := service.GetLogCompressState(ctx)
	if err != nil {
		common.InternalServerError(c, "Failed to load compression state: "+err.Error())
		return
	}
	decompressState, err := service.GetLogDecompressState(ctx)
	if err != nil {
		common.InternalServerError(c, "Failed to load decompress state: "+err.Error())
		return
	}
	// 量不到就是 null，不报错（见 service/db_stats.go 的开头）。
	db := service.ReadDBStats(ctx)
	// 备份判定用的库大小另走文件系统：那是个 os.Stat，不会撞锁，
	// 也不该被上面那组数的 5 秒冷却期拖旧。
	size, _ := service.DBFileSize()
	reclaim, err := service.GetLogReclaimState(ctx)
	if err != nil {
		common.InternalServerError(c, "Failed to load reclaim state: "+err.Error())
		return
	}

	common.Success(c, compressStatus{
		Policy:          policy,
		State:           state,
		DecompressState: decompressState,
		Backup:          service.VerifyBackup(backupPath(), size),
		DB:              db,
		Reclaim:         reclaim,
		Running:         service.CompressRunning(),
		Reclaiming:      service.ReclaimRunning(),
	})
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

	full := req.Full
	// 异步：迁移是分钟级的，挂在请求上会被网关掐断；更要紧的是请求的 ctx
	// 一取消迁移就半途而废。这里的 ctx 必须自己起一个。
	go func() {
		state, err := service.RunLogCompress(context.Background(), full)
		if err != nil {
			slog.Error("log compress run failed", "error", err)
			return
		}
		slog.Info("log compress run finished",
			"status", state.Status, "scanned", state.Scanned, "packed", state.Packed)
	}()

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

	go func() {
		state, err := service.RunLogDecompress(context.Background(), true)
		if err != nil {
			slog.Error("log decompress run failed", "error", err)
			return
		}
		slog.Info("log decompress run finished",
			"status", state.Status, "packed", state.Packed)
	}()

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
	if err := service.StartReclaim(c.Request.Context()); err != nil {
		common.BadRequest(c, "已有一轮回收在执行，请等它结束")
		return
	}
	common.Success(c, map[string]any{"started": true})
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
