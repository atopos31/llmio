package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// 这一组测试钉住两处"测试端点与真实转发不是同一条路"的地方：
//
//	连通性测试（ProviderTestHandler）与能力测试（TestReactHandler）
//
// 两者的结论只有在上游收到的请求与真实流量一致时才有意义。能力测试此前只给
// SDK 配了 base_url 与 api_key，自定义头与代理都没带——在强制自定义头的上游
// （如 opencode）上必然失败。

// upstreamRecorder 记录假上游收到的请求头。
//
// 假上游在 httptest 的另一个 goroutine 里跑，读写的先后没有必然次序，
// 因此用锁而不是裸变量（裸变量在 -race 下会报数据竞争）。
type upstreamRecorder struct {
	mu      sync.Mutex
	headers []http.Header
}

func (u *upstreamRecorder) record(h http.Header) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.headers = append(u.headers, h.Clone())
}

func (u *upstreamRecorder) first(t *testing.T) http.Header {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.headers) == 0 {
		t.Fatal("上游没有收到任何请求")
	}
	return u.headers[0]
}

// setupTestHandlerDB 建一张只含 Provider 与 ModelWithProvider 的库：
// 两个测试端点都只读这两张表。
func setupTestHandlerDB(t *testing.T, providerType, providerConfig, proxy string, customerHeaders map[string]string, withHeader bool) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test-handler.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.Provider{}, &models.ModelWithProvider{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}

	provider := models.Provider{Name: "prov-test", Type: providerType, Config: providerConfig, Proxy: proxy}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	association := models.ModelWithProvider{
		ProviderModel:   "test-model",
		ProviderID:      provider.ID,
		WithHeader:      &withHeader,
		CustomerHeaders: customerHeaders,
	}
	if err := db.Create(&association).Error; err != nil {
		t.Fatalf("create association: %v", err)
	}

	prev := models.DB
	models.DB = db
	t.Cleanup(func() {
		models.DB = prev
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
}

// testCtx 构造带 id 路径参数的 gin 上下文。
func testCtx(t *testing.T, id string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test/"+id, nil)
	// 入站请求头：with_header 为真时它会被透传，为假时不该出现在上游
	c.Request.Header.Set("X-From-Client", "1")
	c.Params = gin.Params{{Key: "id", Value: id}}
	return c, w
}

// assertCustomHeaders 断言这套请求头符合真实转发的语义。
// wantSession 是 {{session}} 应当渲染成的固定测试会话值——两个端点各自一个。
func assertCustomHeaders(t *testing.T, got http.Header, wantSession string) {
	t.Helper()

	if v := got.Get("X-Opencode-Session"); v != wantSession {
		t.Errorf("自定义头未送达上游：X-Opencode-Session = %q，期望 %q", v, wantSession)
	}
	// 与 provider.BuildReq 的次序一致：配置里的 api_key 覆盖同名自定义头
	if v := got.Get("Authorization"); v != "Bearer sk-provider" {
		t.Errorf("Authorization = %q，期望配置里的 api_key 覆盖自定义头", v)
	}
	if v := got.Get("X-Provider-Tag"); v != "abc" {
		t.Errorf("X-Provider-Tag = %q", v)
	}
}

func TestProviderTestHandlerSendsCustomHeaders(t *testing.T) {
	recorder := &upstreamRecorder{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(r.Header)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	setupTestHandlerDB(t, consts.StyleOpenAI,
		fmt.Sprintf(`{"base_url":%q,"api_key":"sk-provider"}`, upstream.URL), "",
		map[string]string{
			"X-Opencode-Session": "{{session}}",
			"X-Provider-Tag":     "abc",
			"Authorization":      "Bearer should-be-overwritten",
		}, false)

	c, w := testCtx(t, "1")
	ProviderTestHandler(c)

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，响应 = %s", w.Code, w.Body.String())
	}
	got := recorder.first(t)
	assertCustomHeaders(t, got, "llmio-connectivity-test")
	// with_header 为假时不得透传入站请求头
	if got.Get("X-From-Client") != "" {
		t.Error("with_header 为假时不应把客户端请求头透传给上游")
	}
}

func TestProviderTestHandlerForwardsIncomingHeadersWhenEnabled(t *testing.T) {
	recorder := &upstreamRecorder{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(r.Header)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	setupTestHandlerDB(t, consts.StyleOpenAI,
		fmt.Sprintf(`{"base_url":%q,"api_key":"sk-provider"}`, upstream.URL), "", nil, true)

	c, w := testCtx(t, "1")
	ProviderTestHandler(c)

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，响应 = %s", w.Code, w.Body.String())
	}
	if got := recorder.first(t).Get("X-From-Client"); got != "1" {
		t.Errorf("with_header 为真时应透传入站请求头，实际 %q", got)
	}
}

// sseChunk 一个"没有工具调用"的完整流：能力测试因此走完检查并报工具调用次数异常。
// 这里要的不是它的结论，而是它发出去的那个请求长什么样。
func sseChunk(model string) string {
	chunks := []string{
		fmt.Sprintf(`{"id":"c1","object":"chat.completion.chunk","created":1,"model":%q,"choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}`, model),
		fmt.Sprintf(`{"id":"c1","object":"chat.completion.chunk","created":1,"model":%q,"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`, model),
	}
	return "data: " + strings.Join(chunks, "\n\ndata: ") + "\n\ndata: [DONE]\n\n"
}

func TestTestReactHandlerSendsCustomHeaders(t *testing.T) {
	recorder := &upstreamRecorder{}
	seen := make(chan struct{}, 64)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(r.Header)
		seen <- struct{}{}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseChunk("test-model"))
	}))
	defer upstream.Close()

	setupTestHandlerDB(t, consts.StyleOpenAI,
		fmt.Sprintf(`{"base_url":%q,"api_key":"sk-provider"}`, upstream.URL), "",
		map[string]string{
			"X-Opencode-Session": "{{session}}",
			"X-Provider-Tag":     "abc",
			"Authorization":      "Bearer should-be-overwritten",
		}, false)

	c, w := testCtx(t, "1")
	TestReactHandler(c)

	// 等到上游确实收到了请求，再断言头；否则会把"没发出去"读成"没带头"
	select {
	case <-seen:
	case <-time.After(5 * time.Second):
		t.Fatal("上游在 5 秒内没有收到能力测试的请求")
	}

	assertCustomHeaders(t, recorder.first(t), "llmio-react-test")
	if body := w.Body.String(); !strings.Contains(body, "event:start") {
		t.Errorf("SSE 响应应以 start 事件开头，实际 %q", body)
	}
}

