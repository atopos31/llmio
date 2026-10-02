package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/atopos31/llmio/models"
	"gorm.io/gorm"
)

// 本文件是历史行的**形态迁移**：把库里已经躺着的明文行，分批改写成压缩形态
// （块表引用帧 / 逐行帧），以及反向的 `decompress`（L2 降级）。
//
// 它和日志清理是一类东西——后台的、动全表的、可中断的维护任务——所以照抄了
// `log_cleanup.go` 的骨架（策略读 Config、调度器带 ticker、进度可查）。
//
// 三条贯穿全文的规矩：
//
//  1. **绕开模型，直接操作列。** 用模型读，`AfterFind` 会先把帧解成明文，
//     于是"这一行迁没迁过"就看不出来了；用模型写，`BeforeCreate` 又会在回滚
//     路径上把明文再压回去。所以候选行一律裸 SQL 取，`UPDATE` 一律裸 SQL 发。
//     编解码本身委托给 `models.PackBodies` / `models.UnpackBody`——
//     与生产钩子同一份代码，不做第二份实现。
//  2. **水位与数据同一个事务。** 中断的两种结果都合法：整批连水位一起回滚
//     （重跑同一批），或者整批连水位一起生效（下一批）。**不存在"数据改了、
//     水位没动"这种会让同一批被压第二遍的中间态。**
//  3. **不碰 `updated_at`。** 迁移改的是存储形态，不是内容。而 `updated_at`
//     正是冷静期过滤器的输入——迁移自己把它刷新了，就等于把这行重新标记成
//     "刚被写过"，下一轮又会绕开它。

const (
	defaultCompressBatchRows  = 64
	defaultCompressBatchBytes = 32 << 20
	defaultCompressQuiesceSec = 60
	compressCheckInterval     = 30 * time.Second
	compressMaxAttempts       = 10
)

// 迁移状态机的状态。字符串直接进 Config，所以取值要稳定。
const (
	compressIdle    = "idle"
	compressRunning = "running"
	compressPaused  = "paused"
	compressDone    = "done"
	compressFailed  = "failed"
)

// ErrMaintenanceBusy 表示另一个维护任务（清理或迁移）正持有维护锁。
//
// 它是**显式错误**而不是"静默跳过"：手动点一下清理却什么都没发生，
// 比报一句"正忙"糟糕得多。调度器那条路会自己吞掉这个错误，
// 因为下一 tick 会再来。
var ErrMaintenanceBusy = errors.New("maintenance: 另一个维护任务正在跑")

// ErrCompressRunning 表示已经有一轮迁移在跑。
var ErrCompressRunning = errors.New("log compress: 已经有一轮在跑")

// maintenanceMu 让所有"改数据形态/删数据"的维护任务互斥。
//
// 迁移**每批**取一次锁、放一次锁，而不是整个跑程霸占：一批几十毫秒，
// 中间放手的空档足够日志清理插进来，不必让"迁移要跑十分钟"变成
// "这十分钟别的维护一概别想动"。
var maintenanceMu sync.Mutex

// 一轮迁移只能有一个。用 CAS 而不是复用 maintenanceMu：锁是每批一取的，
// 挡不住"两个 run 交替取锁"。
var compressInFlight atomic.Bool

// compressPaused 在批与批之间生效。放内存里而不是每批去读 Config：
// 暂停是操作员的即时动作，不该等到下一批落库才被看见。
var compressPauseRequested atomic.Bool

// ── 策略 ──────────────────────────────────────────────────────────────────

func DefaultLogCompressPolicy() *models.LogCompressPolicy {
	return &models.LogCompressPolicy{
		Enabled:    false,
		BatchRows:  defaultCompressBatchRows,
		BatchBytes: defaultCompressBatchBytes,
		QuiesceSec: defaultCompressQuiesceSec,
	}
}

func GetLogCompressPolicy(ctx context.Context) (*models.LogCompressPolicy, error) {
	policy := DefaultLogCompressPolicy()
	config, err := gorm.G[models.Config](models.DB).
		Where("key = ?", models.KeyLogCompressPolicy).First(ctx)
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
		return nil, fmt.Errorf("unmarshal log compress policy: %w", err)
	}
	clampLogCompressPolicy(policy)
	return policy, nil
}

