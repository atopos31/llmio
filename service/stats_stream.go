package service

import (
	"context"
	"database/sql"

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

// openStatsLogs 打开分析取数的结果流。必须调用 close 释放（它同时负责判断截断）。
func openStatsLogs(ctx context.Context, f StatsFilter) (*statsLogStream, error) {
	db := models.DB.WithContext(ctx)
	rows, err := statsLogsQuery(db, f).Rows()
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
	var l models.ChatLog
	if err := s.db.ScanRows(s.rows, &l); err != nil {
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
