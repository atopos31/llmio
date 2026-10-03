package models

import "gorm.io/gorm"

type Config struct {
	gorm.Model
	Key   string // 配置类型
	Value string // 配置内容
}

const (
	KeyAnthropicCountTokens = "anthropic_count_tokens"
	KeyLogCleanupPolicy     = "log_cleanup_policy"
	// KeyLogCompressPolicy / KeyLogCompressState 是数据库压缩的迁移策略与进度。
	// 进度落在 Config 里而不是新开一张表：迁移状态就是一行 JSON，而新表要走
	// AutoMigrate——在 7 GB 的库上，能不加表就不加表。
	KeyLogCompressPolicy  = "log_compress_policy"
	KeyLogCompressState   = "log_compress_state"
	KeyLogDecompressState = "log_decompress_state"
	KeyLogCompressBackup  = "log_compress_backup"
	// KeyLogReclaimState 记的是**空间回收**（增量回收 / 启动转换）上一次干了什么。
	// 与迁移分开：回收不动数据，只动文件，而且它每次都跑完（不像迁移有水位）。
	KeyLogReclaimState = "log_reclaim_state"
	// KeyLogReclaimPolicy 是空间回收的**自动推进**策略（要不要定时回收、多大才值得动）。
	// 它独立于迁移策略：迁移是"改写数据形态"，回收是"把文件缩小"，两者的
	// 触发时机完全不是一回事——迁移完一次就不再跑了，回收要跟着删除量一直跑。
	KeyLogReclaimPolicy = "log_reclaim_policy"
)

type AnthropicCountTokens struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Version string `json:"version"`
}

type LogCleanupPolicy struct {
	Enabled       bool `json:"enabled"`
	RetentionDays int  `json:"retention_days"`
}

// LogCompressPolicy 是历史行迁移的策略。前三个参数是"每批多大"、第四个是
// "批与批之间歇多久"，都不是"压多狠"——压多狠（分块大小、组大小）是
// **写死在代码里的**，见 BlockChunkAvg 旁边的注释：那三个数一旦有数据落库就
// 不能改，改成可配置只会给人一个"改一下试试"的机会。
type LogCompressPolicy struct {
	// Enabled 管的是**后台自动推进**。手动触发（POST .../run）不看它——
	// 与日志清理的语义保持一致。
	Enabled bool `json:"enabled"`
	// BatchRows / BatchBytes 双重封顶。只按行数封顶会让"一批"的字节数随
	// body 大小失控（这份语料平均 470 KiB/行，64 行就是 30 MiB），
	// 而 PutBatch 要求整批原文同时在内存里。
	BatchRows  int   `json:"batch_rows"`
	BatchBytes int64 `json:"batch_bytes"`
	// QuiesceSec 是冷静期：updated_at 比它新的行不碰。它挡的是"正在被写的那一行"
	// ——Create 之后、补响应体的 Updates 之前，那一行是半成品。
	QuiesceSec int `json:"quiesce_sec"`
	// BatchIntervalMs 是批与批之间的**主动停顿**，0 表示不停。
	//
	// 它不改变任何一批的行为，改的是**迁移占用写锁的时间比例**。一批持锁约
	// 185 ms（真机 12,483 行 / 36s / 批 64 行折算），默认 0 时这批接着那批，
	// 迁移期间库有八成时间在写事务里——请求不会报错（busy_timeout 罩得住），
	// 但每次日志 INSERT 都要排队。
	//
	// 所以这是个**占用率旋钮**，不是性能旋钮：调大它迁移更慢、但库更闲。
	// "迁移该跑多凶"取决于这台机器同时在干什么，是运维判断而不是技术判断，
	// 所以它可配。
	BatchIntervalMs int `json:"batch_interval_ms"`
}