// clampLogCompressPolicy 把越界的参数拉回合法区间。
//
// 上限不是拍脑袋的：PutBatch 要求整批原文同时在内存里，而 `BatchBytes`
// 直接就是那个量级。给到 1 GiB 就意味着一次 GC 峰值能顶掉一个实例。
func clampLogCompressPolicy(p *models.LogCompressPolicy) {
	if p.BatchRows <= 0 {
		p.BatchRows = defaultCompressBatchRows
	}
	if p.BatchRows > 4096 {
		p.BatchRows = 4096
	}
	if p.BatchBytes <= 0 {
		p.BatchBytes = defaultCompressBatchBytes
	}
	if p.BatchBytes > 1<<30 {
		p.BatchBytes = 1 << 30
	}
	if p.QuiesceSec < 0 {
		p.QuiesceSec = 0
	}
	if p.QuiesceSec > 86400 {
		p.QuiesceSec = 86400
	}
}

func SaveLogCompressPolicy(ctx context.Context, policy *models.LogCompressPolicy) error {
	clampLogCompressPolicy(policy)
	raw, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	return models.SaveConfigValue(ctx, models.KeyLogCompressPolicy, string(raw))
}

// ── 状态 ──────────────────────────────────────────────────────────────────

// compressMode 把迁移与回滚的差异收敛成三个方法。
//
// 两条路的骨架完全一样（分批、水位、幂等、事务），只有"挑哪些行"和"改成什么"
// 不同。分成两份代码的话，回滚这条**只在事故里才走**的路径就永远不会跟着
// 迁移一起被验证——而它恰恰是唯一的 L2 降级实现。
type compressMode int

const (
	modePack   compressMode = iota // 明文 → 帧/块引用（迁移）
	modeUnpack                     // 帧/块引用 → 明文（L2 回滚）
)

func (m compressMode) String() string {
	if m == modeUnpack {
		return "decompress"
	}
	return "compress"
}

func (m compressMode) stateKey() string {
	if m == modeUnpack {
		return models.KeyLogDecompressState
	}
	return models.KeyLogCompressState
}

// candidateFilter 是"这一行还没做过这件事"的判据。
//
// 两条路都靠 `typeof`：帧按 BLOB 落进 TEXT 亲和列（SQLite 的亲和性只转换
// TEXT↔数字，不碰 BLOB），所以**形态与 typeof 一一对应**，不需要任何标志列
// （C1：7 GB 的表上加列 = AutoMigrate 重建全表）。
//
// 而且 `typeof` 只读记录的类型头、不读载荷——这一列平均 470 KiB，
// 真去读载荷的话光是筛选就能把迁移拖垮。
func (m compressMode) candidateFilter() string {
	if m == modeUnpack {
		return "typeof(input) = 'blob'"
	}
	return "typeof(input) = 'text'"
}

// DefaultLogCompressState 是"从没跑过"的状态。
func DefaultLogCompressState() *models.LogCompressState {
	return &models.LogCompressState{Status: compressIdle}
}

func GetLogCompressState(ctx context.Context) (*models.LogCompressState, error) {
	return getCompressState(ctx, modePack)
}

func getCompressState(ctx context.Context, mode compressMode) (*models.LogCompressState, error) {
	state := DefaultLogCompressState()
	config, err := gorm.G[models.Config](models.DB).
		Where("key = ?", mode.stateKey()).First(ctx)
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
		return nil, fmt.Errorf("unmarshal log %s state: %w", mode, err)
	}
	if state.Status == "" {
		state.Status = compressIdle
	}
	return state, nil
}

func saveCompressState(ctx context.Context, mode compressMode, state *models.LogCompressState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return models.SaveConfigValue(ctx, mode.stateKey(), string(raw))
}

// saveCompressStateTx 把状态写进**迁移批次自己的事务**。
//
// 这是"水位与数据同生共死"的落点：写在这个事务里，中断点就只有两种结果
// ——整批连水位一起回滚，或者整批连水位一起生效。把水位放到事务外写，
// 就会出现"数据已经改完、水位没动"，重启后同一批被再做一遍（幂等能兜住，
// 但白扫一遍全批候选）。
//
// 要用 NewDB 开一个干净 statement：调用方那个 tx 的 statement 已经被
// 前面几十条 UPDATE 钉住了，直接复用会生成错的 SQL。
func saveCompressStateTx(tx *gorm.DB, mode compressMode, state *models.LogCompressState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	db := tx.Session(&gorm.Session{NewDB: true})
	var row models.Config
	switch err := db.Where("key = ?", mode.stateKey()).First(&row).Error; {
	case err == nil:
		row.Value = string(raw)
		return db.Save(&row).Error
	case errors.Is(err, gorm.ErrRecordNotFound):
		return db.Create(&models.Config{Key: mode.stateKey(), Value: string(raw)}).Error
	default:
		return err
	}
}

