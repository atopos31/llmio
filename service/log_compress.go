package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
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
		Enabled:         false,
		BatchRows:       defaultCompressBatchRows,
		BatchBytes:      defaultCompressBatchBytes,
		QuiesceSec:      defaultCompressQuiesceSec,
		BatchIntervalMs: 0, // 不停顿：手动点「开始迁移」就是要点完它
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
	// 上限 5 秒：一份 12,483 行 / 5.64 GiB 的库在批 64 行下是 195 批，
	// 停 5 秒就是 16 分钟量级、占用率掉到 4% 以下。真想更闲的应当**暂停**
	// 而不是把它调成龟速——暂停是可续的、状态是看得见的，而一个停了半小时
	// 还在 running 的迁移只会让人以为它卡死了。
	if p.BatchIntervalMs < 0 {
		p.BatchIntervalMs = 0
	}
	if p.BatchIntervalMs > 5000 {
		p.BatchIntervalMs = 5000
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
// 而且 `typeof` 只读记录的类型头、不读载荷——`input` 平均 470 KiB，
// 真去读载荷的话光是筛选就能把迁移拖垮。
//
// **判据是三列的并集，不是 `input` 一列。** 三列各有各的接入点（`input` 走
// 钩子与块表，响应体两列走序列化器），但"迁没迁过"这件事三列是同一件。
// 只看 `input` 会漏掉整整一类行：一份 `input` 早就迁完的库，响应体那两列
// 还一个字都没压过（它们没有块表那样的"必须整列重写"的理由），而按 `input`
// 筛出来的候选集是**空的**——迁移会报"已完成"，那 1.4 GiB 明文一行都不会动。
func (m compressMode) candidateFilter() string {
	if m == modeUnpack {
		return framedFilter
	}
	return plaintextFilter
}

// chatIOColumns 是参与压缩的三列，顺序即 SELECT / UPDATE 里的顺序。
//
// 用数组而不是三处各写一遍列名：这块代码刚补的一次缺口正是"只做了 input 一列"
// ——响应体那两列在库里躺到 1.4 GiB 明文都没人动，而每一处（SELECT、UPDATE、
// 候选判据、字节统计）都各自写了一遍 `input`，漏一列不会产生任何编译错误。
// 数组把"三列"收成一个可以遍历的东西，新加的每一处都只能跟着走完三遍。
//
// 列名要与下面六个判据常量里的字面量对齐——判据得是 const（要拼进 SQL），
// 生成不了，这处重复是剩下的唯一一处。
var chatIOColumns = [colCount]string{"input", "of_string", "of_string_array"}

const (
	colInput = iota
	colOfString
	colOfArray
	colCount
)

// 三列各自的形态判据。
//
// **两条路都只读 `typeof`，一个字节的载荷都不碰。** 这不是"顺手优化"，是这套
// 东西能不能在真机上活下来的前提：状态页拿着同一套判据做全表聚合，而 SQLite 的
// `typeof` 读的是记录头。实测这份 7.6 GiB 的库，纯 `typeof` 的全表并集 0.076 秒；
// 一旦掺进任何要碰载荷的东西——`length(CAST(列 AS BLOB))`、`列 <> 空串`——
// 就变成 2.9～4.2 秒，成正比于要读的字节数。
//
// 而 4.2 秒是会要命的：库跑在 `journal_mode=delete`（回滚日志）下，**读事务挡写**，
// 状态页每 5 秒刷一次量库现状的同时，聊天请求的写等满 5 秒 `busy_timeout` 报
// `SQLITE_BUSY`——整站 500。这条路径曾经真的长这样。
//
// 判据能瘦成 `typeof`，靠的是那条不变量：**这三列里 TEXT 只表示"非空明文"**
// （空值一律 NULL，见 models/serializer.go 与 models/body.go）。
// 没有它就必须量长度才能把"空"从"明文"里摘出去——而那个 `length()` 正是
// 上面那 4.2 秒的全部来源。历史行里那个形态（`of_string` 是空串的 8,932 行）
// 由迁移自己归一化掉：它读得到载荷，顺手把空的 TEXT 写成 NULL。
//
// **三列一律同一口径**，`input` 也不例外。曾经给 `input` 单独留过
// `length(...) > 0`（怕 NUL 打头的请求体被判成空），现在连它一起不要了：
// 空串、NUL 打头、正常明文，在 `typeof` 眼里都是 `text`，而**那正是我们要的**
// ——它们都是"这一列还没迁"。
const (
	inputPlain    = "typeof(input) = 'text'"
	ofStringPlain = "typeof(of_string) = 'text'"
	ofArrayPlain  = "typeof(of_string_array) = 'text'"

	inputFramed    = "typeof(input) = 'blob'"
	ofStringFramed = "typeof(of_string) = 'blob'"
	ofArrayFramed  = "typeof(of_string_array) = 'blob'"
)

// 两条路的候选过滤器：三列里**任意一列**还没做完，这一行就还是候选。
//
// 拼接而不是各写一遍：判据与用它拼出来的筛子必须同步，分开写就多了一处会漂移的地方。
const (
	plaintextFilter = "(" + inputPlain + " OR " + ofStringPlain + " OR " + ofArrayPlain + ")"
	framedFilter    = "(" + inputFramed + " OR " + ofStringFramed + " OR " + ofArrayFramed + ")"
)

// plaintextBytesExpr 是一行**还没压的那些列**的原文字节合计，逐列判形态。
//
// 逐列判而不是"整行有明文就算整行"：迁移跑在混合形态的库上是常态
// （`input` 早迁完了、响应体还没），整行口径会把已经压过的 `input` 那几 MiB
// 引用帧当成原文算进合计，于是"省了多少"这一栏当场虚高。
//
// `COALESCE` 是必需的，不是保险：NULL 参与算术会让整个表达式变成 NULL，
// 而 `sum()` 跳过 NULL——一行 `of_string_array` 为空就会让这一行的其余两列
// 一起从合计里消失（3593 行是这个形态，不是边角）。
//
// **这里的 `length()` 是要读载荷的**（实测 1.36 GiB 读 0.80 秒，成正比），
// 与上面那两条判据不同。它只在 `snapshotCompress` 里跑——一次迁移一趟，
// 而且本来就是"把还没压的原文量一遍"这件事本身，没有更便宜的做法。
// 别把这段表达式搬进任何按秒轮询的地方：状态页那条路只许用 typeof。
const plaintextBytesExpr = "(CASE WHEN " + inputPlain +
	" THEN COALESCE(length(CAST(input AS BLOB)), 0) ELSE 0 END" +
	" + CASE WHEN " + ofStringPlain +
	" THEN COALESCE(length(CAST(of_string AS BLOB)), 0) ELSE 0 END" +
	" + CASE WHEN " + ofArrayPlain +
	" THEN COALESCE(length(CAST(of_string_array AS BLOB)), 0) ELSE 0 END)"

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
	// 读侧兜底：`bytes_total` 是后加的字段，**在它之前落盘的状态里没有它**，
	// 而界面拿它当压缩比的分子——不加这一句，一份早就迁完的库会永远显示不出
	// 压缩比，除非用户再点一次「开始迁移」（`runCompressMode` 里有同样的补写，
	// 但那只在真跑一轮时才发生）。
	//
	// 只补**跑完的**状态。半途的不能补：`bytes_before` 只盖住扫过的行，
	// 而分母是全表，补出来的比值会恰好是真值的一半——一个看起来很专业的错数。
	// 跑完的状态里两者盖的是同一批行（都是"当时还是明文的那些"），补得成立。
	if mode == modePack && state.Status == compressDone &&
		state.BytesTotal == 0 && state.BytesBefore > 0 {
		state.BytesTotal = state.BytesBefore
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

// resetModeState 把一条路的状态清成"从没跑过"（水位归零、统计清空）。
//
// 它服务的场景只有一个，但那个场景是真机上踩到的：**回滚跑完之后，迁移的水位
// 就不代表任何事了**。见 runCompressMode 里 done 分支的那段注释。
//
// 刻意不复位 `bytes_total` 之外的任何东西去"保留画面"：回滚完成之后，这条路上
// 的每一个计数（扫了多少、压了多少、省了多少字节）描述的都是**上一形态**的事，
// 留着只会让界面自相矛盾。
func resetModeState(ctx context.Context, mode compressMode) error {
	return saveCompressState(ctx, mode, DefaultLogCompressState())
}

// ShouldRescanFromZero 报"这次人工发起的迁移该不该从水位 0 重扫"。
//
// 规则只有一条——状态是 done 就全量重扫，不管调用方传了什么——但它决定了
// 这套东西在真机上**能不能跑起来**。
//
// 理由是水位与"三列并集"之间的错位。水位记的是"上一轮扫到了哪个 id"，而三列
// 各有各的进度：请求体列早就迁完、水位停在表尾的库，响应体那两列可能一行都
// 没压过（真机上 1.36 GiB 明文，而界面报"已完成"）。这时从水位续跑，候选查询
// 在水位之后一行都扫不到，一轮下来立刻报 done——那两列永远迁不动。而 done
// 恰恰说明上一轮已经跑到表尾，再点一次就是"整表再确认一遍"。
//
// 重扫不会把已经迁过的行压第二遍：候选过滤只挑明文，那些行不是候选。
//
// **只给人工入口用。** 调度器每 30 秒拿 full=false 推一次（done 状态也推），
// 这条规则要是落进 runCompressMode，就变成每 30 秒一次全表重扫。
func ShouldRescanFromZero(ctx context.Context) bool {
	state, err := getCompressState(ctx, modePack)
	if err != nil {
		// 读不到状态就不加码：全量重扫是更贵的那个选择，为一次读失败付它不值当。
		return false
	}
	return state.Status == compressDone
}

// ── 跑一轮 ────────────────────────────────────────────────────────────────

// RunLogCompress 把历史明文行迁成压缩形态，一直跑到没有候选行为止。
//
// 阻塞调用：一批几十到几百毫秒，整库（12,483 行 / 5.64 GiB）量级是分钟。
// HTTP 那条路不走这里，走 StartLogCompress——它连"起后台"这件事一起负责。
func RunLogCompress(ctx context.Context, full bool) (*models.LogCompressState, error) {
	if err := beginCompress(); err != nil {
		return nil, fmt.Errorf("%w（%s）", err, modePack)
	}
	defer compressInFlight.Store(false)
	return runCompressMode(ctx, modePack, full)
}

// RunLogDecompress 是 L2 降级：把库里的帧全部还原成明文。
//
// 它不是"以后再说"的功能。除了是唯一的降级实现，它还是**最强的验证器**——
// "压缩整库 → 原样还原 → 与原库逐字节比对"这一趟走通，等于对整库做了一次
// 完整往返验证，比任何抽样都硬。
func RunLogDecompress(ctx context.Context, full bool) (*models.LogCompressState, error) {
	if err := beginCompress(); err != nil {
		return nil, fmt.Errorf("%w（%s）", err, modeUnpack)
	}
	defer compressInFlight.Store(false)
	return runCompressMode(ctx, modeUnpack, full)
}

// StartLogCompress 占位之后就返回，迁移在后台跑；StartLogDecompress 同理。
//
// 与 StartReclaim 同一个形状，而这不是风格问题：**占位必须在返回之前完成**。
// 反过来（handler 自己 go func，占位在那条 goroutine 里）会留下一个窗口——
// 响应已经写下 `started: true`，`CompressRunning()` 还是 false：状态接口当场
// 自相矛盾，前端据此把"开始迁移"放回可点，并发进来的第二个请求也能透过去。
// 测试里它更难看：用例把 `models.DB` 还原成 nil 之后那条 goroutine 才醒来，
// 整个包以一个与用例无关的 nil 指针 panic 收场（2026-10-03 的 CI 就是这么红的）。
func StartLogCompress(ctx context.Context, full bool) error {
	return startCompressMode(ctx, modePack, full)
}

// StartLogDecompress 是回滚那条路的后台入口，占位同样在返回之前完成。
func StartLogDecompress(ctx context.Context, full bool) error {
	return startCompressMode(ctx, modeUnpack, full)
}

func startCompressMode(ctx context.Context, mode compressMode, full bool) error {
	if err := beginCompress(); err != nil {
		return fmt.Errorf("%w（%s）", err, mode)
	}
	go func() {
		defer compressInFlight.Store(false)
		// 请求的 ctx 在响应写完之后就被取消。半途而废必须是"进程被 kill"，
		// 不能是"用户关了个标签页"——所以脱掉取消，只留值。
		if _, err := runCompressMode(context.WithoutCancel(ctx), mode, full); err != nil {
			slog.Error("log compress run failed", "error", err, "mode", mode)
		}
	}()
	return nil
}

// beginCompress 占住"有一轮在跑"这个位，占位与释放都由入口负责。
//
// 释放刻意不放在这里：占位必须先于后台 goroutine 存在，谁占谁放，
// 才不至于把别人的占位顺手放掉（被拒的那一次绝不能清标志）。
func beginCompress() error {
	if !compressInFlight.CompareAndSwap(false, true) {
		return ErrCompressRunning
	}
	// 能占上位就说明这一轮是"被要求跑的"，暂停标志清掉。
	compressPauseRequested.Store(false)
	return nil
}

// runCompressMode 是迁移/回滚本身，既不起后台也不占位（占位由上面几个入口负责）。
func runCompressMode(ctx context.Context, mode compressMode, full bool) (*models.LogCompressState, error) {
	policy, err := GetLogCompressPolicy(ctx)
	if err != nil {
		return nil, err
	}
	state, err := getCompressState(ctx, mode)
	if err != nil {
		return nil, err
	}

	if full {
		// 「从 0 全量重扫」并不等于"把已经迁过的行再压一遍"——候选判据是
		// `typeof(列)='text'`（三列里任意一列还是明文），迁过的那一列已经
		// 变成 BLOB 了，扫到也会跳过。
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

	// 续跑时补一次原文合计：本次改动之前落的盘没有 bytes_total（那时候界面
	// 拿 bytes_before 当分子）。bytes_before 是迁移历轮扫过的**全部候选行**的
	// 原文合计，在"整库本来是明文、一路迁完"这个最常见的形态下与它相等；
	// 已经以压缩形态直接写进来的行不在其中，所以补出来的值只可能偏保守。
	// 不补的话，一份早就迁完的库会永远显示不出压缩比——而那正是要修的那个毛病。
	if state.LastID > 0 && state.BytesTotal == 0 && state.BytesBefore > 0 {
		state.BytesTotal = state.BytesBefore
	}

	// 快照要在水位复位**之后**做：TotalRows 的定义是"本轮要扫的候选行总数"
	// （已扫 + 剩余），复位后它才是全量，续跑时它是"这轮开始时还剩多少"。
	snap, err := snapshotCompress(ctx, mode, state.LastID)
	if err != nil {
		return nil, err
	}
	state.MaxID = snap.maxID
	state.TotalRows = state.Scanned + snap.total
	// 原文合计只在**从 0 起跑**的那一次量得准：那时"水位之后还是明文的行"
	// 就是全表。续跑时水位之后只剩一部分明文，已经压过的行的原文已经不在了
	// ——量不回来，所以保留上一个值，绝不用一个只覆盖半张表的数去覆盖它。
	//
	// 只在 pack 这一路取：unpack 的候选是帧，量出来的是帧的字节数不是原文，
	// 而回滚本来也不该报"压缩比"。
	if mode == modePack && state.LastID == 0 {
		state.BytesTotal = snap.plainBytes
	}
	if state.StartedAt == "" || full {
		state.StartedAt = time.Now().Format(time.RFC3339)
	}
	state.Status = compressRunning
	if err := saveCompressState(ctx, mode, state); err != nil {
		return nil, err
	}

	slog.Info("log compress run started",
		"mode", mode.String(), "full", full,
		"last_id", state.LastID, "max_id", state.MaxID, "total_rows", state.TotalRows,
		"bytes_total", state.BytesTotal)

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
			// 回滚把整个列还原成明文，**迁移的水位就此失效**：它还停在"上次压到
			// 哪"，而水位之前的那些行现在全是明文。不退的话，下一次「开始迁移」
			// 从那个水位续跑，**水位之前一行都扫不到**——它扫到 0〜几行候选就报
			// done，而库里明明还有一整库明文。真机上就是这个形状：回滚 12,469 行
			// 之后再迁移，只压了 1 行，状态却报 `packed=12469`（那个数是上一轮累计
			// 的，续跑不重置），界面上一边写"已完成"一边写"待迁移 12,483 行"。
			//
			// 只在**全量跑完**时退：unpack 允许从水位续跑，那种情况下水位之前的帧
			// 还没被还原，迁移的水位仍然有效。
			//
			// 反过来（迁移跑完退 unpack 的水位）不需要：回滚这条路在接口层就强制
			// full，它的水位每次都是 0 起跑。
			if mode == modeUnpack && full {
				if err := resetModeState(ctx, modePack); err != nil {
					return nil, err
				}
				slog.Info("log compress 水位随回滚复位", "mode", modePack.String())
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

		// 批间停顿：占用率旋钮，见 LogCompressPolicy.BatchIntervalMs。
		//
		// 放在这里（提交之后、取下一批之前）有两个后果，都是想要的：第一，
		// **第一批发车不等**——点了「开始迁移」立刻动，停顿只发生在批与批之间；
		// 第二，**停顿时不持任何锁**（锁是 applyCompressBatch 里每批一取的），
		// 这正是这个旋钮的意义所在。
		//
		// 代价是最后一批之后也会白停一次（再过一轮才发现没候选行、转 done）。
		// 用 0 默认值时它是零，而调大它的操作员已经明确选了"慢一点"。
		sleepBetweenBatches(ctx, time.Duration(policy.BatchIntervalMs)*time.Millisecond)
	}
}

// compressSleepSlice 是停顿的检查粒度：暂停按钮按下去最多隔这么久生效。
const compressSleepSlice = 50 * time.Millisecond

// sleepBetweenBatches 在批与批之间停 d，但**随时可以被叫醒**。
//
// 不能直接 `time.Sleep(d)`：暂停是操作员的即时动作，按下去却要瞪着进度条
// 等最多 d（上限 5 秒）才停，那个手感是"按钮坏了"。ctx 取消同理——进程要退，
// 没有任何理由再等。
//
// 切片轮询而不是 select 一个 timer + 一个暂停通道：暂停状态本来就是个
// atomic.Bool（`PauseLogCompress` 只置位、不阻塞），再加一条通道就是给同一件
// 事造第二份真相。50 ms 的粒度对上"人的反应时间"绰绰有余。
func sleepBetweenBatches(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	deadline := time.Now().Add(d)
	for {
		// 先查暂停再查时间：暂停标志可能是在上一次切片里置上的，
		// 那时我们正好在 time.After 里面，现在补上这一眼。
		if compressPauseRequested.Load() {
			return
		}
		remain := time.Until(deadline)
		if remain <= 0 {
			return
		}
		if remain > compressSleepSlice {
			remain = compressSleepSlice
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(remain):
		}
	}
}

// compressSnapshot 是跑一轮之前量到的数。
type compressSnapshot struct {
	maxID uint  // 本轮起点时的最大 id
	total int64 // 水位之后、maxID 之前的候选行数
	// plainBytes 是水位之后、maxID 之前那些**还裸着原文的列**的字节合计
	// （三列，逐列判形态，见 plaintextBytesExpr）。
	//
	// 它和 total 一起量、同一个 WHERE，所以不多一次扫描：`length()` 读的是
	// **载荷**（实测成正比于字节数），但这条路一次迁移只走一趟，而这本来就是
	// "把还没压的原文量一遍"本身。别把它挪进轮询路径——那条只许用 typeof。
	plainBytes int64
}

// snapshotCompress 量出本轮要扫的范围。
//
// `total` 刻意只数**水位之后**的那一段：跑完之后（稳态）水位≈maxID，
// 这个 count 扫到 0 行就返回，调度器每 30 秒空跑一次的代价是常数级。
// 要是按整表数，一个状态页/一次空跑就得把全表的记录头读一遍。
//
// 代价是首次开跑时它确实要把 `id <= maxID` 这一段全扫一遍，而这一句里的
// `length()` 要读载荷（`total` 那半个是免费的 typeof，`plainBytes` 那半个不是）。
// 一次性，且只发生在真要点「开始迁移」的时候。
func snapshotCompress(ctx context.Context, mode compressMode, lastID uint) (compressSnapshot, error) {
	var snap compressSnapshot
	if err := models.DB.WithContext(ctx).
		Raw(`SELECT COALESCE(MAX(id), 0) FROM chat_ios`).
		Row().Scan(&snap.maxID); err != nil {
		return snap, fmt.Errorf("read max id: %w", err)
	}

	// 两个筛子刻意分开：count 跟着模式走（unpack 数的是帧），原文合计固定按
	// "哪几列还是明文"逐列累加（见 plaintextBytesExpr）。
	countQ := fmt.Sprintf(`SELECT count(*), COALESCE(sum(%s), 0)
		FROM chat_ios
		WHERE deleted_at IS NULL AND id > ? AND id <= ? AND %s`,
		plaintextBytesExpr, mode.candidateFilter())
	if err := models.DB.WithContext(ctx).Raw(countQ, lastID, snap.maxID).
		Row().Scan(&snap.total, &snap.plainBytes); err != nil {
		return snap, fmt.Errorf("count candidates: %w", err)
	}
	return snap, nil
}

// compressRow 是一条候选行。
//
// `stored` 是**列里此刻真实存着的字节**（不是明文），`todo` 标记哪几列还是
// 本模式的活。两者由同一条 SELECT 一起取回：多要三个 `typeof` 不额外读载荷
// （它只读记录头），却省掉了"再判一次这列是什么形态"的第二份逻辑。
type compressRow struct {
	id     uint
	stored [colCount][]byte
	todo   [colCount]bool
}

// candidateBytes 是这一行**还该动的那些列**此刻的字节合计。
//
// 只算待动的列，与 bytes_total 的口径（plaintextBytesExpr）严格对齐：把已经
// 压过的列也算进来的话，`BytesBefore − BytesAfter` 就不再等于"这一批省掉的
// 字节"，而那是界面上最显眼的一栏。
func (r compressRow) candidateBytes() int64 {
	var n int64
	for i := range r.stored {
		if r.todo[i] {
			n += int64(len(r.stored[i]))
		}
	}
	return n
}

// wantsChange 报"这个 storage class 还是本模式该动的东西"。
//
// 只判 storage class、不判长度：压缩路上 `text` 这一档里装着两种东西——
// **非空明文**（要压）与**历史遗留的空串**（要归一化成 NULL），
// 两者都由 `packColumn` 处理，判据本身不必把它们分开。
// 回滚路上 `blob` 这一档只有帧一种，更不必判。
func (m compressMode) wantsChange(storage string) bool {
	if m == modeUnpack {
		return storage == "blob"
	}
	return storage == "text"
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
	// 三列名与三个 typeof 都从 chatIOColumns 拼出来，顺序天然与扫描目标对齐。
	cols := strings.Join(chatIOColumns[:], ", ")
	types := "typeof(" + strings.Join(chatIOColumns[:], "), typeof(") + ")"
	q := fmt.Sprintf(`SELECT id, %s, %s FROM chat_ios
		WHERE deleted_at IS NULL AND id > ? AND id <= ? AND %s AND updated_at <= ?
		ORDER BY id LIMIT ?`, cols, types, mode.candidateFilter())

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
		var (
			r       compressRow
			storage [colCount]string
		)
		// 扫描目标必须与 SELECT 的列序严格对齐：先是 id，再是三列的值，
		// 最后才是三个 typeof。交错着排会让 typeof 落进 []byte 里，
		// 而 SQLite 的 typeof 永远是文本——错位在第一次扫描就报错，还算走运。
		dest := make([]any, 0, 1+2*colCount)
		dest = append(dest, &r.id)
		for i := range chatIOColumns {
			dest = append(dest, &r.stored[i])
		}
		for i := range chatIOColumns {
			dest = append(dest, &storage[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scan candidate: %w", err)
		}
		for i := range chatIOColumns {
			r.todo[i] = mode.wantsChange(storage[i])
		}
		n := r.candidateBytes()
		if len(out) > 0 && total+n > policy.BatchBytes {
			break
		}
		total += n
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
		patches, err := rewriteBodies(ctx, mode, tx, rows)
		if err != nil {
			return err
		}
		for i, r := range rows {
			next.LastID = r.id
			next.Scanned++
			next.BytesBefore += r.candidateBytes()
			changed := false
			var after int64
			for c := range chatIOColumns {
				patch := patches[i][c]
				if !patch.set {
					// 这一列不用动（压不动就存明文 / 已经是这个形态 / 空值）。
					// 待动的列也要计进 BytesAfter，这样 BytesBefore−BytesAfter
					// 恰好是这一批省掉的字节数。
					if r.todo[c] {
						after += int64(len(r.stored[c]))
					}
					continue
				}
				if err := setColumn(ctx, tx, chatIOColumns[c], patch.value, r.id); err != nil {
					return err
				}
				changed = true
				if r.todo[c] {
					after += int64(len(patch.value))
				}
			}
			if changed {
				next.Packed++
			} else {
				next.Skipped++
			}
			next.BytesAfter += after
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

// columnValue 是一行里某一列要落库的新值。
//
// `set` 与 `value == nil` 必须分得很开，这是这套 patch 语义的全部：
//
//	set=false            这一列不动（压不动就存明文 / 已经是对的那个形态）；
//	set=true, value=nil  这一列写成 **NULL**（空值归一化，见 packColumn）；
//	set=true, value=…    这一列写成这些字节（明文落 TEXT、帧落 BLOB，由 BodyBytes 定）。
//
// 把前两种混起来是静默损坏：把"写 NULL"当成"不动"，空值就永远归一化不掉，
// 而判据又只剩 `typeof`——那 8,932 行 `of_string` 的空串就会被永久算成待迁移。
// 用结构体而不是拿 `[]byte{}` 当哨兵，就是不让这个区别靠"读者记得 nil 和空切片
// 不一样"来维持。
type columnValue struct {
	value []byte
	set   bool
}

// columnPatch 是一行里三列各自要落库的新值。
type columnPatch [colCount]columnValue

// rewriteBodies 算出每一行**该落库的字节**，nil 表示"这一列不用动"。
//
// **三列的接入点不同，这一点在这里必须显式分开**：`input` 可能要读写块表
// （引用序列里的块存在 `chat_io_blocks`），只能走 `models.PackBodies` /
// `models.UnpackBody`；响应体那两列是纯值变换，走序列化器同款的
// `PackColumnValue` / `UnpackColumnValue`。喂错函数的后果不是报错而是**解错**：
// 拿纯值变换去解引用序列，会把块表引用当成一段坏掉的压缩流。
//
// 落库的值统一交 `models.BodyBytes` 包（在 setColumn 里）：它是"让内容决定
// 存储类"的那个类型，明文落 TEXT、帧落 BLOB。回滚这条路尤其不能写错——
// 把明文按 BLOB 写回去，列里的格式就错了（`typeof` 不再对应形态），
// 而**读路径仍然读得出来**，于是这个错会一直躺着，直到下一次迁移把它漏掉。
func rewriteBodies(
	ctx context.Context, mode compressMode, tx *gorm.DB, rows []compressRow,
) ([]columnPatch, error) {
	out := make([]columnPatch, len(rows))

	if mode == modePack {
		// input 整列先取出来一起过 PackBodies：块要在**这一批内**封组，
		// 逐行各封各的会让组平均只有 7.70 KiB（阶段 3 真机实测），
		// 远低于 flate 的 32 KiB 窗口，白省 35.9%。见 models.PackBodies。
		inputs := make([][]byte, len(rows))
		for i, r := range rows {
			inputs[i] = r.stored[colInput]
		}
		packed, err := models.PackBodies(ctx, tx, inputs)
		if err != nil {
			return nil, err
		}
		for i, r := range rows {
			out[i][colInput] = packColumn(r, colInput, packed[i])
			for _, c := range []int{colOfString, colOfArray} {
				out[i][c] = packColumn(r, c, models.PackColumnValue(r.stored[c]))
			}
		}
		return out, nil
	}

	for i, r := range rows {
		for c := range chatIOColumns {
			if !r.todo[c] {
				continue
			}
			var (
				plain []byte
				err   error
			)
			if c == colInput {
				plain, err = models.UnpackBody(ctx, tx, r.stored[c])
			} else {
				plain, err = models.UnpackColumnValue(r.stored[c])
			}
			if err != nil {
				return nil, fmt.Errorf("unpack chat_io %d %s: %w", r.id, chatIOColumns[c], err)
			}
			// nil 是空值；和原字节一样说明这一列的 BLOB 根本不是帧（不该发生，
			// 候选判据只挑 blob，而不变量说 blob 一定是帧）。两种都跳过——
			// **拿不准就不动它**，这条路径上的每一条"我猜它应该是……"
			// 都是一次静默损坏的机会。
			if plain == nil || bytes.Equal(plain, r.stored[c]) {
				continue
			}
			out[i][c] = columnValue{value: plain, set: true}
		}
	}
	return out, nil
}

// packColumn 决定压缩路上一列的新值。
//
// `packed` 是编解码器给的结论（nil = 压不动、原样留明文），本函数在它之上补一条
// **空值归一化**：这一列此刻是空的 TEXT（`todo` 且零字节）就写成 NULL。
//
// 为什么非写不可：判据只剩 `typeof` 之后，"TEXT"与"非空明文"必须是同一件事，
// 否则 `of_string` 那 8,932 行空串（流式响应，正文在 `of_string_array` 里）
// 会被永远算成待迁移的行，而它们本来无事可做。
//
// 写 NULL 而不是"留着不动"是有意的：这一步把历史行里那个形态**收敛掉**，
// 从此 `typeof` 就是完整判据、状态页的"待迁移行数"也就精确了。读路径完全
// 不受影响——NULL 与空串读出来都是零长度（见 models/serializer.go 的 Scan）。
//
// 只在 `todo` 时归一化：NULL 本来就不是候选（`typeof(NULL)='null'`），
// 不该被这一趟顺手改一遍。
func packColumn(r compressRow, c int, packed []byte) columnValue {
	if !r.todo[c] {
		return columnValue{}
	}
	if len(r.stored[c]) == 0 {
		return columnValue{set: true} // 空的 TEXT → NULL
	}
	return columnValue{value: packed, set: packed != nil}
}

// setColumn 只改**真正变化的那一列**。
//
// 逐列发而不是一条 UPDATE 写三列：这条语料一行 470 KiB，把没变的列一起写回去
// 意味着凭空重写一遍它的 BLOB——纯粹的 IO 与 WAL 放大。回滚路径上更糟：
// 那一列此刻是明文，`BodyBytes` 会按内容判形态，值虽然对，但"写回去"这件事
// 本身是多余的（也正是"拿不准就不动它"想避免的那类动作）。
//
// 列名是拼进 SQL 的，但一个用户输入都到不了这里：它只可能是 chatIOColumns
// 里的常量字符串。值走参数绑定。
func setColumn(ctx context.Context, tx *gorm.DB, column string, value []byte, id uint) error {
	q := fmt.Sprintf(`UPDATE chat_ios SET %s = ? WHERE id = ?`, column)
	if err := tx.WithContext(ctx).Exec(q, models.BodyBytes(value), id).Error; err != nil {
		return fmt.Errorf("rewrite chat_io %d %s: %w", id, column, err)
	}
	return nil
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
			// 这里走阻塞入口：它自己占位，占不上就是"已经有别的入口在跑"。
			if _, err := RunLogCompress(ctx, false); err != nil {
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
