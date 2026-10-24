package service

import (
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

// BalanceChat 内层自愈闭环的伪上游测试（计划 §3.7）。三处核对：
// 客户端拿到什么（BalanceChat 返回的 res）、上游收到什么（伪上游记录的 body）、
// 日志记了什么（chat_logs 的失败行与成功行）。

func setupSelfHealChatDB(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "selfheal-chat.db")
	models.Init(context.Background(), path)
	t.Cleanup(func() {
		if sqlDB, err := models.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
}

// pickyUpstream 伪上游：按收到的次数回 400（报错文本由脚本给出）或 200。
type pickyUpstream struct {
	mu     sync.Mutex
	bodies []string
	// errors[i] 是第 i+1 发的 400 报错文本；发完 errors 后一律 200
	errors []string
}

func (p *pickyUpstream) handler(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	p.mu.Lock()
	n := len(p.bodies)
	p.bodies = append(p.bodies, string(raw))
	errText := ""
	if n < len(p.errors) {
		errText = p.errors[n]
	}
	p.mu.Unlock()

	if errText != "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"error":{"message":%q,"type":"invalid_request_error"}}`, errText)))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"id":"cmpl","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
}

func (p *pickyUpstream) requestCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.bodies)
}

func (p *pickyUpstream) lastBody() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.bodies[len(p.bodies)-1]
}

func selfHealFixture(t *testing.T, up *httptest.Server) (ProvidersWithMeta, Before) {
	t.Helper()
	provider := models.Provider{Name: "picky", Type: "openai", Config: fmt.Sprintf(`{"base_url":%q,"api_key":"sk-test"}`, up.URL)}
	if err := models.DB.Create(&provider).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	assoc := models.ModelWithProvider{ModelID: 1, ProviderID: provider.ID, ProviderModel: "picky-model"}
	if err := models.DB.Create(&assoc).Error; err != nil {
		t.Fatalf("create assoc: %v", err)
	}
	meta := ProvidersWithMeta{
		ModelWithProviderMap: map[uint]models.ModelWithProvider{assoc.ID: assoc},
		WeightItems:          map[uint]int{assoc.ID: 1},
		ProviderMap:          map[uint]models.Provider{provider.ID: provider},
		MaxRetry:             2,
		TimeOut:              30,
		Strategy:             consts.BalancerLottery,
	}
	before := Before{Model: "gpt-selfheal", raw: []byte(`{"model":"gpt-selfheal","messages":[{"role":"user","content":"hi"}],"top_k":40}`)}
	return meta, before
}