// ── 跑一轮 ────────────────────────────────────────────────────────────────

// RunLogCompress 把历史明文行迁成压缩形态，一直跑到没有候选行为止。
//
// 阻塞调用：一批几十到几百毫秒，整库（12,483 行 / 5.64 GiB）量级是分钟。
// 调用方（HTTP handler）应当把它放到 goroutine 里，用状态接口看进度。
func RunLogCompress(ctx context.Context, full bool) (*models.LogCompressState, error) {
	return runCompressMode(ctx, modePack, full)
}

// RunLogDecompress 是 L2 降级：把库里的帧全部还原成明文。
//
// 它不是"以后再说"的功能。除了是唯一的降级实现，它还是**最强的验证器**——
// "压缩整库 → 原样还原 → 与原库逐字节比对"这一趟走通，等于对整库做了一次
// 完整往返验证，比任何抽样都硬。
func RunLogDecompress(ctx context.Context, full bool) (*models.LogCompressState, error) {
	return runCompressMode(ctx, modeUnpack, full)
}

func runCompressMode(ctx context.Context, mode compressMode, full bool) (*models.LogCompressState, error) {
	if !compressInFlight.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("%w（%s）", ErrCompressRunning, mode)
	}
	defer compressInFlight.Store(false)
	// 能走到这里就说明这一轮是"被要求跑的"，暂停标志清掉。
	compressPauseRequested.Store(false)

	policy, err := GetLogCompressPolicy(ctx)
	if err != nil {
		return nil, err
	}
	state, err := getCompressState(ctx, mode)
	if err != nil {
		return nil, err
	}

	if full {
		// 「从 0 全量重扫」并不等于"把已经迁过的行再压一遍"——候选过滤是
		// `typeof(input)='text'`，迁过的行已经变成 BLOB 了，扫到也会跳过。
		// 它的真正用途是"水位不可信了"（手工改过库、从备份恢复），
		// 以及**回滚路径**：解压必须从 0 开始，因为帧散布在全表。
		state.LastID = 0
		state.Scanned = 0
		state.Packed = 0
		state.Skipped = 0
		state.BytesBefore = 0
		state.BytesAfter = 0
		state.Attempts = 0
		state.LastError = ""
	}
	state.FinishedAt = ""

	// 快照要在水位复位**之后**做：TotalRows 的定义是"本轮要扫的候选行总数"
	// （已扫 + 剩余），复位后它才是全量，续跑时它是"这轮开始时还剩多少"。
	snap, err := snapshotCompress(ctx, mode, state.LastID)
	if err != nil {
		return nil, err
	}
	state.MaxID = snap.maxID
	state.TotalRows = state.Scanned + snap.total
	if state.StartedAt == "" || full {
		state.StartedAt = time.Now().Format(time.RFC3339)
	}
	state.Status = compressRunning
	if err := saveCompressState(ctx, mode, state); err != nil {
		return nil, err
	}

	slog.Info("log compress run started",
		"mode", mode.String(), "full", full,
		"last_id", state.LastID, "max_id", state.MaxID, "total_rows", state.TotalRows)

	for {
		if compressPauseRequested.Load() {
			state.Status = compressPaused
			if err := saveCompressState(ctx, mode, state); err != nil {
				return nil, err
			}
			slog.Info("log compress paused", "mode", mode.String(), "last_id", state.LastID)
			return state, nil
		}

		rows, err := fetchCompressCandidates(ctx, mode, policy, state.LastID, state.MaxID)
		if err != nil {
			return state, err
		}
		if len(rows) == 0 {
			state.Status = compressDone
			state.Attempts = 0
			state.LastError = ""
			state.FinishedAt = time.Now().Format(time.RFC3339)
			if err := saveCompressState(ctx, mode, state); err != nil {
				return state, err
			}
			slog.Info("log compress done",
				"mode", mode.String(), "scanned", state.Scanned, "packed", state.Packed,
				"bytes_before", state.BytesBefore, "bytes_after", state.BytesAfter)
			return state, nil
		}

		if err := applyCompressBatch(ctx, mode, rows, state); err != nil {
			state.Attempts++
			state.LastError = err.Error()
			// 「指数退避」落在**调度器的 tick 上**，不是进程内 sleep：一批失败
			// 通常不是瞬时的（磁盘满、库被别的东西锁着），进程内立刻重试同一条
			// SQL 只会立刻再失败一次，还把一个 HTTP 请求/一个 goroutine 挂住。
			// 下一 tick 重跑同一批，重试间隔天然是 tick 周期；连续 10 次就停下
			// 等人处理，免得无限重试把日志刷满。
			if state.Attempts >= compressMaxAttempts {
				state.Status = compressFailed
			} else {
				state.Status = compressRunning
			}
			if saveErr := saveCompressState(ctx, mode, state); saveErr != nil {
				slog.Error("save compress failure state failed", "error", saveErr)
			}
			slog.Error("log compress batch failed",
				"mode", mode.String(), "attempts", state.Attempts,
				"last_id", state.LastID, "error", err)
			return state, err
		}
	}
}

