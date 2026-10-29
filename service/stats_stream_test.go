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

	logs, loadTruncated, err := LoadChatLogs(context.Background(), f)
	if err != nil {
		t.Fatalf("LoadChatLogs: %v", err)
	}
	want := Aggregate(logs, f)
	want.Truncated = loadTruncated
	fillTrendWindow(&want, f)
	resolveKeyGroups(context.Background(), &want)

	// GeneratedAt 是墙钟，必然不同
	got.GeneratedAt = 0
	want.GeneratedAt = 0
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal got: %v", err)
	}
	wantJSON, err := json.Marshal(&want)
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("流式聚合与切片聚合结果不一致\n流式: %s\n切片: %s", gotJSON, wantJSON)
	}

	// 顺带确认样本量对得上：断言的是"真的读了 120 行"，不是两张空表相等
	if got.KPI.Total != int64(len(logs)) || got.KPI.Total != 120 {
		t.Fatalf("KPI.Total = %d，want 120（读到的行数 %d）", got.KPI.Total, len(logs))
	}
}
