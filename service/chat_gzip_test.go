package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/tidwall/gjson"
)

func setupGzipChatDB(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gzip-chat.db")
	models.Init(context.Background(), path)
	t.Cleanup(func() {
		if sqlDB, err := models.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
}

// gzipChatUpstream 记录每次收到的 Content-Encoding 与解压后的请求体。
type gzipChatUpstream struct {
	mu         sync.Mutex
	encodings  []string
	bodies     [][]byte
	rejectGzip bool
	status     int
}

func (u *gzipChatUpstream) handler(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	encoding := r.Header.Get("Content-Encoding")
	body := raw
	if encoding == "gzip" {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid gzip body","type":"invalid_request_error"}}`))
			return
		}
		body, err = io.ReadAll(zr)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid gzip body","type":"invalid_request_error"}}`))
			return
		}
	}

	u.mu.Lock()
	u.encodings = append(u.encodings, encoding)
	u.bodies = append(u.bodies, body)
	reject := u.rejectGzip && encoding == "gzip"
	status := u.status
	u.mu.Unlock()

	if reject {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"gzip request body is not supported","type":"invalid_request_error"}}`))
		return
	}
	if status != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream failure","type":"server_error"}}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"id":"cmpl","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
}

func (u *gzipChatUpstream) requestCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.encodings)
}

func (u *gzipChatUpstream) encodingAt(i int) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.encodings[i]
}

func (u *gzipChatUpstream) bodyAt(i int) []byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.bodies[i]
}

func gzipChatFixture(t *testing.T, upURL string) (ProvidersWithMeta, Before) {
	t.Helper()
	provider := models.Provider{
		Name:                 "gzip-upstream",
		Type:                 consts.StyleOpenAI,
		Config:               fmt.Sprintf(`{"base_url":%q,"api_key":"sk-test"}`, upURL),
		RequestGzipEnabled:   true,
		RequestGzipSupported: new(true),
	}
	provider.RequestGzipProbeHash = ProviderGzipHash(provider)
	if err := models.DB.Create(&provider).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	assoc := models.ModelWithProvider{ModelID: 1, ProviderID: provider.ID, ProviderModel: "gzip-model"}
	if err := models.DB.Create(&assoc).Error; err != nil {
		t.Fatalf("create assoc: %v", err)
	}
	if err := models.DB.Create(&models.Config{
		Key:   models.KeyTrafficCompressionPolicy,
		Value: `{"enabled":true,"sse_response":true,"upstream_request":true}`,
	}).Error; err != nil {
		t.Fatalf("create policy: %v", err)
	}
	meta := ProvidersWithMeta{
		ModelWithProviderMap: map[uint]models.ModelWithProvider{assoc.ID: assoc},
		WeightItems:          map[uint]int{assoc.ID: 1},
		ProviderMap:          map[uint]models.Provider{provider.ID: provider},
		MaxRetry:             2,
		TimeOut:              30,
		Strategy:             consts.BalancerLottery,
	}
	raw := []byte(fmt.Sprintf(`{"model":"ignored","messages":[{"role":"user","content":%q}]}`,
		strings.Repeat("长请求体", 2000)))
	if len(raw) < 4096 {
		t.Fatalf("测试请求体过小: %d", len(raw))
	}
	return meta, Before{Model: "gpt-gzip", raw: raw}
}

func TestBalanceChatGzipsUpstreamRequest(t *testing.T) {
	setupGzipChatDB(t)
	up := &gzipChatUpstream{}
	server := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(server.Close)

	meta, before := gzipChatFixture(t, server.URL)
	res, _, err := BalanceChat(context.Background(), time.Now(), consts.StyleOpenAI, before, meta, models.ReqMeta{}, NewBridgeNotes())
	if err != nil {
		t.Fatalf("BalanceChat: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if up.requestCount() != 1 {
		t.Fatalf("上游应收 1 发，实得 %d", up.requestCount())
	}
	if got := up.encodingAt(0); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := gjson.GetBytes(up.bodyAt(0), "model").String(); got != "gzip-model" {
		t.Fatalf("上游解压后的 model = %q, want gzip-model", got)
	}
}

func TestBalanceChatGzipDowngradesToPlaintext(t *testing.T) {
	setupGzipChatDB(t)
	up := &gzipChatUpstream{rejectGzip: true}
	server := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(server.Close)

	meta, before := gzipChatFixture(t, server.URL)
	res, _, err := BalanceChat(context.Background(), time.Now(), consts.StyleOpenAI, before, meta, models.ReqMeta{}, NewBridgeNotes())
	if err != nil {
		t.Fatalf("BalanceChat: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if up.requestCount() != 2 {
		t.Fatalf("上游应收 2 发（gzip + 明文），实得 %d", up.requestCount())
	}
	if up.encodingAt(0) != "gzip" || up.encodingAt(1) != "" {
		t.Fatalf("编码序列 = %q, want [gzip, plaintext]", up.encodings)
	}

	var stored models.Provider
	if err := models.DB.WithContext(context.Background()).
		Where("name = ?", "gzip-upstream").First(&stored).Error; err != nil {
		t.Fatalf("reload provider: %v", err)
	}
	if stored.RequestGzipEnabled {
		t.Fatal("运行时降级后提供商开关必须关闭")
	}
	if stored.RequestGzipSupported == nil || *stored.RequestGzipSupported {
		t.Fatal("运行时降级后支持状态必须写为 false")
	}
	waitForFailRows(t, before.Model, 1)
}

func TestBalanceChatGzipServerErrorDoesNotDowngrade(t *testing.T) {
	setupGzipChatDB(t)
	up := &gzipChatUpstream{status: http.StatusInternalServerError}
	server := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(server.Close)

	meta, before := gzipChatFixture(t, server.URL)
	res, _, err := BalanceChat(context.Background(), time.Now(), consts.StyleOpenAI, before, meta, models.ReqMeta{}, NewBridgeNotes())
	if err == nil {
		if res != nil {
			_ = res.Body.Close()
		}
		t.Fatal("500 应导致 BalanceChat 返回错误")
	}
	if up.requestCount() != 1 {
		t.Fatalf("500 不应触发明文重试，实得 %d 发", up.requestCount())
	}

	var stored models.Provider
	if err := models.DB.WithContext(context.Background()).
		Where("name = ?", "gzip-upstream").First(&stored).Error; err != nil {
		t.Fatalf("reload provider: %v", err)
	}
	if !stored.RequestGzipEnabled {
		t.Fatal("500 不应关闭提供商开关")
	}
	if stored.RequestGzipSupported == nil || !*stored.RequestGzipSupported {
		t.Fatal("500 不应改写支持状态")
	}
	waitForFailRows(t, before.Model, 1)
}

func TestBalanceChatMasterSwitchOffKeepsPlaintext(t *testing.T) {
	setupGzipChatDB(t)
	up := &gzipChatUpstream{}
	server := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(server.Close)

	meta, before := gzipChatFixture(t, server.URL)
	if err := models.DB.Model(&models.Config{}).
		Where("key = ?", models.KeyTrafficCompressionPolicy).
		Update("value", `{"enabled":false,"sse_response":true,"upstream_request":true}`).Error; err != nil {
		t.Fatalf("disable master switch: %v", err)
	}
	res, _, err := BalanceChat(context.Background(), time.Now(), consts.StyleOpenAI, before, meta, models.ReqMeta{}, NewBridgeNotes())
	if err != nil {
		t.Fatalf("BalanceChat: %v", err)
	}
	defer res.Body.Close()
	if up.requestCount() != 1 || up.encodingAt(0) != "" {
		t.Fatalf("总开关关闭时应明文发送，encodings = %q", up.encodings)
	}
}