// LogCompressState 是迁移进度。水位与它写在**同一个事务**里（INV-1 的推广）：
// 中断点只有两个结果——整批连水位一起回滚，或者整批连水位一起生效。
type LogCompressState struct {
	// Status: idle / running / paused / done / failed。
	// 启动时读到 running 就是"上次被中断了"，直接续跑（水位在库里）。
	Status string `json:"status"`
	// LastID 是水位：id <= LastID 的行已经扫过。
	LastID uint `json:"last_id"`
	// MaxID 是本轮起始时的 id 快照。迁移期间新产生的行由写路径直接以新形态
	// 落库，不归这一轮管（否则水位会被不断推着往前跑，永远追不上）。
	MaxID     uint  `json:"max_id"`
	TotalRows int64 `json:"total_rows"` // 快照时的候选行数，进度条分母
	Scanned   int64 `json:"scanned"`
	Packed    int64 `json:"packed"`  // 真正改写了形态的行数
	Skipped   int64 `json:"skipped"` // 扫到但无需改动的行数
	// BytesBefore / BytesAfter 只统计**扫过的行**在这一列上的字节数，
	// 所以两者之差恰好是 input 列被省掉的字节——不含组表，组表要另算。
	BytesBefore int64 `json:"bytes_before"`
	BytesAfter  int64 `json:"bytes_after"`
	// BytesTotal 是**全表**原文在这一列上的合计，只在"从水位 0 起跑"那一次量。
	//
	// 它存在的唯一理由是当压缩比的分子。用 BytesBefore 当分子是错的：
	// 那个数只覆盖**扫过的行**，而分母（真正落库）是全表——半程时分子是全表
	// 的一半、分母是全表，算出来的比值恰好是真值的一半，一个看起来很专业的
	// 错误数字。两个数必须盖住同一批行。
	//
	// 它同时是"实时"的来源：从起跑那一刻起分子就固定了，分母随着行改形态
	// 一路缩，比值肉眼可见地往上爬；而 BytesBefore 要等跑完才等于它。
	//
	// 续跑时**保留**不重算：水位之后还有明文行不代表全表都还是明文，
	// 已经压过的行的原文已经不在了，量不回来。所以它的定义是"起跑时那一量"。
	BytesTotal int64  `json:"bytes_total"`
	Attempts   int    `json:"attempts"` // 连续失败次数，成功一批就清零
	LastError  string `json:"last_error"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
}

// LogReclaimPolicy 是**空间回收**的自动推进策略。
//
// 默认全关（Enabled=false），这不是保守，是这条动作的性质决定的：回收**全程持
// 写锁**，那是一段写请求会被排队的真实时间。"这台机器什么时候可以占用库"是运维
// 判断而不是技术判断，默认替用户做主，就等于把一个会被感知到的动作变成默认行为。
//
// 与迁移策略不同，这里没有"每批多大"这一档：回收的批次大小是**被 busy_timeout
// 推出来的**，而且现在由 service.nextBatchSize 按每批的实测耗时自己走
// （16–512 条语句/事务）。真机上就栽在"把 512 定死"上：换一份形态更散的库，
// 同样 512 条要 8.9 秒，越过 5 秒的写请求耐心，成片 500。它不是能按偏好调的旋钮。
type LogReclaimPolicy struct {
	// Enabled 打开后，服务会按 CheckIntervalSec 定期看一眼，够条件就自动跑一趟。
	// 手动点「回收」不看它（与迁移、日志清理的语义一致）。
	Enabled bool `json:"enabled"`
	// MinBytes 是"值得跑一趟"的门槛。没有它，一个刚回收过、只删了几行的库
	// 会在每个周期被拉起来占一次写锁，换回来几 MiB——收益与打扰完全不成比例。
	MinBytes int64 `json:"min_bytes"`
	// CheckIntervalSec 是多久看一次。看一次很便宜（一次 freelist 计数），
	// 所以它管的是"发现有空闲页之后多久动手"，不是性能旋钮。
	CheckIntervalSec int `json:"check_interval_sec"`
}

// LogReclaimState.Kind 的两个取值。只在 Go 侧用，前端拿到的仍是字符串——
// 加一个没见过的取值不该让老前端崩掉。
const (
	// ReclaimKindReclaim 是增量回收：一个事务里连打若干条 `PRAGMA incremental_vacuum`，
	// 一条放一页，慢慢把洞收掉。不需要额外磁盘，也不必停服务。
	ReclaimKindReclaim = "reclaim"
	// ReclaimKindVacuum 是重整：整库重写一遍，一次把 freelist 还干净。
	// 快得多，但要 2× 库大小的空闲磁盘和一个全程排他的窗口。
	ReclaimKindVacuum = "vacuum"
)

// LogReclaimState 是**空间回收**的运行记录。
//
// 与迁移的状态机不同，它没有水位：回收是一次一次独立的动作，跑到哪算哪，
// 中断了也没有"一半的形态"可言——文件该多大还是多大，只是没缩到位。
// 所以这里记的是**结果**，不是进度。
type LogReclaimState struct {
	// Status: idle / running / done / failed。`done` 里再分跑完没跑完，见 StopReason。
	Status string `json:"status"`
	// Kind 是这一次**做的是什么**：
	//   reclaim —— 增量回收：一条语句放一页，慢慢把洞收掉
	//   vacuum  —— 重整：整库重写一遍，把 freelist 一次还干净
	//   ""      —— 加这个字段之前写下的记录，一律当 reclaim（那时只有那一条路）
	//
	// 与 Source 正交，两者都需要：Source 说"谁发起"，Kind 说"做了什么"。
	// 同样是 startup 发起的，一次转换（VACUUM）和它之后可能接着跑的增量回收
	// 是两件事，界面上要给两套说法；手动那条路上更明显——「立即重整」和
	// 「开始回收」在记录里必须分得开。
	Kind string `json:"kind"`
	// Source 是这一次是谁发起的：
	//   startup   —— 启动期转换/VACUUM（在监听端口之前跑）
	//   manual    —— 用户在控制台点的
	//   scheduled —— 定时回收（见 LogReclaimPolicy）自己起来的
	Source string `json:"source"`
	// Continuous 记这一次是不是"持续到放完"。
	//
	// 它与 Rounds 一起构成回执：`Rounds=1` 的 done/budget 是"点了一下、跑满一段"，
	// 而 `Rounds=37` 才是"一路放到底"。不记这两项，两种完全不同的运维动作
	// 在记录里长得一模一样。
	Continuous bool `json:"continuous"`
	// Rounds 是跑了几轮。单轮模式恒为 1；持续模式下一轮一批（见
	// service.reclaimContinuousPause 的"一批一松手"），所以它同时是
	// "打了几批"的另一种写法。
	Rounds int `json:"rounds"`
	// PageSize 是页大小，用来把页数折成字节（界面上要说"放掉了 5.4 GiB"）。
	PageSize int64 `json:"page_size"`
	// FreedPages / FreedBytes 是这一次放掉的页数与字节数。
	FreedPages int64 `json:"freed_pages"`
	FreedBytes int64 `json:"freed_bytes"`
	// FileSizeBefore / FileSizeAfter 是文件大小的收尾。**回收的意义全在这两个数上**
	// ——freelist 少了不等于文件小了（auto_vacuum=0 时文件一字节都不会缩）。
	FileSizeBefore int64 `json:"file_size_before"`
	FileSizeAfter  int64 `json:"file_size_after"`
	FreelistBefore int64 `json:"freelist_before"`
	FreelistAfter  int64 `json:"freelist_after"`
	// Calls 是这一次打了几条 `PRAGMA incremental_vacuum`。它是**页数的同义词**：
	// 生产驱动忽略参数、每条恰好放一页（见 service.reclaimBatchMax）。
	Calls int `json:"calls"`
	// BatchSize 是这一趟**最后收敛到的**批量（一个事务里连打几条语句）。
	//
	// 它是诊断读数而不是配置：批量由 service.nextBatchSize 按每批实测耗时自己
	// 走，起点与边界见那边的 reclaimBatch*。记下来是因为"这台机器上一批能收到
	// 多少"直接决定"收完这个洞要几轮"，而它随库的形态走，事先算不出来。
	BatchSize  int    `json:"batch_size"`
	DurationMs int64  `json:"duration_ms"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	// StopReason 是收尾时的判定。
	// 增量回收（manual / scheduled）：
	//   empty          —— freelist 空了，这一趟把能放的全放了
	//   budget         —— 到点了（本轮时间预算用完），还剩着没放，可以再点一次
	//   stopped        —— 用户按了「停止」（只在持续模式里可能出现）
	//   busy           —— 持续模式的轮间松手之后，维护锁被别的任务抢走了，让给它
	//   no_auto_vacuum —— 库的 auto_vacuum 不是 INCREMENTAL，这句 PRAGMA 是空操作
	//   stalled        —— 放了一批 freelist 却没少（引擎行为反常），主动收工并记 LastError
	// 重整（vacuum，见 Kind）：
	//   vacuumed           —— 整库重写了一遍
	//   converted          —— 顺带把 auto_vacuum 转成了 INCREMENTAL（转换本身就是一次 VACUUM）
	//   insufficient_space —— 磁盘不够，**跳过**（不是失败：启动期服务照常起）
	//
	// 三条对 manual 与 startup 都成立，只有 `insufficient_space` 例外：手动那次
	// 走的是 StartVacuum 的**预检**，磁盘不够时压根不会开始（见 ErrVacuumNoSpace），
	// 不会留下一条"跑了但跳过"的记录。
	//
	// 两者共有：failed —— 出错。
	StopReason string `json:"stop_reason"`
	LastError  string `json:"last_error"`
}

// LogCompressBackup 记下迁移开始前的备份存证。
//
// 为什么要有这一条：迁移是不可逆的形态改写（要还原得走 decompress）。
// 出问题时最省事的路是"把备份盖回去"，而那要求**备份确实存在、且确实是迁移前
// 那一刻的库**。所以进状态页的东西必须是自己量出来的（大小 + mtime），
// 不是操作员口头保证的。
type LogCompressBackup struct {
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	MTime string `json:"mtime"`
	At    string `json:"at"`
	// Source 是**探出来的**结论，不是操作员说的：
	//   manual  —— 探到备份，且不比库小
	//   stale   —— 探到备份，但比库小（多半是迁移前的旧备份，盖回去会丢数据）
	//   missing —— 路径上什么都没有
	//   forced  —— 没探到，但操作员明确确认了要继续
	Source string `json:"source"`
}