func waitChatLogs(t *testing.T, name string, want int) []models.ChatLog {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var rows []models.ChatLog
		if err := models.DB.Where("name = ?", name).Order("id asc").Find(&rows).Error; err != nil {
			t.Fatalf("query chat logs: %v", err)
		}
		// 失败行走异步 goroutine，等齐 want 行再断言
		allSettled := len(rows) >= want
		for _, r := range rows {
			if r.Status == consts.StatusRunning {
				allSettled = false
			}
		}
		if allSettled {
			return rows
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("等不到 %d 条结算日志", want)
	return nil
}

func TestBalanceChatSelfHealsDroppedParam(t *testing.T) {
	setupSelfHealChatDB(t)
	up := &pickyUpstream{errors: []string{"Unrecognized request argument supplied: top_k"}}
	server := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(server.Close)

	meta, before := selfHealFixture(t, server)
	notes := NewBridgeNotes()

	res, log, err := BalanceChat(context.Background(), time.Now(), "openai", before, meta, models.ReqMeta{}, notes)
	if err != nil {
		t.Fatalf("BalanceChat: %v", err)
	}
	defer res.Body.Close()

	// 成功行由 handler 落库（BalanceChat 只返回 log，Status 初始为 running，
	// RecordLog 按响应补成 success）；测试里替 handler 补齐这两步
	log.Status = consts.StatusSuccess
	if _, err := SaveChatLog(context.Background(), *log); err != nil {
		t.Fatalf("save success log: %v", err)
	}

	// 客户端拿到 200
	if res.StatusCode != http.StatusOK {
		t.Fatalf("客户端应拿到 200，实得 %d", res.StatusCode)
	}
	// 上游恰 2 发，第 2 发无 top_k、messages 原样
	if up.requestCount() != 2 {
		t.Fatalf("上游应收 2 发，实得 %d", up.requestCount())
	}
	last := up.lastBody()
	if gjson.GetBytes([]byte(last), "top_k").Exists() {
		t.Fatalf("重发的 body 仍带 top_k：%s", last)
	}
	if !gjson.GetBytes([]byte(last), "messages.0.content").Exists() {
		t.Fatalf("重发的 body 丢了消息：%s", last)
	}
	// 日志：1 条失败行（400 原文 + 自愈标记）+ 1 条成功行（记账 + Retry 计数）
	rows := waitChatLogs(t, "gpt-selfheal", 2)
	errRows, okRows := 0, 0
	for _, r := range rows {
		switch r.Status {
		case consts.StatusError:
			errRows++
			if !strings.Contains(r.Error, "(self-heal: dropped top_k)") || !strings.Contains(r.Error, "Unrecognized request argument") {
				t.Fatalf("失败行原文应含 400 报文与自愈标记：%q", r.Error)
			}
		case consts.StatusSuccess:
			okRows++
			if !strings.Contains(r.BridgeNotes, "self_heal_dropped:top_k") {
				t.Fatalf("成功行记账缺 self_heal_dropped:top_k，实得 %q", r.BridgeNotes)
			}
			if r.Retry != 1 {
				t.Fatalf("成功行 Retry 应为 1（自愈一次），实得 %d", r.Retry)
			}
		}
	}
	if errRows != 1 || okRows != 1 {
		t.Fatalf("日志应为 1 失败 + 1 成功，实得 %d/%d", errRows, okRows)
	}
	_ = log
}

func TestBalanceChatSelfHealBudgetExhausted(t *testing.T) {
	setupSelfHealChatDB(t)
	// 连报 4 个不同参数：预算 3 次删完仍 400 → Delete 换家（池空 → pop err）
	up := &pickyUpstream{errors: []string{
		"Unrecognized request argument supplied: top_k",
		"Unrecognized request argument supplied: logit_bias",
		"Unrecognized request argument supplied: stop_sequences",
		"Unrecognized request argument supplied: metadata",
	}}
	server := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(server.Close)

	meta, before := selfHealFixture(t, server)
	before.raw = []byte(`{"model":"gpt-selfheal","messages":[{"role":"user","content":"hi"}],"top_k":40,"logit_bias":{"a":1},"stop_sequences":["x"],"metadata":{"u":1}}`)
	meta.MaxRetry = 1

	_, _, err := BalanceChat(context.Background(), time.Now(), "openai", before, meta, models.ReqMeta{}, NewBridgeNotes())
	if err == nil {
		t.Fatal("预算耗尽后应返回错误")
	}
	if !strings.Contains(err.Error(), "All retry failed") {
		t.Fatalf("收尾错误应是 All retry failed，实得 %q", err.Error())
	}
	// 上游恰 4 发（3 次删参重发 + 1 次终判），最后一发已无前三个参数
	if up.requestCount() != 4 {
		t.Fatalf("上游应收 4 发，实得 %d", up.requestCount())
	}
	last := gjson.Parse(up.lastBody())
	for _, k := range []string{"top_k", "logit_bias", "stop_sequences"} {
		if last.Get(k).Exists() {
			t.Fatalf("最后一发仍带 %s", k)
		}
	}
	if !last.Get("metadata").Exists() {
		t.Fatal("第四个参数不该被删（预算已尽）")
	}
}

func TestBalanceChatSelfHealDisabled(t *testing.T) {
	setupSelfHealChatDB(t)
	// 策略关闭：行为与现状一致，1 次 400 即 Delete 换家
	policy := `{"enabled":false}`
	if err := models.DB.Create(&models.Config{Key: models.KeySelfHealPolicy, Value: policy}).Error; err != nil {
		t.Fatalf("seed config: %v", err)
	}

	up := &pickyUpstream{errors: []string{"Unrecognized request argument supplied: top_k"}}
	server := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(server.Close)

	meta, before := selfHealFixture(t, server)
	meta.MaxRetry = 1

	_, _, err := BalanceChat(context.Background(), time.Now(), "openai", before, meta, models.ReqMeta{}, NewBridgeNotes())
	if err == nil {
		t.Fatal("关闭自愈后应失败")
	}
	if up.requestCount() != 1 {
		t.Fatalf("关闭自愈后上游只应收 1 发，实得 %d", up.requestCount())
	}
}

func TestBalanceChat429NotSelfHealed(t *testing.T) {
	setupSelfHealChatDB(t)
	// 429 不进自愈：即使报错文本命中模式也不删参，走 Reduce 降权
	var mu sync.Mutex
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"Unknown parameter: 'top_k'."}}`))
	}))
	t.Cleanup(server.Close)

	meta, before := selfHealFixture(t, server)
	meta.MaxRetry = 1

	_, _, err := BalanceChat(context.Background(), time.Now(), "openai", before, meta, models.ReqMeta{}, NewBridgeNotes())
	if err == nil {
		t.Fatal("429 应失败")
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Fatalf("429 不该重发，实发 %d 次", hits)
	}
}
