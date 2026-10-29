package service

import (
	"strings"
	"testing"
	"time"

	"github.com/atopos31/llmio/models"
	"gorm.io/gorm"
)

// 分析页取数必须走 created_at 索引，且不产生临时 B 树排序。
//
// 这条断言值得存在，因为退化不会报错、也不会少数据：语句形状没变，只是查询
// 计划器被一条"永远命中整张表"的索引引向了全表扫（chat_logs.deleted_at 恒为
// NULL，见 models.DropChatLogsDeletedAtIndex）。代价是线上 1.34 GB 的库上出现
// 2 秒的读事务——回滚日志模式读挡写，写请求排满 busy_timeout 后成片报
// SQLITE_BUSY，积压的请求再把容器顶到 OOM。
func TestStatsQueryUsesCreatedAtIndex(t *testing.T) {
	setupStatsDB(t)

	f := StatsFilter{From: time.Now().Add(-24 * time.Hour), To: time.Now()}
	sql := models.DB.ToSQL(func(tx *gorm.DB) *gorm.DB {
		var logs []models.ChatLog
		return statsLogsQuery(tx, f).Find(&logs)
	})
	if strings.TrimSpace(sql) == "" {
		t.Fatal("ToSQL 没拿到语句")
	}

	var plan []struct{ Detail string }
	if err := models.DB.Raw("EXPLAIN QUERY PLAN " + sql).Scan(&plan).Error; err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN 失败：%v\nSQL: %s", err, sql)
	}
	details := make([]string, 0, len(plan))
	for _, p := range plan {
		details = append(details, p.Detail)
	}
	joined := strings.Join(details, " | ")

	if !strings.Contains(joined, "idx_chat_logs_created_at") {
		t.Fatalf("取数没走 created_at 索引：%s", joined)
	}
	if strings.Contains(joined, "TEMP B-TREE") {
		t.Fatalf("出现了临时 B 树排序（说明索引只用来过滤、没提供顺序）：%s", joined)
	}
	if strings.Contains(joined, "idx_chat_logs_deleted_at") {
		t.Fatalf("又选上了 deleted_at 索引（它命中整张表）：%s", joined)
	}
}