// compressSnapshot 是跑一轮之前量到的数。
type compressSnapshot struct {
	maxID uint  // 本轮起点时的最大 id
	total int64 // 水位之后、maxID 之前的候选行数
}

// snapshotCompress 量出本轮要扫的范围。
//
// `total` 刻意只数**水位之后**的那一段：跑完之后（稳态）水位≈maxID，
// 这个 count 扫到 0 行就返回，调度器每 30 秒空跑一次的代价是常数级。
// 要是按整表数，一个状态页/一次空跑就得把全表的记录头读一遍。
//
// 代价是首次开跑时它确实要把 `id <= maxID` 这一段全扫一遍（只读记录头，
// 不读载荷）。一次性，且只发生在真要点「开始迁移」的时候。
func snapshotCompress(ctx context.Context, mode compressMode, lastID uint) (compressSnapshot, error) {
	var snap compressSnapshot
	if err := models.DB.WithContext(ctx).
		Raw(`SELECT COALESCE(MAX(id), 0) FROM chat_ios`).
		Row().Scan(&snap.maxID); err != nil {
		return snap, fmt.Errorf("read max id: %w", err)
	}

	countQ := fmt.Sprintf(`SELECT count(*) FROM chat_ios
		WHERE deleted_at IS NULL AND id > ? AND id <= ? AND %s`, mode.candidateFilter())
	if err := models.DB.WithContext(ctx).Raw(countQ, lastID, snap.maxID).
		Row().Scan(&snap.total); err != nil {
		return snap, fmt.Errorf("count candidates: %w", err)
	}
	return snap, nil
}

// compressRow 是一条候选行，body 是**列里此刻真实存着的字节**（不是明文）。
type compressRow struct {
	id   uint
	body []byte
}

// fetchCompressCandidates 取一批候选行。
//
// 冷静期（`updated_at <= now - quiesce`）挡的是"正在被写的那一行"：
// `service/chat.go` 记日志是三条独立语句，Create 之后那一行的请求体已经在库里、
// 响应体还没补上——它就是半成品。`updated_at` 恰好是这个状态的照妖镜，
// 因为补响应体的那句 Updates 会把它刷新。
//
// 按字节再切一刀：只按行数封顶会让"一批"的大小随 body 走。这份语料平均
// 470 KiB/行，光 64 行的上限就是 30 MiB 原文同时在内存里。
// **但至少留一行**——一行就超过 BatchBytes 时，切到 0 行会让水位原地不动，
// 于是这一批永远推不动，跑成一个死循环。
func fetchCompressCandidates(
	ctx context.Context, mode compressMode, policy *models.LogCompressPolicy, lastID, maxID uint,
) ([]compressRow, error) {
	cutoff := time.Now().Add(-time.Duration(policy.QuiesceSec) * time.Second)
	q := fmt.Sprintf(`SELECT id, input FROM chat_ios
		WHERE deleted_at IS NULL AND id > ? AND id <= ? AND %s AND updated_at <= ?
		ORDER BY id LIMIT ?`, mode.candidateFilter())

	rows, err := models.DB.WithContext(ctx).Raw(q, lastID, maxID, cutoff, policy.BatchRows).Rows()
	if err != nil {
		return nil, fmt.Errorf("fetch candidates: %w", err)
	}
	defer rows.Close()

	var (
		out   []compressRow
		total int64
	)
	for rows.Next() {
		var r compressRow
		if err := rows.Scan(&r.id, &r.body); err != nil {
			return nil, fmt.Errorf("scan candidate: %w", err)
		}
		if len(out) > 0 && total+int64(len(r.body)) > policy.BatchBytes {
			break
		}
		total += int64(len(r.body))
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate candidates: %w", err)
	}
	return out, nil
}

