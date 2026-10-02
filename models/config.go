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

// LogReclaimState 是**空间回收**的运行记录。
//
// 与迁移的状态机不同，它没有水位：回收是一次一次独立的动作，跑到哪算哪，
// 中断了也没有"一半的形态"可言——文件该多大还是多大，只是没缩到位。
// 所以这里记的是**结果**，不是进度。
type LogReclaimState struct {
	// Status: idle / running / done / failed。`done` 里再分跑完没跑完，见 StopReason。
	Status string `json:"status"`
	// Source 是这一次是谁发起的：startup（启动期转换/VACUUM）或 manual（点回收）。
	Source string `json:"source"`
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
	// 生产驱动忽略参数、每条恰好放一页（见 service.reclaimPagesPerTx）。
	Calls      int    `json:"calls"`
	DurationMs int64  `json:"duration_ms"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	// StopReason 是收尾时的判定。
	// 增量回收（manual）：
	//   empty          —— freelist 空了，这一趟把能放的全放了
	//   budget         —— 到点了（本轮时间预算用完），还剩着没放，可以再点一次
	//   no_auto_vacuum —— 库的 auto_vacuum 不是 INCREMENTAL，这句 PRAGMA 是空操作
	//   stalled        —— 放了一批 freelist 却没少（引擎行为反常），主动收工并记 LastError
	// 启动期（startup）：
	//   converted            —— 转成了 auto_vacuum=INCREMENTAL（转换本身就是一次 VACUUM）
	//   vacuumed             —— 只做了启动期 VACUUM（DB_VACUUM=true）
	//   insufficient_space   —— 磁盘不够，**跳过**（不是失败：服务照常起）
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