func TestTestReactHandlerRejectsNonOpenAIStyle(t *testing.T) {
	setupTestHandlerDB(t, consts.StyleAnthropic, `{"base_url":"http://example.invalid"}`, "", nil, false)

	c, w := testCtx(t, "1")
	TestReactHandler(c)

	if body := w.Body.String(); !strings.Contains(body, "该测试仅支持 OpenAI 类型") {
		t.Errorf("非 OpenAI 类型应明确拒绝，实际 %q", body)
	}
}

// 断言 FindChatModel 把自定义头与透传开关一并取出——两个端点都靠它拿配置。
func TestFindChatModelCarriesHeaderConfig(t *testing.T) {
	setupTestHandlerDB(t, consts.StyleOpenAI, `{"base_url":"http://example.invalid","api_key":"k"}`, "http://proxy.invalid:8080",
		map[string]string{"X-Tag": "v"}, true)

	chatModel, err := FindChatModel(t.Context(), "1")
	if err != nil {
		t.Fatalf("FindChatModel: %v", err)
	}
	if chatModel.CustomerHeaders["X-Tag"] != "v" {
		t.Errorf("CustomerHeaders = %v", chatModel.CustomerHeaders)
	}
	if chatModel.WithHeader == nil || !*chatModel.WithHeader {
		t.Error("WithHeader 未取出")
	}
	if chatModel.Proxy != "http://proxy.invalid:8080" {
		t.Errorf("Proxy = %q", chatModel.Proxy)
	}

	// ChatModel 的 JSON 形态是前端契约的一部分，字段名错了前端会静默拿不到值
	raw, err := json.Marshal(chatModel)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{`"with_header"`, `"customer_headers"`, `"proxy"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("序列化结果缺少 %s：%s", key, raw)
		}
	}
}
