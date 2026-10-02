package handler

import (
	"context"
	"log/slog"
	"os"

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
	DB              *compressDBStats          `json:"db"`
	Running         bool                      `json:"running"`
}

type compressDBStats struct {
	Path        string `json:"path"`
	FileSize    int64  `json:"file_size"`
	PageSize    int64  `json:"page_size"`
	PageCount   int64  `json:"page_count"`
	Freelist    int64  `json:"freelist_count"`
	AutoVacuum  int64  `json:"auto_vacuum"`
	Rows        int64  `json:"rows"`
	PendingRows int64  `json:"pending_rows"`
	FramedRows  int64  `json:"framed_rows"`

	BlockRows   int64 `json:"block_rows"`
	GroupRows   int64 `json:"block_group_rows"`
	GroupBytes  int64 `json:"block_group_bytes"`
	ColumnBytes int64 `json:"input_column_bytes"`
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
	db, err := readCompressDBStats(ctx)
	if err != nil {
		common.InternalServerError(c, "Failed to read database stats: "+err.Error())
		return
	}

	common.Success(c, compressStatus{
		Policy:          policy,
		State:           state,
		DecompressState: decompressState,
		Backup:          service.VerifyBackup(backupPath(), db.FileSize),
		DB:              db,
		Running:         service.CompressRunning(),
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
	db, err := readCompressDBStats(ctx)
	if err != nil {
		common.InternalServerError(c, "Failed to read database stats: "+err.Error())
		return
	}
	backup := service.VerifyBackup(backupPath(), db.FileSize)
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

// readCompressDBStats 量一次库的现状。
//
// `pending_rows` 是全表扫 typeof——它只读记录的类型头、不读载荷（这一列平均
// 470 KiB），所以便宜；但它确实是全表级的，状态页别做成每秒轮询。
func readCompressDBStats(ctx context.Context) (*compressDBStats, error) {
	stats := &compressDBStats{Path: models.DBPath}

	if models.DBPath != "" {
		if info, err := os.Stat(models.DBPath); err == nil {
			stats.FileSize = info.Size()
		}
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
