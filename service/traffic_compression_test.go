package service

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atopos31/llmio/models"
	"gorm.io/gorm"
)

func setupTrafficCompressionDB(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "traffic-compression.db")
	models.Init(context.Background(), path)
	t.Cleanup(func() {
		if sqlDB, err := models.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
}

func TestEnsureTrafficCompressionPolicyConfig(t *testing.T) {
	setupTrafficCompressionDB(t)
	ctx := context.Background()

	if err := EnsureTrafficCompressionPolicyConfig(ctx); err != nil {
		t.Fatalf("ensure policy: %v", err)
	}
	policy := GetTrafficCompressionPolicy(ctx)
	if policy.Enabled || policy.SSEResponse || policy.UpstreamRequest {
		t.Fatalf("默认策略必须全关，got %+v", policy)
	}

	// 幂等：再种一次不应覆盖已有值。
	if err := models.DB.Model(&models.Config{}).
		Where("key = ?", models.KeyTrafficCompressionPolicy).
		Update("value", `{"enabled":true,"sse_response":true,"upstream_request":true}`).Error; err != nil {
		t.Fatalf("update policy: %v", err)
	}
	if err := EnsureTrafficCompressionPolicyConfig(ctx); err != nil {
		t.Fatalf("ensure policy again: %v", err)
	}
	policy = GetTrafficCompressionPolicy(ctx)
	if !policy.Enabled || !policy.SSEResponse || !policy.UpstreamRequest {
		t.Fatalf("幂等种子覆盖了已有值，got %+v", policy)
	}
}

func TestGetTrafficCompressionPolicyFallsBackToDefault(t *testing.T) {
	setupTrafficCompressionDB(t)
	ctx := context.Background()

	for _, value := range []string{"", "not-json"} {
		if value == "" {
			if err := models.DB.Create(&models.Config{Key: models.KeyTrafficCompressionPolicy, Value: value}).Error; err != nil {
				t.Fatalf("create config: %v", err)
			}
		} else if err := models.DB.Model(&models.Config{}).
			Where("key = ?", models.KeyTrafficCompressionPolicy).
			Update("value", value).Error; err != nil {
			t.Fatalf("update config: %v", err)
		}
		policy := GetTrafficCompressionPolicy(ctx)
		if policy.Enabled || policy.SSEResponse || policy.UpstreamRequest {
			t.Fatalf("坏值 %q 必须回默认全关，got %+v", value, policy)
		}
	}
}

func TestProviderGzipHash(t *testing.T) {
	base := models.Provider{Type: "openai", Config: `{"base_url":"https://api.example.com/"}`, Proxy: ""}
	h1 := ProviderGzipHash(base)
	if h1 == "" || len(h1) != 64 {
		t.Fatalf("hash 长度异常: %q", h1)
	}
	if h1 != ProviderGzipHash(base) {
		t.Fatal("同一配置的 hash 必须稳定")
	}
	if h1 != ProviderGzipHash(models.Provider{Type: "openai", Config: `{"base_url":"https://api.example.com"}`, Proxy: ""}) {
		t.Fatal("末尾斜杠不应改变 hash")
	}
	for _, changed := range []models.Provider{
		{Type: "anthropic", Config: base.Config},
		{Type: "openai", Config: `{"base_url":"https://other.example.com"}`},
		{Type: "openai", Config: base.Config, Proxy: "http://proxy.local:8080"},
	} {
		if ProviderGzipHash(changed) == h1 {
			t.Fatalf("配置变化后 hash 必须变化: %+v", changed)
		}
	}
}

func TestShouldGzipUpstream(t *testing.T) {
	supported := new(true)
	unsupported := new(false)
	baseProvider := models.Provider{
		Type:                 "openai",
		Config:               `{"base_url":"https://api.example.com"}`,
		RequestGzipEnabled:   true,
		RequestGzipSupported: supported,
	}
	baseProvider.RequestGzipProbeHash = ProviderGzipHash(baseProvider)
	enabledPolicy := &models.TrafficCompressionPolicy{Enabled: true, SSEResponse: true, UpstreamRequest: true}

	tests := []struct {
		name     string
		policy   *models.TrafficCompressionPolicy
		provider models.Provider
		bodyLen  int
		header   http.Header
		want     bool
	}{
		{"all conditions met", enabledPolicy, baseProvider, 4096, http.Header{}, true},
		{"below threshold", enabledPolicy, baseProvider, 4095, http.Header{}, false},
		{"master off", &models.TrafficCompressionPolicy{UpstreamRequest: true}, baseProvider, 4096, http.Header{}, false},
		{"feature off", &models.TrafficCompressionPolicy{Enabled: true}, baseProvider, 4096, http.Header{}, false},
		{"policy nil", nil, baseProvider, 4096, http.Header{}, false},
		{"provider switch off", enabledPolicy, func() models.Provider {
			p := baseProvider
			p.RequestGzipEnabled = false
			return p
		}(), 4096, http.Header{}, false},
		{"unknown support", enabledPolicy, func() models.Provider {
			p := baseProvider
			p.RequestGzipSupported = nil
			return p
		}(), 4096, http.Header{}, false},
		{"unsupported", enabledPolicy, func() models.Provider {
			p := baseProvider
			p.RequestGzipSupported = unsupported
			return p
		}(), 4096, http.Header{}, false},
		{"probe hash missing", enabledPolicy, func() models.Provider {
			p := baseProvider
			p.RequestGzipProbeHash = ""
			return p
		}(), 4096, http.Header{}, false},
		{"probe hash stale", enabledPolicy, func() models.Provider {
			p := baseProvider
			p.RequestGzipProbeHash = "stale"
			return p
		}(), 4096, http.Header{}, false},
		{"content encoding gzip", enabledPolicy, baseProvider, 4096, http.Header{"Content-Encoding": []string{"gzip"}}, false},
		{"content encoding identity", enabledPolicy, baseProvider, 4096, http.Header{"Content-Encoding": []string{"identity"}}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldGzipUpstream(tc.policy, tc.provider, tc.bodyLen, tc.header); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestMarkProviderRequestGzipUnsupported(t *testing.T) {
	setupTrafficCompressionDB(t)
	ctx := context.Background()
	provider := models.Provider{
		Name:                 "probe",
		Type:                 "openai",
		Config:               `{"base_url":"https://api.example.com","api_key":"sk-test"}`,
		RequestGzipEnabled:   true,
		RequestGzipSupported: new(true),
	}
	if err := models.DB.Create(&provider).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}

	if err := MarkProviderRequestGzipUnsupported(ctx, provider.ID, strings.Repeat("x", 300)); err != nil {
		t.Fatalf("mark unsupported: %v", err)
	}
	got, err := gorm.G[models.Provider](models.DB).Where("id = ?", provider.ID).First(ctx)
	if err != nil {
		t.Fatalf("reload provider: %v", err)
	}
	if got.RequestGzipEnabled {
		t.Fatal("provider 级开关必须关闭")
	}
	if got.RequestGzipSupported == nil || *got.RequestGzipSupported {
		t.Fatal("支持状态必须写为 false")
	}
	if got.RequestGzipProbedAt == nil {
		t.Fatal("探测时间必须写入")
	}
	if len(got.RequestGzipProbeDetail) != 255 {
		t.Fatalf("证据摘要应截断到 255，got %d", len(got.RequestGzipProbeDetail))
	}
}

func TestTrafficCompressionPolicyJSONShape(t *testing.T) {
	// 前端按这三个键读写，键名变化会让设置页静默失效。
	raw, err := json.Marshal(DefaultTrafficCompressionPolicy())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{`"enabled"`, `"sse_response"`, `"upstream_request"`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("默认策略缺少键 %s: %s", key, raw)
		}
	}
}
