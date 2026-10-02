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

// LogCompressPolicy 是历史行迁移的策略。三个参数都是"每批多大"，不是"压多狠"——
// 压多狠（分块大小、组大小）是**写死在代码里的**，见 BlockChunkAvg 旁边的注释：
// 那三个数一旦有数据落库就不能改，改成可配置只会给人一个"改一下试试"的机会。
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
	BytesBefore int64  `json:"bytes_before"`
	BytesAfter  int64  `json:"bytes_after"`
	Attempts    int    `json:"attempts"` // 连续失败次数，成功一批就清零
	LastError   string `json:"last_error"`
	StartedAt   string `json:"started_at"`
	FinishedAt  string `json:"finished_at"`
}

// LogCompressBackup 记下迁移开始前的备份存证。
//
// 为什么要有这一条：迁移是不可逆的形态改写（要还原得走 decompress）。
// 出问题时最省事的路是"把备份盖回去"，而那要求**备份确实存在、且确实是迁移前
// 那一刻的库**。所以进状态页的东西必须是自己量出来的（大小 + mtime），
// 不是操作员口头保证的。
type LogCompressBackup struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	MTime  string `json:"mtime"`
	At     string `json:"at"`
	// Source 是**探出来的**结论，不是操作员说的：
	//   manual  —— 探到备份，且不比库小
	//   stale   —— 探到备份，但比库小（多半是迁移前的旧备份，盖回去会丢数据）
	//   missing —— 路径上什么都没有
	//   forced  —— 没探到，但操作员明确确认了要继续
	Source string `json:"source"`
}