// applyCompressBatch 是一个批次：写块/改列/推水位，全在一个事务里。
//
// `state` 只在事务提交成功之后才被写回——这一批的计数必须与这一批的数据同生共死，
// 否则失败重跑会把同一批的字节数记两遍，进度条与"省了多少"当场失真。
func applyCompressBatch(
	ctx context.Context, mode compressMode, rows []compressRow, state *models.LogCompressState,
) error {
	// 锁必须在**事务之外、之前**取。
	//
	// 反过来（先开事务再取锁）会和日志清理组成经典死锁：清理是"先取维护锁、
	// 再开事务"，两条路的加锁顺序一致才不会互相等。这里若先开事务，迁移就变成
	// "持 DB 写锁等维护锁"，而清理是"持维护锁等 DB 写锁"——两边都不放。
	maintenanceMu.Lock()
	defer maintenanceMu.Unlock()

	next := *state // 值拷贝：提交成功才生效
	err := models.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		newBody, err := rewriteBodies(ctx, mode, tx, rows)
		if err != nil {
			return err
		}
		for i, r := range rows {
			next.LastID = r.id
			next.Scanned++
			next.BytesBefore += int64(len(r.body))
			if newBody[i] == nil {
				// 形态不用改（压不动就存明文 / 已经是明文）。也要计进 BytesAfter，
				// 这样 BytesBefore−BytesAfter 恰好是这一列被省掉的字节数。
				next.Skipped++
				next.BytesAfter += int64(len(r.body))
				continue
			}
			if err := tx.Exec(`UPDATE chat_ios SET input = ? WHERE id = ?`,
				models.BodyBytes(newBody[i]), r.id).Error; err != nil {
				return fmt.Errorf("rewrite chat_io %d: %w", r.id, err)
			}
			next.Packed++
			next.BytesAfter += int64(len(newBody[i]))
		}
		next.Status = compressRunning
		next.Attempts = 0
		next.LastError = ""
		return saveCompressStateTx(tx, mode, &next)
	})
	if err != nil {
		return err
	}
	*state = next
	return nil
}

// rewriteBodies 算出每一行**该落库的字节**，nil 表示"这一行不用动"。
//
// 传 `models.BodyBytes` 而不是裸 `[]byte`：它是"让内容决定存储类"的那个类型，
// 明文落 TEXT、帧落 BLOB。回滚这条路尤其不能写错——把明文按 BLOB 写回去，
// 列里的格式就错了（`typeof` 不再对应形态），而**读路径仍然读得出来**，
// 于是这个错会一直躺着，直到下一次迁移把它漏掉。
func rewriteBodies(
	ctx context.Context, mode compressMode, tx *gorm.DB, rows []compressRow,
) ([][]byte, error) {
	plains := make([][]byte, len(rows))
	for i, r := range rows {
		plains[i] = r.body
	}
	if mode == modePack {
		return models.PackBodies(ctx, tx, plains)
	}

	out := make([][]byte, len(rows))
	for i, r := range rows {
		plain, err := models.UnpackBody(ctx, tx, r.body)
		if err != nil {
			return nil, fmt.Errorf("unpack chat_io %d: %w", r.id, err)
		}
		// nil 是空值；和原字节一样说明这一行的 BLOB 根本不是帧（不该发生，
		// 候选过滤只挑 BLOB）。两种都跳过——**拿不准就不动它**，
		// 这条路径上的每一条"我猜它应该是……"都是一次静默损坏的机会。
		if plain == nil || bytes.Equal(plain, r.body) {
			continue
		}
		out[i] = plain
	}
	return out, nil
}

// ── 对外读数 ──────────────────────────────────────────────────────────────

