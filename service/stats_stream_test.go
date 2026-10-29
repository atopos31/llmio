package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
)

// seedStatsRows 造一批形状接近线上的行：状态、模型、提供商、UA 都有多值，
// 单价与 token 非零，失败行带错误文本。
func seedStatsRows(t *testing.T, n int) time.Time {
	t.Helper()
	base := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	rows := make([]models.ChatLog, 0, n)
	for i := range n {
		status := consts.StatusSuccess
		errText := ""
		switch i % 5 {
		case 2:
			status = consts.StatusError
			errText = `status: 429, body: rate limited`
		case 4:
			status = consts.StatusRunning
		}
		rows = append(rows, models.ChatLog{
			Name:           fmt.Sprintf("model-%d", i%3),
			ProviderName:   fmt.Sprintf("provider-%d", i%2),
			ProviderModel:  fmt.Sprintf("pm-%d", i%3),
			Status:         status,
			UserAgent:      fmt.Sprintf("ua-%d", i%4),
			Style:          consts.StyleOpenAI,
			UpstreamStyle:  consts.StyleOpenAI,
			CreatedAt:      base.Add(time.Duration(i) * time.Minute),
			ProxyTime:      time.Duration(100+i) * time.Millisecond,
			FirstChunkTime: time.Duration(50+i%40) * time.Millisecond,
			Tps:            float64(20 + i%30),
			Size:           1000 + i,
			Retry:          i % 3,
			Backup:         i%7 == 0,
			Error:          errText,
			Usage: models.Usage{
				PromptTokens:        100,
				CompletionTokens:    50,
				TotalTokens:         150,
				PromptTokensDetails: models.PromptTokensDetails{CachedTokens: 10},
			},
			InputPrice:     1.5,
			OutputPrice:    4,
			CacheReadPrice: 0.5,
			Currency:       "CNY",
		})
	}
	if err := models.DB.CreateInBatches(rows, 50).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	return base
}

// ComputeStats 现在走流式读，LoadChatLogs + Aggregate 走切片读。两条路必须
// 逐字段给出**完全相同**的结果——否则"省内存"就成了"悄悄换了口径"。
func TestComputeStatsMatchesAggregatePath(t *testing.T) {
	setupStatsDB(t)
	base := seedStatsRows(t, 120)

	f := StatsFilter{
		From:        base.Add(-time.Hour),
		To:          base.Add(4 * time.Hour),
		Granularity: "1h",
	}

	got, truncated, err := ComputeStats(context.Background(), f)
	if err != nil {
		t.Fatalf("ComputeStats: %v", err)
	}
	if truncated {
		t.Fatal("120 行不该触发截断")
	}

	logs, _, err := LoadChatLogs(context.Background(), f)
	if err != nil {
		t.Fatalf("LoadChatLogs: %v", err)
	}
	assertSameStats(t, f, got, logs)

	// 顺带确认样本量对得上：断言的是"真的读了 120 行"，不是两张空表相等
	if got.KPI.Total != int64(len(logs)) || got.KPI.Total != 120 {
		t.Fatalf("KPI.Total = %d，want 120（读到的行数 %d）", got.KPI.Total, len(logs))
	}
}

// assertSameStats 把"流式直读 + 聚合"的结果与"GORM 参照路径 + 聚合"逐字段比对。
//
// 两条路的差别只在**怎么把行读出来**：直读 Scan 用 COALESCE 兜 NULL、自己解
// JSON 列，GORM 走它自己的 NULL/类型处理。这个断言就是两者口径的合同。
func assertSameStats(t *testing.T, f StatsFilter, got *StatsResult, want []models.ChatLog) {
	t.Helper()

	wantRes := Aggregate(want, f)
	fillTrendWindow(&wantRes, f)
	resolveKeyGroups(context.Background(), &wantRes)

	// GeneratedAt 是墙钟，必然不同
	got.GeneratedAt = 0
	wantRes.GeneratedAt = 0
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal got: %v", err)
	}
	wantJSON, err := json.Marshal(&wantRes)
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("直读与 GORM 参照路径的结果不一致\n直读: %s\nGORM: %s", gotJSON, wantJSON)
	}
}

// 老库里的历史行可能是整片 NULL（tps / token / 单价 / 时段都是后加的列）。
// 直读 Scan 必须与 GORM 一样把它们当零值——否则分析页在老库上直接 500。
func TestComputeStatsToleratesLegacyNullColumns(t *testing.T) {
	setupStatsDB(t)

	// 除了必备的几列，其余一律留 NULL，模拟十几代迁移之前写下的行
	for _, ts := range []string{"2026-09-29 10:00:00", "2026-09-29 10:05:00", "2026-09-29 10:10:00"} {
		if err := models.DB.Exec(`INSERT INTO chat_logs
			(created_at, name, provider_name, provider_model, status, user_agent, auth_key_id, retry,
			 proxy_time, first_chunk_time, tps, prompt_tokens, completion_tokens, total_tokens,
			 prompt_tokens_details, input_price, output_price, cache_read_price, currency, error)
			VALUES (?, 'legacy', 'p', 'pm', 'success', NULL, NULL, NULL, NULL, NULL, NULL,
			        NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL)`, ts).Error; err != nil {
			t.Fatalf("seed legacy row: %v", err)
		}
	}

	// 再混一条正常的失败行：确认 NULL 与有值的行混在一起也一致
	if err := models.DB.Exec(`INSERT INTO chat_logs
		(created_at, name, provider_name, provider_model, status, error, retry)
		VALUES ('2026-09-29 10:15:00', 'legacy', 'p', 'pm', 'error', 'status: 429, body: rate limited', 2)`).Error; err != nil {
		t.Fatalf("seed error row: %v", err)
	}

	f := StatsFilter{From: time.Date(2026, 9, 29, 9, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 29, 11, 0, 0, 0, time.UTC)}
	got, truncated, err := ComputeStats(context.Background(), f)
	if err != nil {
		t.Fatalf("ComputeStats: %v", err)
	}
	if truncated {
		t.Fatal("4 行不该触发截断")
	}
	if got.KPI.Total != 4 || got.KPI.Failed != 1 {
		t.Fatalf("NULL 行没被算进 KPI：%+v", got.KPI)
	}

	logs, _, err := LoadChatLogs(context.Background(), f)
	if err != nil {
		t.Fatalf("LoadChatLogs: %v", err)
	}
	assertSameStats(t, f, got, logs)
}
