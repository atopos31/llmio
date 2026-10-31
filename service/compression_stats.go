package service

import (
	"math"
	"sync/atomic"
	"time"
)

// CompressionMetric 是一条链路的进程内累计值。
type CompressionMetric struct {
	Count       int64   `json:"count"`
	BeforeBytes int64   `json:"before_bytes"`
	AfterBytes  int64   `json:"after_bytes"`
	SavedBytes  int64   `json:"saved_bytes"`
	Ratio       float64 `json:"ratio"`
}

// CompressionStatsSnapshot 是传输压缩效果快照。
// StartedAt 是本次进程开始统计的时间；重启后计数归零。
type CompressionStatsSnapshot struct {
	StartedAt string            `json:"started_at"`
	Upstream  CompressionMetric `json:"upstream"`
	SSE       CompressionMetric `json:"sse"`
}

type compressionCounter struct {
	Count       atomic.Int64
	BeforeBytes atomic.Int64
	AfterBytes  atomic.Int64
}

var (
	upstreamCompression  compressionCounter
	sseCompression       compressionCounter
	compressionStartedAt = time.Now()
)

func (c *compressionCounter) record(before, after int64) {
	if before <= 0 || after <= 0 {
		return
	}
	c.Count.Add(1)
	c.BeforeBytes.Add(before)
	c.AfterBytes.Add(after)
}

func (c *compressionCounter) snapshot() CompressionMetric {
	before := c.BeforeBytes.Load()
	after := c.AfterBytes.Load()
	ratio := 0.0
	if after > 0 {
		ratio = math.Round(float64(before)/float64(after)*100) / 100
	}
	return CompressionMetric{
		Count:       c.Count.Load(),
		BeforeBytes: before,
		AfterBytes:  after,
		SavedBytes:  before - after,
		Ratio:       ratio,
	}
}

// RecordUpstreamCompression 记录一次实际压缩发送的上游请求。
func RecordUpstreamCompression(before, after int64) {
	upstreamCompression.record(before, after)
}

// RecordSSECompression 记录一次正常完成的压缩流式响应。
func RecordSSECompression(before, after int64) {
	sseCompression.record(before, after)
}

// GetCompressionStats 返回当前进程的累计压缩效果。
func GetCompressionStats() CompressionStatsSnapshot {
	return CompressionStatsSnapshot{
		StartedAt: compressionStartedAt.Format(time.RFC3339),
		Upstream:  upstreamCompression.snapshot(),
		SSE:       sseCompression.snapshot(),
	}
}

// resetCompressionStats 只供测试使用；生产路径没有任何重置入口。
func resetCompressionStats() {
	upstreamCompression.Count.Store(0)
	upstreamCompression.BeforeBytes.Store(0)
	upstreamCompression.AfterBytes.Store(0)
	sseCompression.Count.Store(0)
	sseCompression.BeforeBytes.Store(0)
	sseCompression.AfterBytes.Store(0)
}
