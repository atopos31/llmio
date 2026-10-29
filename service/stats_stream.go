package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/atopos31/llmio/models"
	"gorm.io/gorm"
)

// statsLogStream 逐行读取分析取数结果，从头到尾不把结果攒进切片。
//
// 为什么不是 `Find(&logs)` 一把梭：30 天窗口在真机上是 9 万行（按 ~3034 行/天
// 推算），一次性物化进切片实测 **51.7 MiB 常驻堆（602 B/行）**，而容器内存上限
// 只有 128 MiB——当天 3034 行的那次 OOM 只是导火索，这个才是"选个 30 天窗口就
// 复发"的隐患。逐行扫描之后，常驻堆只剩聚合自身的输出（同一实测里是 2.5 MiB），
// 与窗口里有多少行彻底脱钩。
//
// 代价是这条 SELECT 在执行期间保持打开（与原先 Find 一样要读完整个结果集），
// 所以读锁时长没有变化；变的只是"读出来的行放在哪"。
type statsLogStream struct {
	db    *gorm.DB
	rows  *sql.Rows
	count int
	err   error
}

// statsLogIter 逐条产出待聚合的日志。返回 false 表示取完了。
//
// 用迭代器而不是切片，是为了让聚合的内存占用与行数无关：聚合本身是单次遍历
// （见 aggregateWithBucketIter），喂一行算一行即可。
type statsLogIter func() (models.ChatLog, bool)

// statsLogColumns 是分析取数**真正需要**的列，逐列列出而不是 SELECT *。
//
// 两个理由：一是少搬十几列用不上的文本（每次打开分析页要过几万行，列数直接乘
// 在搬运行上）；二是让 Scan 的参数顺序与"表里新加了什么列"解耦——SELECT * 的
// 列序会随迁移变化，那时错位是静默的。
//
// COALESCE 把历史行里的 NULL 收成零值：老库里 tps / token / 单价 / 用户代理都
// 可能是 NULL（这些列都是后加的，AutoMigrate 不给存量行填默认值），而直接 Scan
// 到 string/float64 遇到 NULL 会报错。GORM 那边是自己把 NULL 落成零值，两边口径
// 必须一致——由 TestComputeStatsToleratesLegacyNullColumns 与
// TestComputeStatsMatchesAggregatePath 逐字段比对保证。
const statsLogColumns = `
	id, created_at,
	COALESCE(name, ''), COALESCE(provider_name, ''), COALESCE(provider_model, ''),
	COALESCE(user_agent, ''), COALESCE(status, ''),
	COALESCE(auth_key_id, 0), COALESCE(retry, 0),
	COALESCE(proxy_time, 0), COALESCE(first_chunk_time, 0), COALESCE(tps, 0),
	COALESCE(currency, ''),
	COALESCE(prompt_tokens, 0), COALESCE(completion_tokens, 0), COALESCE(total_tokens, 0),
	COALESCE(prompt_tokens_details, ''),
	COALESCE(input_price, 0), COALESCE(output_price, 0), COALESCE(cache_read_price, 0),
	COALESCE(error, '')`

// scanStatsLogRow 把一行直接扫进 models.ChatLog。
//
// 不用 gorm 的 ScanRows：它每行都走一遍反射找字段、分配中间值，实测比直接
// Scan 慢一倍多（90k 行：12.7 µs/行 → 约 5 µs/行）。分析页一次要过几万行，
// 这个差距在 100m CPU 的容器上就是几十秒。
func scanStatsLogRow(rows *sql.Rows) (models.ChatLog, error) {
	var (
		l       models.ChatLog
		details string
	)
	if err := rows.Scan(
		&l.ID, &l.CreatedAt, &l.Name, &l.ProviderName, &l.ProviderModel, &l.UserAgent, &l.Status,
		&l.AuthKeyID, &l.Retry, &l.ProxyTime, &l.FirstChunkTime, &l.Tps, &l.Currency,
		&l.PromptTokens, &l.CompletionTokens, &l.TotalTokens,
		&details,
		&l.InputPrice, &l.OutputPrice, &l.CacheReadPrice,
		&l.Error,
	); err != nil {
		return models.ChatLog{}, err
	}
	// 缓存命中数存在 JSON 列里（gorm serializer:json）。解析失败按零值处理：
	// 统计少算几个缓存 token 远好过整页报错。
	if details != "" {
		_ = json.Unmarshal([]byte(details), &l.PromptTokensDetails)
	}
	return l, nil
}

// openStatsLogs 打开分析取数的结果流。必须调用 close 释放（它同时负责判断截断）。
func openStatsLogs(ctx context.Context, f StatsFilter) (*statsLogStream, error) {
	db := models.DB.WithContext(ctx)
	rows, err := statsLogsQuery(db, f).Select(strings.TrimSpace(statsLogColumns)).Rows()
	if err != nil {
		return nil, err
	}
	return &statsLogStream{db: db, rows: rows}, nil
}

// next 取下一行。到达 StatsScanLimit 或出错时返回 false（错误由 close 报出）。
func (s *statsLogStream) next() (models.ChatLog, bool) {
	if s.err != nil || s.count >= StatsScanLimit {
		return models.ChatLog{}, false
	}
	if !s.rows.Next() {
		s.err = s.rows.Err()
		return models.ChatLog{}, false
	}
	l, err := scanStatsLogRow(s.rows)
	if err != nil {
		s.err = err
		return models.ChatLog{}, false
	}
	s.count++
	return l, true
}

// close 关闭结果流，并回答"是否被上限截断"。
//
// 判据与旧的"多读一行再截到 N"完全一致：SQL 侧 Limit 是 StatsScanLimit+1，
// 读满 N 行之后还能再读出一行，就说明窗口里还有数据没被聚合。
func (s *statsLogStream) close() (bool, error) {
	defer s.rows.Close()

	truncated := false
	if s.err == nil && s.count >= StatsScanLimit {
		truncated = s.rows.Next()
		if !truncated {
			s.err = s.rows.Err()
		}
	}
	return truncated, s.err
}
