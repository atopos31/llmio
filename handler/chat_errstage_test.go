package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupErrstageDB 建转发路径要读写的最小表集，并写入一条可用的模型关联。
func setupErrstageDB(t *testing.T, baseURL string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "errstage-handler.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.Provider{}, &models.Model{}, &models.ModelWithProvider{}, &models.ChatLog{}, &models.Config{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}

	provider := models.Provider{
		Name:   "prov-errstage",
		Type:   consts.StyleOpenAI,
		Config: `{"base_url":"` + baseURL + `","api_key":"sk-test"}`,
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model := models.Model{Name: "m-test", MaxRetry: 1, TimeOut: 5, Strategy: consts.BalancerDefault}
	if err := db.Create(&model).Error; err != nil {
		t.Fatalf("create model: %v", err)
	}
	enabled := true
	association := models.ModelWithProvider{
		ModelID:       model.ID,
		ProviderID:    provider.ID,
		ProviderModel: "test-model",
		Status:        &enabled,
		Weight:        1,
	}
	if err := db.Create(&association).Error; err != nil {
		t.Fatalf("create association: %v", err)
	}

	prev := models.DB
	models.DB = db
	t.Cleanup(func() {
		awaitRecordLogs(t)
		models.DB = prev
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
}

// 流式转发中途客户端断开：落库的 error 必须以
// "[response_stream] canceled by client or intermediary proxy" 开头。
// 阶段与归因只能在 handler 的 io.Copy 失败处打（那里离请求 ctx 最近），
// 错误经管道传给 RecordLog 原文落库——这条链路断任何一环，日志里就只剩
// 一句无从排查的 "context canceled"。
func TestChatHandlerStreamClientCancelMarksStage(t *testing.T) {
	// 上游先吐一个 SSE 块，然后挂着不结束，等客户端断开时一并被取消
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(upstream.Close)

	setupErrstageDB(t, upstream.URL)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = context.WithValue(ctx, consts.ContextKeyAllowAllModel, true)

	body := `{"model":"m-test","messages":[{"role":"user","content":"hi"}],"stream":true}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")

	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		ChatCompletionsHandler(c)
	}()

	// 等转发路径把 running 日志落库（SaveChatLog 同步发生在流式转发之前）
	waitFor(t, "running 日志", func() bool {
		n, err := gorm.G[models.ChatLog](models.DB).Where("status = ?", consts.StatusRunning).Count(context.Background(), "id")
		return err == nil && n > 0
	})

	cancel()
	select {
	case <-handlerDone:
	case <-time.After(10 * time.Second):
		t.Fatal("chatHandler 未在取消后返回")
	}

	// RecordLog 跑在后台协程：轮询等它把 error 落库
	waitFor(t, "error 日志落库", func() bool {
		n, err := gorm.G[models.ChatLog](models.DB).Where("status = ?", consts.StatusError).Count(context.Background(), "id")
		return err == nil && n > 0
	})

	log, err := gorm.G[models.ChatLog](models.DB).Where("status = ?", consts.StatusError).First(context.Background())
	if err != nil {
		t.Fatalf("query error log: %v", err)
	}
	const wantPrefix = "[response_stream] canceled by client"
	if !strings.HasPrefix(log.Error, wantPrefix) {
		t.Fatalf("落库 error = %q，期望以 %q 开头", log.Error, wantPrefix)
	}
}

// waitFor 每 20ms 轮询一次条件，最多等 5 秒。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待 %s 超时", what)
}
