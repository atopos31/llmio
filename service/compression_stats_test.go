package service

import (
	"math"
	"testing"
)

func TestCompressionStatsSnapshot(t *testing.T) {
	resetCompressionStats()
	t.Cleanup(resetCompressionStats)

	RecordUpstreamCompression(1000, 300)
	RecordUpstreamCompression(500, 100)
	RecordSSECompression(2000, 400)

	stats := GetCompressionStats()
	if stats.StartedAt == "" {
		t.Fatal("started_at 不能为空")
	}
	if stats.Upstream.Count != 2 {
		t.Fatalf("upstream count = %d, want 2", stats.Upstream.Count)
	}
	if stats.Upstream.BeforeBytes != 1500 || stats.Upstream.AfterBytes != 400 {
		t.Fatalf("upstream bytes = %d/%d, want 1500/400", stats.Upstream.BeforeBytes, stats.Upstream.AfterBytes)
	}
	if stats.Upstream.SavedBytes != 1100 {
		t.Fatalf("upstream saved = %d, want 1100", stats.Upstream.SavedBytes)
	}
	if math.Abs(stats.Upstream.Ratio-3.75) > 0.001 {
		t.Fatalf("upstream ratio = %v, want 3.75", stats.Upstream.Ratio)
	}
	if stats.SSE.Count != 1 || stats.SSE.BeforeBytes != 2000 || stats.SSE.AfterBytes != 400 {
		t.Fatalf("sse = %+v", stats.SSE)
	}
	if math.Abs(stats.SSE.Ratio-5) > 0.001 {
		t.Fatalf("sse ratio = %v, want 5", stats.SSE.Ratio)
	}

	// 非法记录（0/负值）不应改变任何计数。
	RecordUpstreamCompression(0, 100)
	RecordSSECompression(100, 0)
	RecordSSECompression(-1, -1)
	afterInvalid := GetCompressionStats()
	if afterInvalid.Upstream.Count != 2 || afterInvalid.SSE.Count != 1 {
		t.Fatalf("非法记录被计入: %+v", afterInvalid)
	}
}

func TestCompressionStatsZeroState(t *testing.T) {
	resetCompressionStats()
	t.Cleanup(resetCompressionStats)

	stats := GetCompressionStats()
	if stats.Upstream.Count != 0 || stats.Upstream.Ratio != 0 || stats.SSE.Count != 0 || stats.SSE.Ratio != 0 {
		t.Fatalf("空统计应全为 0: %+v", stats)
	}
}
