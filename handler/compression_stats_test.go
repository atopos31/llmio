package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
)

func TestGetTransportCompressionStats(t *testing.T) {
	before := service.GetCompressionStats()
	service.RecordUpstreamCompression(1000, 250)
	service.RecordSSECompression(2000, 500)

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/metrics/compression", nil)

	GetTransportCompressionStats(c)

	var env struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v, body=%s", err, rec.Body.String())
	}
	if env.Code != http.StatusOK {
		t.Fatalf("code = %d, body=%s", env.Code, rec.Body.String())
	}
	var stats service.CompressionStatsSnapshot
	if err := json.Unmarshal(env.Data, &stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if stats.Upstream.Count != before.Upstream.Count+1 {
		t.Fatalf("upstream count = %d, want %d", stats.Upstream.Count, before.Upstream.Count+1)
	}
	if stats.Upstream.BeforeBytes != before.Upstream.BeforeBytes+1000 {
		t.Fatalf("upstream before = %d", stats.Upstream.BeforeBytes)
	}
	if stats.SSE.Count != before.SSE.Count+1 || stats.SSE.AfterBytes != before.SSE.AfterBytes+500 {
		t.Fatalf("sse = %+v, before=%+v", stats.SSE, before.SSE)
	}
	if stats.StartedAt == "" {
		t.Fatal("started_at 不能为空")
	}
}