// CompressRunning 报"此刻有没有一轮迁移在跑"。
//
// 报的是**进程内的事实**（那一个 CAS 标志），不是库里的 Status：库里写着
// running 而进程里没人在跑，恰恰是最常见的情形——上次被 kill 了。
// 那件事由状态机自己处理（重启后调度器接着推），界面要分的这两件事不能混。
func CompressRunning() bool { return compressInFlight.Load() }

// GetLogDecompressState 查回滚这一条独立水位的进度。
func GetLogDecompressState(ctx context.Context) (*models.LogCompressState, error) {
	return getCompressState(ctx, modeUnpack)
}

// RecordBackup 把"开跑前探到的备份"记进 Config，作为运维存证。
//
// 记的是**探测结论**而不是操作员的口头保证：出事之后翻这一条，要能看出
// 当时到底有没有备份。它失败**不阻断迁移**——存证不是迁移的前提条件，
// 为它挡住一次本该跑完的迁移是本末倒置（调用方只记日志）。
func RecordBackup(ctx context.Context, rec models.LogCompressBackup) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return models.SaveConfigValue(ctx, models.KeyLogCompressBackup, string(raw))
}

// ── 暂停 ──────────────────────────────────────────────────────────────────

// PauseLogCompress 请求暂停。批间生效。
//
// 没有任何一轮在跑时，标志置了也没人消费，所以这里顺手把状态落盘——
// 否则界面上会一直显示 running，而其实什么都没在跑。
func PauseLogCompress(ctx context.Context) (*models.LogCompressState, error) {
	compressPauseRequested.Store(true)
	state, err := getCompressState(ctx, modePack)
	if err != nil {
		return nil, err
	}
	if !compressInFlight.Load() && state.Status == compressRunning {
		state.Status = compressPaused
		if err := saveCompressState(ctx, modePack, state); err != nil {
			return nil, err
		}
	}
	return state, nil
}

// ── 备份门槛 ──────────────────────────────────────────────────────────────

// VerifyBackup 检查"迁移前有没有一份可用的备份"。
//
// 迁移是不可逆的形态改写（要还原得走 decompress，而 decompress 本身也要能跑）。
// 出问题时最省事的路是把备份盖回去，所以进状态页的必须是**自己量出来的事实**
// ——大小与 mtime——而不是操作员口头保证的。
//
// 它只报告，不拦（拦的逻辑在 handler 上，见 §1.5 的 `--allow-in-place`）。
func VerifyBackup(path string, dbSize int64) models.LogCompressBackup {
	rec := models.LogCompressBackup{
		Path:   path,
		At:     time.Now().Format(time.RFC3339),
		Source: "manual",
	}
	info, err := os.Stat(path)
	if err != nil {
		rec.Source = "missing"
		return rec
	}
	rec.Size = info.Size()
	rec.MTime = info.ModTime().Format(time.RFC3339)
	if rec.Size < dbSize {
		rec.Source = "stale"
	}
	return rec
}

// ── 调度 ──────────────────────────────────────────────────────────────────

// StartLogCompressScheduler 每 30 秒看一眼要不要推进迁移。
//
// 30 秒而不是清理那样的 1 小时：迁移是"点一下就开始跑"的交互式任务，
// 被 kill 掉之后重启要能很快接上；而稳态下这一眼只是两条按主键范围的
// count/select（水位≈maxID，扫到 0 行就返回），几乎不花钱。
//
// **它不自己判断"该不该开始"**：`Enabled` 是总开关，`paused`/`failed`
// 两个状态要人处理之后才继续（`failed` 重试 10 次都失败，再自动重试
// 只会把日志刷满）。
func StartLogCompressScheduler(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(compressCheckInterval)
		defer ticker.Stop()

		run := func() {
			policy, err := GetLogCompressPolicy(ctx)
			if err != nil {
				slog.Error("load log compress policy failed", "error", err)
				return
			}
			if !policy.Enabled {
				return
			}
			state, err := getCompressState(ctx, modePack)
			if err != nil {
				slog.Error("load log compress state failed", "error", err)
				return
			}
			switch state.Status {
			case compressPaused, compressFailed:
				return
			}
			if _, err := runCompressMode(ctx, modePack, false); err != nil {
				// 已经在跑（另一个入口起的）不算错——这一 tick 让给它。
				if errors.Is(err, ErrCompressRunning) {
					return
				}
				slog.Error("scheduled log compress failed", "error", err)
			}
		}

		run()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}
