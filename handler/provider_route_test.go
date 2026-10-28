package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/middleware"
	"github.com/atopos31/llmio/models"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

/**
 * 提供商专属地址 /p/{name}/*path 端到端行为。
 *
 * 钉住五类契约：
 *  1. 鉴权：路由组挂着 authDedicated（x-api-key 优先、回落 Bearer），无凭据 401；
 *     受限密钥对无法定位模型的透传/纯转发请求，以及已知端点上未授权的模型，一律 403
 *     （专属地址不允许成为绕过模型权限的后门）。
 *  2. 兼容：全局 /v1 的未知路径照旧 404（不引入全局透传），而专属地址的
 *     已知端点走与 /v1 相同的 handler（行为一致，不钉死提供商）。
 *  3. 透传：未知路径固定转给 URL 指名的提供商，绑定命中时改写模型名，
 *     未绑定原样；模型名不安全（路径穿越、控制字符）一律拒绝。
 *  4. 记账：透传请求落一条 Style=passthrough 的日志，token 为空。
 *  5. 归一化：转发路径剥掉与上游 base_url 版本段重复的那一级（/v1 或 /v1beta，
 *     混合透传与纯转发同一套规则）；base_url 没有版本段时仍按 /v1 剥离。
 */

// dedicatedUpstream 记录假上游收到的请求与 body，供断言。
type dedicatedUpstream struct {
	mu          sync.Mutex
	path        string
	auth        string
	accept      string
	contentType string
	body        string
	rawQuery    string
	headers     http.Header
	// resp* 是给个别用例定制的响应；留空表示默认的 {"output":"ok"} JSON。
	respBody        string
	respContentType string
	respStatus      int
	// respDelay 让假上游延迟响应，给「首字节耗时」断言一个不依赖机器时钟
	// 精度的确定时长（Windows 上 time.Since 的粒度可能粗到毫秒级）。
	respDelay time.Duration
}

func (u *dedicatedUpstream) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := ""
		if r.Body != nil {
			buf := make([]byte, 65536)
			n, _ := r.Body.Read(buf)
			body = string(buf[:n])
		}
		u.mu.Lock()
		u.path = r.URL.Path
		u.auth = r.Header.Get("Authorization")
		u.accept = r.Header.Get("Accept")
		u.contentType = r.Header.Get("Content-Type")
		u.body = body
		u.rawQuery = r.URL.RawQuery
		u.headers = r.Header.Clone()
		respBody := u.respBody
		respContentType := u.respContentType
		respStatus := u.respStatus
		respDelay := u.respDelay
		u.mu.Unlock()

		if respDelay > 0 {
			time.Sleep(respDelay)
		}

		// 默认响应用于绝大多数用例；日志增强的用例用 setResponse 定制成带 usage
		// 的 JSON 或 SSE 流。
		if respBody == "" {
			respBody = `{"output":"ok"}`
		}
		if respContentType == "" {
			respContentType = "application/json"
		}
		if respStatus == 0 {
			respStatus = http.StatusOK
		}
		w.Header().Set("Content-Type", respContentType)
		w.WriteHeader(respStatus)
		_, _ = w.Write([]byte(respBody))
	}
}

func (u *dedicatedUpstream) snapshot() (path, auth, accept, body string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.path, u.auth, u.accept, u.body
}

func (u *dedicatedUpstream) sentContentType() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.contentType
}

// sentHeader 取假上游收到的某个请求头，转发策略用例用它断言头有没有串味。
func (u *dedicatedUpstream) sentHeader(key string) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.headers.Get(key)
}

// sentRawQuery 取假上游收到的原始 query，用于断言凭据参数被剥离。
func (u *dedicatedUpstream) sentRawQuery() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.rawQuery
}

// setResponse 定制假上游的响应（body 为空则回到默认 JSON），供日志/用量用例使用。
func (u *dedicatedUpstream) setResponse(body, contentType string, status int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.respBody = body
	u.respContentType = contentType
	u.respStatus = status
}

// setResponseDelay 让假上游延迟响应，用来稳定测量首字节耗时。
func (u *dedicatedUpstream) setResponseDelay(delay time.Duration) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.respDelay = delay
}

type dedicatedFixture struct {
	router    *gin.Engine
	upstream  *dedicatedUpstream
	serverURL string
}

// setupDedicatedRoute 搭建包含专属地址路由的 gin 引擎、数据库（alpha-name 混合
// 可绑定、beta-name 纯转发、gamma-name 关闭、delta-name 纯转发且 base_url 自带
// 版本段、epsilon-name Gemini 地址且关闭、eta-name Gemini 地址且混合），以及
// 指向它们的假上游。
func setupDedicatedRoute(t *testing.T, authHeader string) *dedicatedFixture {
	t.Helper()

	gin.SetMode(gin.TestMode)

	upstream := &dedicatedUpstream{}
	server := httptest.NewServer(upstream.handler(t))
	t.Cleanup(server.Close)

	path := filepath.Join(t.TempDir(), "dedicated-route.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.Provider{}, &models.Model{}, &models.ModelWithProvider{}, &models.ChatLog{}, &models.ChatIO{}, &models.AuthKey{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}

	// 三家覆盖 ForwardMode 三态：alpha 混合（false）、beta 纯转发（true）、
	// gamma 关闭（nil）。混合模式的透传行为全部钉在 alpha 上；delta 只用于复刻
	// base_url 自带版本段的生产形态。
	// slug 与名称解耦：这里仍取同名，保持既有用例的 URL 不变；改写策略留空
	// （等价 known），个别用例在 provider 上就地覆盖。
	alpha := models.Provider{
		Name:           "alpha-name",
		Type:           "openai",
		Config:         `{"base_url":"` + server.URL + `","api_key":"sk-upstream"}`,
		ForwardMode:    new(false),
		ForwardSlug:    new("alpha-name"),
		ForwardRewrite: consts.ForwardRewriteKnown,
	}
	beta := models.Provider{
		Name:           "beta-name",
		Type:           "openai",
		Config:         `{"base_url":"` + server.URL + `","api_key":"sk-upstream"}`,
		ForwardMode:    new(true),
		ForwardSlug:    new("beta-name"),
		ForwardRewrite: consts.ForwardRewriteNone,
	}
	gamma := models.Provider{
		Name:        "gamma-name",
		Type:        "openai",
		Config:      `{"base_url":"` + server.URL + `","api_key":"sk-upstream"}`,
		ForwardSlug: new("gamma-name"),
	}
	// delta 复刻生产形态：base_url 按模板惯例自带版本段（.../v1），客户端仍用
	// UI 给出的 /p/{name}/v1 —— 纯转发必须剥掉重复的 /v1，不能拼出 /v1/v1/...。
	delta := models.Provider{
		Name:           "delta-name",
		Type:           "openai",
		Config:         `{"base_url":"` + server.URL + `/v1","api_key":"sk-upstream"}`,
		ForwardMode:    new(true),
		ForwardSlug:    new("delta-name"),
		ForwardRewrite: consts.ForwardRewriteNone,
	}
	// epsilon / eta 是 Gemini 地址：地址协议由类型决定，与转发模式无关。
	// epsilon 关闭（nil）——Gemini 原生端点照样是本地端点；eta 混合（false）——
	// 原生对话同样走本地流水线，不再透传给上游。
	epsilon := models.Provider{
		Name:           "epsilon-name",
		Type:           "gemini",
		Config:         `{"base_url":"` + server.URL + `/v1beta","api_key":"sk-gemini"}`,
		ForwardSlug:    new("epsilon-name"),
		ForwardRewrite: consts.ForwardRewriteNone,
	}
	eta := models.Provider{
		Name:           "eta-name",
		Type:           "gemini",
		Config:         `{"base_url":"` + server.URL + `/v1beta","api_key":"sk-gemini"}`,
		ForwardMode:    new(false),
		ForwardSlug:    new("eta-name"),
		ForwardRewrite: consts.ForwardRewriteNone,
	}
	// 逐个直接 Create：循环变量副本会让 GORM 把自增 ID 写进副本而非 alpha/beta/gamma
	if err := db.Create(&alpha).Error; err != nil {
		t.Fatalf("create provider alpha: %v", err)
	}
	if err := db.Create(&beta).Error; err != nil {
		t.Fatalf("create provider beta: %v", err)
	}
	if err := db.Create(&gamma).Error; err != nil {
		t.Fatalf("create provider gamma: %v", err)
	}
	if err := db.Create(&delta).Error; err != nil {
		t.Fatalf("create provider delta: %v", err)
	}
	if err := db.Create(&epsilon).Error; err != nil {
		t.Fatalf("create provider epsilon: %v", err)
	}
	if err := db.Create(&eta).Error; err != nil {
		t.Fatalf("create provider eta: %v", err)
	}

	chatModel := models.Model{Name: "latest"}
	if err := db.Create(&chatModel).Error; err != nil {
		t.Fatalf("create model: %v", err)
	}
	// latest 绑定到 alpha-name，上游名 latest-direct
	assoc := models.ModelWithProvider{
		ModelID:       chatModel.ID,
		ProviderID:    alpha.ID,
		ProviderModel: "latest-direct",
		Status:        new(true),
	}
	if err := db.Create(&assoc).Error; err != nil {
		t.Fatalf("create association: %v", err)
	}
	// Gemini 原生模型各绑一家：池里只有一个候选，断言能确定落到哪家。
	for _, bound := range []struct {
		modelName     string
		providerID    uint
		providerModel string
	}{
		{"gemini-native", epsilon.ID, "gemini-native-direct"},
		{"gemini-mixed", eta.ID, "gemini-mixed-direct"},
	} {
		// 走本地流水线需要非零重试次数与超时：MaxRetry=0 时 chat 循环一轮都不跑。
		m := models.Model{Name: bound.modelName, MaxRetry: 3, TimeOut: 10}
		if err := db.Create(&m).Error; err != nil {
			t.Fatalf("create gemini model %s: %v", bound.modelName, err)
		}
		gemAssoc := models.ModelWithProvider{
			ModelID:       m.ID,
			ProviderID:    bound.providerID,
			ProviderModel: bound.providerModel,
			Status:        new(true),
			// 权重大于 0 才会进加权轮询：0 权重等于这家不参与。
			Weight: 1,
		}
		if err := db.Create(&gemAssoc).Error; err != nil {
			t.Fatalf("create gemini association %s: %v", bound.modelName, err)
		}
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

	router := gin.New()
	var authMiddleware gin.HandlerFunc = testAuth(authHeader)
	if authHeader == "__none__" {
		authMiddleware = disabledAuth()
	}
	p := router.Group("/p", authMiddleware)
	{
		p.Any("/:slug/*path", ProviderDedicatedRoute)
	}

	return &dedicatedFixture{router: router, upstream: upstream, serverURL: server.URL}
}

// testAuth 直接复用 middleware.AuthDedicated：真实实现保证测试与线上一致
// （/p 组线上挂的就是它，x-api-key 优先、回落 Bearer）。
func testAuth(adminToken string) gin.HandlerFunc {
	return middleware.AuthDedicated(adminToken)
}

// disabledAuth 模拟"未携带凭据"的 401 拦截。
func disabledAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "Authorization key is missing"})
	}
}

func doDedicated(t *testing.T, f *dedicatedFixture, method, target, auth, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	// 固定 UA，日志增强的用例要断言它原样落到 ChatLog。
	req.Header.Set("User-Agent", "llmio-test/1.0")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

// 透传请求的 body 里没有可用模型定位（无 model），受限密钥应被 403。
func TestDedicatedRoutePassthroughWithoutModelForbidden(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	// 受限密钥：只允许模型 latest（但请求不带 model）
	key := models.AuthKey{Name: "limited", Key: "sk-limited", Status: new(true), AllowAll: new(false), Models: []string{"latest"}}
	if err := models.DB.Create(&key).Error; err != nil {
		t.Fatalf("create auth key: %v", err)
	}

	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/alpha/search", "Bearer sk-limited", `{}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("受限密钥无 model 透传应 403, got %d: %s", w.Code, w.Body.String())
	}
}

// 全量权限（admin token）时，无 model 的透传放行。
func TestDedicatedRoutePassthroughWithoutModelAllowedWithFullPermission(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/alpha/search", "Bearer sk-admin", `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("admin 无 model 透传应 200, got %d: %s", w.Code, w.Body.String())
	}
	path, auth, _, body := f.upstream.snapshot()
	if path != "/alpha/search" {
		t.Errorf("上游收到 path = %q, want /alpha/search", path)
	}
	if auth != "Bearer sk-upstream" {
		t.Errorf("上游应收到提供商自己的鉴权头, got %q", auth)
	}
	if body != `{}` {
		t.Errorf("body 应原样, got %q", body)
	}
}

// 绑定命中：model=latest 改写为 latest-direct 后透传；权限按模型校验放行。
func TestDedicatedRoutePassthroughRewritesBoundModel(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/alpha/search", "Bearer sk-admin",
		`{"model":"latest","id":"search-1","commands":{}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}
	_, _, _, body := f.upstream.snapshot()
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("上游 body 不是 JSON: %q", body)
	}
	if got["model"] != "latest-direct" {
		t.Errorf("model 应改写为绑定上的 latest-direct, got %v", got["model"])
	}
}

// 未绑定该提供商的模型名原样透传。
func TestDedicatedRoutePassthroughKeepsUnboundModel(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/alpha/search", "Bearer sk-admin",
		`{"model":"gpt-native-x"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}
	_, _, _, body := f.upstream.snapshot()
	if !strings.Contains(body, `"model":"gpt-native-x"`) {
		t.Errorf("未绑定模型应原样透传, got %q", body)
	}
}

// /v1beta/... 这类版本变体路径必须整段保留：/v1 前缀剥离只认完整段（后随段边界），
// 不能把 /v1beta 咬成 beta/...——否则 Gemini 原生客户端在混合模式下直接 400。
func TestDedicatedRoutePassthroughKeepsV1BetaPath(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	w := doDedicated(t, f, http.MethodPost,
		"/p/alpha-name/v1beta/models/gemini-2.5-pro:generateContent", "Bearer sk-admin",
		`{"contents":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}
	path, _, _, _ := f.upstream.snapshot()
	if path != "/v1beta/models/gemini-2.5-pro:generateContent" {
		t.Errorf("上游 path = %q, want /v1beta/models/gemini-2.5-pro:generateContent", path)
	}
}

// 客户端 Content-Type 原样透传：multipart（audio 转写等）的 boundary 丢了上游就无法解析，
// 不能被强改为 application/json。
func TestDedicatedRoutePassthroughPreservesContentType(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	req := httptest.NewRequest(http.MethodPost, "/p/alpha-name/v1/audio/transcriptions",
		strings.NewReader("--boundary\r\nbody"))
	req.Header.Set("Authorization", "Bearer sk-admin")
	req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}
	if ct := f.upstream.sentContentType(); ct != "multipart/form-data; boundary=boundary" {
		t.Errorf("上游 Content-Type = %q, want multipart 原样", ct)
	}
}

// 专属地址与全局 /anthropic 同一套双头鉴权：Anthropic 原生客户端（x-api-key）
// 不得在 /p 上撞 401。受限密钥带 model 的透传按模型级权限放行。
func TestDedicatedRouteXAPIKeyPassthrough(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	key := models.AuthKey{Name: "limited", Key: "sk-limited", Status: new(true), AllowAll: new(false), Models: []string{"latest"}}
	if err := models.DB.Create(&key).Error; err != nil {
		t.Fatalf("create auth key: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/p/alpha-name/v1/alpha/search",
		strings.NewReader(`{"model":"latest"}`))
	req.Header.Set("x-api-key", "sk-limited")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("x-api-key 受限密钥带 model 透传应 200, got %d: %s", w.Code, w.Body.String())
	}
}

// 已知端点同样放 x-api-key：/p/{name}/v1/messages 进入 chat 流水线（空 model 报错）而非 401。
func TestDedicatedRouteXAPIKeyKnownEndpoint(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	req := httptest.NewRequest(http.MethodPost, "/p/alpha-name/v1/messages", strings.NewReader(`{}`))
	req.Header.Set("x-api-key", "sk-admin")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Fatalf("x-api-key 不应在专属地址上 401")
	}
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "model") {
		t.Fatalf("应进入 chat 流水线（空 model 报错 500）: %d %s", w.Code, w.Body.String())
	}
}

// 受限密钥对可定位模型的透传走模型级权限：拥有该模型则放行。
func TestDedicatedRoutePassthroughModelPermission(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	key := models.AuthKey{Name: "limited", Key: "sk-limited", Status: new(true), AllowAll: new(false), Models: []string{"latest"}}
	if err := models.DB.Create(&key).Error; err != nil {
		t.Fatalf("create auth key: %v", err)
	}

	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/alpha/search", "Bearer sk-limited",
		`{"model":"latest","commands":{}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("受限密钥有模型权限应 200, got %d: %s", w.Code, w.Body.String())
	}
	_, _, _, body := f.upstream.snapshot()
	if !strings.Contains(body, `"model":"latest-direct"`) {
		t.Errorf("绑定模型应改写, got %q", body)
	}
}

// 已知端点的模型级权限同样生效：受限密钥（x-api-key）请求未授权的模型 → 403。
// 专属地址不允许成为绕过模型权限的后门——Anthropic 形态的已知端点也不例外。
func TestDedicatedRouteXAPIKeyKnownEndpointForbidden(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	key := models.AuthKey{Name: "limited", Key: "sk-limited", Status: new(true), AllowAll: new(false), Models: []string{"other-model"}}
	if err := models.DB.Create(&key).Error; err != nil {
		t.Fatalf("create auth key: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/p/alpha-name/v1/messages",
		strings.NewReader(`{"model":"latest","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", "sk-limited")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("受限密钥请求未授权模型应 403, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "latest") {
		t.Errorf("错误信息应指明被拒的模型: %s", w.Body.String())
	}
}

// 纯转发模式对受限密钥一律 403（无模型语义，无法按模型鉴权），与凭据头形态无关。
// 用 x-api-key 一并覆盖 AuthDedicated → forwardAll 的链路。
func TestDedicatedRouteForwardModeForbiddenForLimitedKey(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	key := models.AuthKey{Name: "limited", Key: "sk-limited", Status: new(true), AllowAll: new(false), Models: []string{"latest"}}
	if err := models.DB.Create(&key).Error; err != nil {
		t.Fatalf("create auth key: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/p/beta-name/v1/chat/completions",
		strings.NewReader(`{"model":"native-model","messages":[]}`))
	req.Header.Set("x-api-key", "sk-limited")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("受限密钥纯转发应 403, got %d: %s", w.Code, w.Body.String())
	}
}

// 无凭据 401（组中间件拦截）。
func TestDedicatedRouteUnauthorized(t *testing.T) {
	f := setupDedicatedRoute(t, "__none__")

	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/alpha/search", "", `{}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("无凭据应 401, got %d", w.Code)
	}
}

// 已知端点直调现有 handler：与 /v1 同语义（这里验证 responses 能进入
// chatHandler 流水线并因 model 为空报错，而不是被透传给上游）。
func TestDedicatedRouteKnownEndpointGoesChatPipeline(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/responses", "Bearer sk-admin", `{}`)
	// chatHandler 对空 model 报 "model is empty"（500），说明走的是流水线而不是透传
	if w.Code == http.StatusOK {
		t.Fatalf("已知端点应进入 chat 流水线（空 model 报错），不应透传成功: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "model") {
		t.Errorf("错误信息应指向 model 校验: %s", w.Body.String())
	}
}

// 全局 /v1 未知路径照旧 404——不引入全局透传（安全回归断言）。
func TestGlobalV1UnknownPathStays404(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	router := gin.New()
	v1 := router.Group("/v1")
	{
		v1.GET("/models", func(c *gin.Context) { c.Status(http.StatusOK) })
	}
	_ = f

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/alpha/search", strings.NewReader(`{}`))
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("/v1/alpha/search 应 404, got %d", w.Code)
	}
}

// 纯转发模式：所有路径直转、不改写 model、任意方法放行。
func TestDedicatedRouteForwardMode(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	w := doDedicated(t, f, http.MethodPost, "/p/beta-name/v1/chat/completions", "Bearer sk-admin",
		`{"model":"native-model","messages":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("纯转发应 200, got %d: %s", w.Code, w.Body.String())
	}
	path, auth, _, body := f.upstream.snapshot()
	// 客户端写的是 /p/beta-name/v1/chat/completions：剥掉与上游 base_url 版本段
	// 重复的 /v1 后，上游应收到 /chat/completions（beta 的 base_url 无版本段）。
	if path != "/chat/completions" {
		t.Errorf("上游 path = %q, want /chat/completions", path)
	}
	if !strings.Contains(body, `"model":"native-model"`) {
		t.Errorf("纯转发不改写 model, got %q", body)
	}
	if auth != "Bearer sk-upstream" {
		t.Errorf("纯转发也要换上游鉴权头, got %q", auth)
	}

	// GET 也放行
	w = doDedicated(t, f, http.MethodGet, "/p/beta-name/v1/files", "Bearer sk-admin", "")
	if w.Code != http.StatusOK {
		t.Fatalf("纯转发 GET 应 200, got %d", w.Code)
	}
}

// 生产形态回归：上游 base_url 自带版本段（.../v1）、客户端用 UI 给出的专属地址
// /p/{name}/v1 —— 纯转发必须剥掉与 base_url 重复的 /v1，否则上游收到 /v1/v1/... 直接 404。
func TestDedicatedRouteForwardModeStripsClientVersionSegment(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	w := doDedicated(t, f, http.MethodPost, "/p/delta-name/v1/chat/completions", "Bearer sk-admin",
		`{"model":"native-model","messages":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("纯转发应 200, got %d: %s", w.Code, w.Body.String())
	}
	if path, _, _, _ := f.upstream.snapshot(); path != "/v1/chat/completions" {
		t.Errorf("上游 path = %q, want /v1/chat/completions（只应有一级 /v1）", path)
	}

	w = doDedicated(t, f, http.MethodGet, "/p/delta-name/v1/models", "Bearer sk-admin", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET 纯转发应 200, got %d: %s", w.Code, w.Body.String())
	}
	if path, _, _, _ := f.upstream.snapshot(); path != "/v1/models" {
		t.Errorf("上游 path = %q, want /v1/models", path)
	}
}

// 转发关闭（ForwardMode nil）：未知路径 404，上游不得收到任何请求。
// 这是"关闭"档位的核心契约——专属地址退化为只有已知端点的普通入口。
func TestDedicatedRouteForwardDisabledUnknownPath404(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	w := doDedicated(t, f, http.MethodPost, "/p/gamma-name/v1/alpha/search", "Bearer sk-admin", `{}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("关闭转发的未知路径应 404, got %d: %s", w.Code, w.Body.String())
	}
	if path, _, _, _ := f.upstream.snapshot(); path != "" {
		t.Errorf("关闭转发不得触碰上游, 上游收到 %q", path)
	}
}

// 转发关闭只挡未知路径：已知端点照常进入 chat 流水线（空 model 报错而非 404/透传）。
func TestDedicatedRouteForwardDisabledKnownEndpointStaysAvailable(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	w := doDedicated(t, f, http.MethodPost, "/p/gamma-name/v1/messages", "Bearer sk-admin", `{}`)
	if w.Code == http.StatusNotFound {
		t.Fatalf("关闭转发不应影响已知端点: %d %s", w.Code, w.Body.String())
	}
	if w.Code == http.StatusOK {
		t.Fatalf("已知端点应进入 chat 流水线（空 model 报错），不应透传成功: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "model") {
		t.Errorf("错误信息应指向 model 校验: %s", w.Body.String())
	}
}

// 混合模式下未知路径的 DELETE 被方法白名单拦截。
func TestDedicatedRouteUnknownPathMethodWhitelist(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	w := doDedicated(t, f, http.MethodDelete, "/p/alpha-name/v1/files/1", "Bearer sk-admin", "")
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("混合模式未知路径 DELETE 应 405, got %d", w.Code)
	}
}

// 查无此提供商 404。
func TestDedicatedRouteUnknownProvider(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	w := doDedicated(t, f, http.MethodPost, "/p/no-such/v1/alpha/search", "Bearer sk-admin", `{}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("查无提供商应 404, got %d", w.Code)
	}
}

// 名称不满足 URL 安全字符的标识直接 404，不查库。
// （含空格等非法字符的原始 URL 无法构造请求行，字符级规则由
// TestIsSafeProviderSegment 直接覆盖。）
func TestDedicatedRouteUnsafeName(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	for _, name := range []string{"..", "a..b"} {
		w := doDedicated(t, f, http.MethodPost, "/p/"+name+"/v1/alpha/search", "Bearer sk-admin", `{}`)
		if w.Code != http.StatusNotFound {
			t.Fatalf("不安全名称 %q 应 404, got %d", name, w.Code)
		}
	}
}

// 提供商专属地址的名称标识只允许 URL 安全字符（字母/数字开头，
// 其余为字母、数字、-、_、.），从源头排除路径穿越与编码歧义。
func TestIsSafeProviderSegment(t *testing.T) {
	valid := []string{"a", "openai-1", "My_Provider.v2", "a-b_c.d9"}
	invalid := []string{"", "a b", "提", "a/b", ".a", "..", "-a", "_a", strings.Repeat("a", 65), "a\tb", "a\x00b"}
	for _, s := range valid {
		if !isSafeProviderSegment(s) {
			t.Errorf("%q 应为合法标识", s)
		}
	}
	for _, s := range invalid {
		if isSafeProviderSegment(s) {
			t.Errorf("%q 应为非法标识", s)
		}
	}
}

// 路径穿越在转发前被 400 拒绝。
func TestDedicatedRoutePathTraversalRejected(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	w := doDedicated(t, f, http.MethodGet, "/p/alpha-name/v1/../api/metrics/stats", "Bearer sk-admin", "")
	if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
		t.Fatalf("路径穿越应被拒绝(400/404), got %d: %s", w.Code, w.Body.String())
	}
}

// 透传请求落一条 passthrough 日志：模型名、提供商、状态、协议齐全，token 为空。
func TestDedicatedRoutePassthroughLog(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/alpha/search", "Bearer sk-admin",
		`{"model":"latest","commands":{}}`)

	logs, err := gorm.G[models.ChatLog](models.DB).Find(t.Context())
	if err != nil {
		t.Fatalf("query logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("应落一条日志, got %d", len(logs))
	}
	log := logs[0]
	if log.Style != consts.StylePassthrough {
		t.Errorf("Style = %q, want passthrough", log.Style)
	}
	if log.ProviderName != "alpha-name" || log.ProviderModel != "latest-direct" {
		t.Errorf("ProviderName/ProviderModel = %q/%q, want alpha-name/latest-direct", log.ProviderName, log.ProviderModel)
	}
	if log.Status != consts.StatusSuccess {
		t.Errorf("Status = %q, want success", log.Status)
	}
	if log.PromptTokens != 0 || log.CompletionTokens != 0 {
		t.Errorf("透传无 token 用量, got %+v", log.Usage)
	}
}

// patchProvider 就地覆盖提供商的转发列，模拟转发专页保存后的库内状态。
func patchProvider(t *testing.T, name string, values map[string]any) {
	t.Helper()
	result := models.DB.Model(&models.Provider{}).Where("name = ?", name).Updates(values)
	if result.Error != nil {
		t.Fatalf("patch provider %s: %v", name, result.Error)
	}
	if result.RowsAffected == 0 {
		t.Fatalf("patch provider %s: no row affected", name)
	}
}

// createLimitedKey 创建一个只允许 latest 的受限密钥，返回其明文。
func createLimitedKey(t *testing.T) string {
	t.Helper()
	key := models.AuthKey{Name: "limited", Key: "sk-limited", Status: new(true), AllowAll: new(false), Models: []string{"latest"}}
	if err := models.DB.Create(&key).Error; err != nil {
		t.Fatalf("create auth key: %v", err)
	}
	return key.Key
}

// 纯转发 + known 改写：直转所有路径，但认识且已绑定的模型名仍改写为上游名。
// 改写策略与转发模式正交，这是本次解耦的核心契约。
func TestDedicatedRoutePureModeRewritesKnownModel(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")
	patchProvider(t, "alpha-name", map[string]any{
		"forward_mode":    true,
		"forward_rewrite": consts.ForwardRewriteKnown,
	})

	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/chat/completions", "Bearer sk-admin",
		`{"model":"latest","messages":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("纯转发应 200, got %d: %s", w.Code, w.Body.String())
	}
	path, _, _, body := f.upstream.snapshot()
	if path != "/chat/completions" {
		t.Errorf("上游 path = %q, want /chat/completions", path)
	}
	if !strings.Contains(body, `"model":"latest-direct"`) {
		t.Errorf("known 策略应把绑定模型改写为 latest-direct, got %q", body)
	}
}

// 纯转发 + all 改写：无条件改写为固定目标模型，路径不变。
func TestDedicatedRoutePureModeFullRewrite(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")
	patchProvider(t, "alpha-name", map[string]any{
		"forward_mode":         true,
		"forward_rewrite":      consts.ForwardRewriteAll,
		"forward_target_model": "fixed-upstream",
	})

	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/chat/completions", "Bearer sk-admin",
		`{"model":"latest","messages":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("纯转发应 200, got %d: %s", w.Code, w.Body.String())
	}
	_, _, _, body := f.upstream.snapshot()
	if !strings.Contains(body, `"model":"fixed-upstream"`) {
		t.Errorf("all 策略应改写为固定目标, got %q", body)
	}
}

// all 改写同时覆盖 Gemini 风格路径里的模型名（/models/{id}:action）。
func TestDedicatedRouteFullRewriteRewritesGeminiPath(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")
	patchProvider(t, "alpha-name", map[string]any{
		"forward_mode":         true,
		"forward_rewrite":      consts.ForwardRewriteAll,
		"forward_target_model": "gemini-fixed",
	})

	w := doDedicated(t, f, http.MethodPost,
		"/p/alpha-name/v1beta/models/gemini-2.5-pro:generateContent", "Bearer sk-admin",
		`{"contents":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}
	path, _, _, _ := f.upstream.snapshot()
	if path != "/v1beta/models/gemini-fixed:generateContent" {
		t.Errorf("上游 path = %q, want /v1beta/models/gemini-fixed:generateContent", path)
	}
}

// 混合模式下 all 改写同样生效：未知路径透传前把 body 里的 model 换成固定目标。
func TestDedicatedRouteMixedModeFullRewrite(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")
	patchProvider(t, "alpha-name", map[string]any{
		"forward_rewrite":      consts.ForwardRewriteAll,
		"forward_target_model": "mixed-fixed",
	})

	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/alpha/search", "Bearer sk-admin",
		`{"model":"latest","commands":{}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}
	path, _, _, body := f.upstream.snapshot()
	if path != "/alpha/search" {
		t.Errorf("上游 path = %q, want /alpha/search", path)
	}
	if !strings.Contains(body, `"model":"mixed-fixed"`) {
		t.Errorf("all 策略应改写为固定目标, got %q", body)
	}
}

// all 改写绕过客户端模型名鉴权，因此受限密钥即使带 model 也必须 403。
func TestDedicatedRouteFullRewriteForbiddenForLimitedKey(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")
	limited := createLimitedKey(t)
	patchProvider(t, "alpha-name", map[string]any{
		"forward_rewrite":      consts.ForwardRewriteAll,
		"forward_target_model": "mixed-fixed",
	})

	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/alpha/search", "Bearer "+limited,
		`{"model":"latest"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("受限密钥 all 改写应 403, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "forward rewrite requires a token with full model permission") {
		t.Errorf("错误信息应指向全量权限, got %s", w.Body.String())
	}
}

// all 策略缺目标模型是配置缺失：500，且不触碰上游。
func TestDedicatedRouteFullRewriteWithoutTarget(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")
	patchProvider(t, "alpha-name", map[string]any{
		"forward_rewrite":      consts.ForwardRewriteAll,
		"forward_target_model": "",
	})

	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/alpha/search", "Bearer sk-admin",
		`{"model":"latest"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("缺目标模型应 500, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "forward target model is not configured") {
		t.Errorf("错误信息应指向缺目标模型, got %s", w.Body.String())
	}
	if path, _, _, _ := f.upstream.snapshot(); path != "" {
		t.Errorf("配置缺失不得触碰上游, 上游收到 %q", path)
	}
}

// 没有 slug 的提供商在专属地址上不可达（404），即使名称仍存在。
func TestDedicatedRouteWithoutSlugNotFound(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")
	patchProvider(t, "gamma-name", map[string]any{"forward_slug": nil})

	w := doDedicated(t, f, http.MethodPost, "/p/gamma-name/v1/chat/completions", "Bearer sk-admin",
		`{"model":"latest","messages":[]}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("无 slug 应 404, got %d: %s", w.Code, w.Body.String())
	}
}

// slug 与名称解耦：改名后旧地址（slug）继续可用，新名称不再等于地址。
func TestDedicatedRouteSlugIndependentOfName(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")
	patchProvider(t, "alpha-name", map[string]any{"name": "alpha-renamed"})

	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/alpha/search", "Bearer sk-admin",
		`{"model":"latest"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("改名后旧 slug 应仍可用, got %d: %s", w.Code, w.Body.String())
	}

	w = doDedicated(t, f, http.MethodPost, "/p/alpha-renamed/v1/alpha/search", "Bearer sk-admin",
		`{"model":"latest"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("新名称不是 slug, 应 404, got %d: %s", w.Code, w.Body.String())
	}
}

// 目标模型名带路径分隔符时只改 body，不动 URL 路径（避免把上游路径改写坏）。
func TestDedicatedRouteFullRewriteKeepsPathForUnsafeTarget(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")
	patchProvider(t, "alpha-name", map[string]any{
		"forward_mode":         true,
		"forward_rewrite":      consts.ForwardRewriteAll,
		"forward_target_model": "org/gemini-fixed",
	})

	w := doDedicated(t, f, http.MethodPost,
		"/p/alpha-name/v1beta/models/gemini-2.5-pro:generateContent", "Bearer sk-admin",
		`{"contents":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}
	path, _, _, _ := f.upstream.snapshot()
	if path != "/v1beta/models/gemini-2.5-pro:generateContent" {
		t.Errorf("不安全目标不应改写路径, got %q", path)
	}
}

// singleChatLog 取出唯一一条日志；相关用例都只发一个请求。
func singleChatLog(t *testing.T) models.ChatLog {
	t.Helper()
	logs, err := gorm.G[models.ChatLog](models.DB).Find(t.Context())
	if err != nil || len(logs) != 1 {
		t.Fatalf("应落一条日志, got %d, err=%v", len(logs), err)
	}
	return logs[0]
}

// singleChatIO 取出唯一一条 IO 行。
func singleChatIO(t *testing.T) models.ChatIO {
	t.Helper()
	records, err := gorm.G[models.ChatIO](models.DB).Find(t.Context())
	if err != nil || len(records) != 1 {
		t.Fatalf("应落一条 ChatIO, got %d, err=%v", len(records), err)
	}
	return records[0]
}

// countChatIO 数 IO 行，用来断言「不该记录」的情况。
func countChatIO(t *testing.T) int {
	t.Helper()
	records, err := gorm.G[models.ChatIO](models.DB).Find(t.Context())
	if err != nil {
		t.Fatalf("查 ChatIO 失败: %v", err)
	}
	return len(records)
}

// 透传日志从「只有元数据」升级成可追溯的请求记录：trace、UA、IP、请求路径、
// 首字节耗时、IO 行都要有；管理 TOKEN 默认记录 IO。
func TestDedicatedRouteForwardLogsRequestAndResponseIO(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")
	// 假上游延迟 100ms：首字节耗时/代理耗时必须稳定为正，不受时钟精度影响。
	f.upstream.setResponseDelay(100 * time.Millisecond)

	requestBody := `{"model":"latest","commands":{}}`
	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/alpha/search", "Bearer sk-admin", requestBody)
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}

	log := singleChatLog(t)
	if log.TraceID == "" {
		t.Error("TraceID 不应为空")
	}
	if log.UserAgent != "llmio-test/1.0" {
		t.Errorf("UserAgent = %q, want llmio-test/1.0", log.UserAgent)
	}
	if log.RemoteIP == "" {
		t.Error("RemoteIP 不应为空")
	}
	if log.RequestPath != "POST /p/alpha-name/v1/alpha/search" {
		t.Errorf("RequestPath = %q, want POST /p/alpha-name/v1/alpha/search", log.RequestPath)
	}
	if !log.ChatIO {
		t.Error("管理 TOKEN 默认记录 IO，ChatIO 应为 true")
	}
	if log.Size == 0 || log.ProxyTime <= 0 {
		t.Errorf("大小/耗时应记录, size=%d proxy=%v", log.Size, log.ProxyTime)
	}
	if log.FirstChunkTime <= 0 || log.ChunkTime < 0 {
		t.Errorf("首字节耗时应记录, first=%v chunk=%v", log.FirstChunkTime, log.ChunkTime)
	}

	io := singleChatIO(t)
	if io.LogId != log.ID {
		t.Errorf("ChatIO.LogId = %d, want %d", io.LogId, log.ID)
	}
	if string(io.Input) != requestBody {
		t.Errorf("ChatIO.Input = %q, want 原样请求体", string(io.Input))
	}
	if io.OfString != `{"output":"ok"}` || len(io.OfStringArray) != 0 {
		t.Errorf("ChatIO 输出应原样 JSON, got %q / %v", io.OfString, io.OfStringArray)
	}
}

// 非流式响应里的 usage 解析进 ChatLog，并算出 TPS。
func TestDedicatedRouteForwardLogsUsageAndTps(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")
	response := `{"output":"ok","usage":{"prompt_tokens":12,"completion_tokens":34,"total_tokens":46,"prompt_tokens_details":{"cached_tokens":5}}}`
	f.upstream.setResponse(response, "application/json", http.StatusOK)
	// Windows 上 time.Since 可能量出 0（量级小于时钟粒度），而 TPS 只在
	// ProxyTime > 0 时计算，会让这个断言随机为 0。给上游 2ms 延迟，让被测时长
	// 远大于粒度，同时不改变断言意图。
	f.upstream.setResponseDelay(2 * time.Millisecond)

	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/alpha/search", "Bearer sk-admin", `{"model":"latest"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}

	log := singleChatLog(t)
	if log.PromptTokens != 12 || log.CompletionTokens != 34 || log.TotalTokens != 46 {
		t.Errorf("usage = %d/%d/%d, want 12/34/46", log.PromptTokens, log.CompletionTokens, log.TotalTokens)
	}
	if log.PromptTokensDetails.CachedTokens != 5 {
		t.Errorf("cached tokens = %d, want 5", log.PromptTokensDetails.CachedTokens)
	}
	if log.Tps <= 0 {
		t.Errorf("TPS 应大于 0, got %v", log.Tps)
	}
}

// SSE 透传：输出拆成事件数组（剥掉 data: 前缀与 [DONE]），usage 取最后一条。
func TestDedicatedRouteForwardLogsStreamingIO(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n" +
		"data: [DONE]\n\n"
	f.upstream.setResponse(stream, "text/event-stream", http.StatusOK)

	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/alpha/stream", "Bearer sk-admin", `{"model":"latest"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}
	// SSE 必须显式关掉中间层的缓冲/缓存，否则 nginx 之类的反代会攒满整条流才吐。
	if got := w.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("SSE 应带 X-Accel-Buffering: no, got %q", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("SSE 应带 Cache-Control: no-cache, got %q", got)
	}

	log := singleChatLog(t)
	if log.PromptTokens != 3 || log.CompletionTokens != 2 || log.TotalTokens != 5 {
		t.Errorf("SSE usage = %d/%d/%d, want 3/2/5", log.PromptTokens, log.CompletionTokens, log.TotalTokens)
	}

	io := singleChatIO(t)
	if len(io.OfStringArray) != 2 {
		t.Fatalf("SSE 输出应为 2 个事件（[DONE] 跳过）, got %d: %v", len(io.OfStringArray), io.OfStringArray)
	}
	if !strings.Contains(io.OfStringArray[1], "\"usage\"") {
		t.Errorf("最后一个事件应含 usage, got %q", io.OfStringArray[1])
	}
	if io.OfString != "" {
		t.Errorf("SSE 不应同时写 OfString, got %q", io.OfString)
	}
}

// 受限密钥没开 ioLog：不记录请求/响应体，也不产生 ChatIO 行，只留元数据。
func TestDedicatedRouteForwardLogsSkippedForPlainKey(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")
	limited := createLimitedKey(t)

	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/alpha/search", "Bearer "+limited, `{"model":"latest"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}

	log := singleChatLog(t)
	if log.AuthKeyID == 0 {
		t.Error("受限密钥的日志应带 AuthKeyID")
	}
	if log.ChatIO {
		t.Error("密钥未开 ioLog，ChatIO 应为 false")
	}
	if n := countChatIO(t); n != 0 {
		t.Fatalf("不应落 ChatIO, got %d", n)
	}
}

// 密钥开了 ioLog：记录 IO，且日志归属该密钥。
func TestDedicatedRouteForwardLogsIOForIOLogKey(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")
	key := models.AuthKey{Name: "io", Key: "sk-io", Status: new(true), AllowAll: new(true), IOLog: new(true)}
	if err := models.DB.Create(&key).Error; err != nil {
		t.Fatalf("创建密钥失败: %v", err)
	}

	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/alpha/search", "Bearer sk-io", `{"model":"latest"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}

	log := singleChatLog(t)
	if log.AuthKeyID != key.ID {
		t.Errorf("AuthKeyID = %d, want %d", log.AuthKeyID, key.ID)
	}
	if !log.ChatIO {
		t.Error("密钥开了 ioLog，ChatIO 应为 true")
	}
	io := singleChatIO(t)
	if io.LogId != log.ID {
		t.Errorf("ChatIO.LogId = %d, want %d", io.LogId, log.ID)
	}
}

// 纯转发的日志同样带请求路径；纯转发不改写，模型名就是请求体里的值。
func TestDedicatedRouteForwardAllLogsRequestPath(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	w := doDedicated(t, f, http.MethodPost, "/p/beta-name/v1/embeddings", "Bearer sk-admin", `{"model":"text-embedding-3-small","input":"x"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}

	log := singleChatLog(t)
	if log.RequestPath != "POST /p/beta-name/v1/embeddings" {
		t.Errorf("RequestPath = %q, want POST /p/beta-name/v1/embeddings", log.RequestPath)
	}
	if log.Name != "text-embedding-3-small" || log.ProviderModel != "text-embedding-3-small" {
		t.Errorf("模型名 = %q/%q, want 均为 text-embedding-3-small", log.Name, log.ProviderModel)
	}
	if !log.ChatIO {
		t.Error("管理 TOKEN 默认记录 IO")
	}
}

// 回归 B1：Gemini 的 base_url 自带 /v1beta，客户端路径也按 UI 写 /v1beta，不能拼成
// /v1beta/v1beta/...。用假上游做端到端断言，而不只是单测归一化函数。
// 这里用非对话方法 :embedContent——本地端点只实现两种对话方法，其余动作在混合
// 模式下仍走透传（对话方法的本地化见 TestDedicatedRouteGeminiNativeChat*）。
func TestDedicatedRouteGeminiVersionSegmentNotDoubled(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")
	patchProvider(t, "alpha-name", map[string]any{
		"type":   "gemini",
		"config": `{"base_url":"` + f.serverURL + `/v1beta","api_key":"sk-gemini"}`,
	})

	w := doDedicated(t, f, http.MethodPost,
		"/p/alpha-name/v1beta/models/gemini-2.5-pro:embedContent", "Bearer sk-admin",
		`{"contents":[{"parts":[{"text":"hi"}]}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}

	path, _, _, _ := f.upstream.snapshot()
	if path != "/v1beta/models/gemini-2.5-pro:embedContent" {
		t.Fatalf("上游 path = %q, want /v1beta/models/gemini-2.5-pro:embedContent（不能出现 /v1beta/v1beta）", path)
	}
	if got := f.upstream.sentHeader("X-Goog-Api-Key"); got != "sk-gemini" {
		t.Errorf("gemini 鉴权头应使用提供商自己的 key, got %q", got)
	}
}

// 回归：Anthropic 流式的 usage 分散在 message_start（输入，且嵌在 message 下）与
// message_delta（输出，顶层）两个事件里；只认顶层 usage 会让 prompt 一直是 0。
func TestDedicatedRouteAnthropicStreamUsageFromMessageStart(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")
	patchProvider(t, "alpha-name", map[string]any{"type": "anthropic"})
	stream := "event: message_start\n" +
		`data: {"type":"message_start","message":{"usage":{"input_tokens":25,"cache_read_input_tokens":0}}}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","usage":{"output_tokens":15}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"
	f.upstream.setResponse(stream, "text/event-stream", http.StatusOK)

	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/alpha/stream", "Bearer sk-admin",
		`{"model":"latest","stream":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}

	log := singleChatLog(t)
	if log.PromptTokens != 25 {
		t.Errorf("prompt_tokens = %d, want 25（来自 message_start.message.usage）", log.PromptTokens)
	}
	if log.CompletionTokens != 15 {
		t.Errorf("completion_tokens = %d, want 15（来自 message_delta.usage）", log.CompletionTokens)
	}
	if log.TotalTokens != 40 {
		t.Errorf("total_tokens = %d, want 40", log.TotalTokens)
	}
}

// 头转发：上游特性头（anthropic-beta、x-*）必须能到上游；客户端凭据头不能串味，
// 提供商的鉴权头由 setAuthHeaders 覆盖。
func TestDedicatedRouteForwardsClientHeaders(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	// 用 Authorization 完成 /p 鉴权，同时带上游特性头。
	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/alpha/search", "Bearer sk-admin", `{"model":"latest"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := f.upstream.sentHeader("Anthropic-Beta"); got != "" {
		t.Errorf("未发送的 anthropic-beta 不应凭空出现, got %q", got)
	}

	// 显式设置头再打一次（doDedicated 只支持固定头，这里自己构造请求）。
	req := httptest.NewRequest(http.MethodPost, "/p/alpha-name/v1/alpha/search", strings.NewReader(`{"model":"latest"}`))
	req.Header.Set("Authorization", "Bearer sk-admin")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Anthropic-Beta", "feature-2025-01-01")
	req.Header.Set("X-Custom-Trace", "trace-123")
	req.Header.Set("Cookie", "session=client")
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if got := f.upstream.sentHeader("Anthropic-Beta"); got != "feature-2025-01-01" {
		t.Errorf("anthropic-beta 应透传到上游, got %q", got)
	}
	if got := f.upstream.sentHeader("X-Custom-Trace"); got != "trace-123" {
		t.Errorf("自定义 x-* 头应透传到上游, got %q", got)
	}
	if got := f.upstream.sentHeader("Cookie"); got != "" {
		t.Errorf("客户端 Cookie 不得透传到上游, got %q", got)
	}
	if _, auth, _, _ := f.upstream.snapshot(); auth != "Bearer sk-upstream" {
		t.Errorf("上游 Authorization 应是提供商自己的 key, got %q", auth)
	}

	// x-api-key 本身也能鉴权 /p（Gemini 风格客户端）；它优先于 Authorization，所以上面
	// 那个请求不能同时带客户端 x-api-key（会被当成鉴权凭据而 401）。这里单独验证：
	// 用 sk-admin 通过 x-api-key 鉴权时，客户端 key 同样不会出现在上游请求里。
	req2 := httptest.NewRequest(http.MethodPost, "/p/alpha-name/v1/alpha/search", strings.NewReader(`{"model":"latest"}`))
	req2.Header.Set("x-api-key", "sk-admin")
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	f.router.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("x-api-key 鉴权应 200, got %d: %s", rec2.Code, rec2.Body.String())
	}
	if got := f.upstream.sentHeader("X-Api-Key"); got == "sk-admin" {
		t.Errorf("客户端 x-api-key（admin）不得透传到上游, got %q", got)
	}
}

// query 里的凭据参数转发前必须删掉，但 Gemini 的 alt=sse 这类协议参数要保留。
func TestDedicatedRouteStripsCredentialQuery(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	w := doDedicated(t, f, http.MethodPost,
		"/p/alpha-name/v1/alpha/search?key=leak&alt=sse", "Bearer sk-admin", `{"model":"latest"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := f.upstream.sentRawQuery(); got != "alt=sse" {
		t.Errorf("上游 query = %q, want 仅 alt=sse（key 凭据必须剥掉）", got)
	}
}

// 请求体超过 64 MiB 上限时直接 413，不读取整段、也不触碰上游。
func TestDedicatedRouteBodyTooLarge(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	big := strings.Repeat("a", maxForwardBodyBytes+1)
	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/alpha/search", "Bearer sk-admin", big)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超限请求应 413, got %d: %s", w.Code, w.Body.String())
	}
	if path, _, _, _ := f.upstream.snapshot(); path != "" {
		t.Errorf("超限请求不得触碰上游, 上游收到 %q", path)
	}
}

// 上游传输错误的细节只进日志，响应给客户端通用文案（不回显内部地址）。
func TestDedicatedRouteUpstreamErrorHidesDetails(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")
	// 127.0.0.1:1 基本不可能有服务监听，连接会立刻被拒。
	patchProvider(t, "alpha-name", map[string]any{
		"config": `{"base_url":"http://127.0.0.1:1","api_key":"sk-upstream"}`,
	})

	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/alpha/search", "Bearer sk-admin", `{"model":"latest"}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("上游连接失败应 502, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "upstream request failed") {
		t.Errorf("应返回通用文案, got %s", body)
	}
	for _, leak := range []string{"127.0.0.1", "dial", "connection refused", "connectex"} {
		if strings.Contains(body, leak) {
			t.Errorf("响应不得回显上游传输错误细节 %q, got %s", leak, body)
		}
	}
}

// Gemini 地址（关闭模式）上，GET /v1beta/models 是本地端点：返回本服务登记过的
// Gemini 模型，不触碰上游。这是本次改动最直接的行为变化——它以前在关闭模式下 404。
func TestDedicatedRouteGeminiNativeModelsList(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	w := doDedicated(t, f, http.MethodGet, "/p/epsilon-name/v1beta/models", "Bearer sk-admin", "")
	if w.Code != http.StatusOK {
		t.Fatalf("Gemini 地址的模型列表应 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "models/gemini-native") {
		t.Errorf("应列出本服务的 Gemini 模型, got %s", w.Body.String())
	}
	if path, _, _, _ := f.upstream.snapshot(); path != "" {
		t.Errorf("本地端点不得触碰上游, 上游收到 %q", path)
	}
}

// Gemini 原生对话在关闭模式下也走本地流水线（全池负载均衡）：上游收到的是绑定
// 关系上的上游模型名，日志的 Style 是 gemini 而不是 passthrough。
func TestDedicatedRouteGeminiNativeChatGoesLocalPipeline(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	w := doDedicated(t, f, http.MethodPost,
		"/p/epsilon-name/v1beta/models/gemini-native:generateContent", "Bearer sk-admin",
		`{"contents":[{"parts":[{"text":"hi"}]}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}

	path, _, _, _ := f.upstream.snapshot()
	if path != "/v1beta/models/gemini-native-direct:generateContent" {
		t.Fatalf("上游 path = %q, want /v1beta/models/gemini-native-direct:generateContent（本地流水线按绑定换名）", path)
	}
	log := singleChatLog(t)
	if log.Style != consts.StyleGemini {
		t.Errorf("Style = %q, want gemini", log.Style)
	}
	if log.ProviderName != "epsilon-name" {
		t.Errorf("ProviderName = %q, want epsilon-name", log.ProviderName)
	}
}

// :streamGenerateContent 同样走本地流水线，并由 Gemini 适配器补上 ?alt=sse。
func TestDedicatedRouteGeminiNativeStreamGoesLocalPipeline(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")
	f.upstream.setResponse("event: message\n\n", "text/event-stream", http.StatusOK)

	w := doDedicated(t, f, http.MethodPost,
		"/p/epsilon-name/v1beta/models/gemini-native:streamGenerateContent?alt=sse", "Bearer sk-admin",
		`{"contents":[{"parts":[{"text":"hi"}]}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}

	path, _, _, _ := f.upstream.snapshot()
	if path != "/v1beta/models/gemini-native-direct:streamGenerateContent" {
		t.Fatalf("上游 path = %q, want /v1beta/models/gemini-native-direct:streamGenerateContent", path)
	}
	if got := f.upstream.sentRawQuery(); got != "alt=sse" {
		t.Errorf("上游 query = %q, want alt=sse", got)
	}
	if log := singleChatLog(t); log.Style != consts.StyleGemini {
		t.Errorf("Style = %q, want gemini", log.Style)
	}
}

// 混合模式的 Gemini 地址：原生对话端点同样走本地流水线，不再透传给上游。
func TestDedicatedRouteMixedModeGeminiNativeGoesLocal(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	w := doDedicated(t, f, http.MethodPost,
		"/p/eta-name/v1beta/models/gemini-mixed:generateContent", "Bearer sk-admin",
		`{"contents":[{"parts":[{"text":"hi"}]}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}

	path, _, _, _ := f.upstream.snapshot()
	if path != "/v1beta/models/gemini-mixed-direct:generateContent" {
		t.Fatalf("上游 path = %q, want /v1beta/models/gemini-mixed-direct:generateContent", path)
	}
	log := singleChatLog(t)
	if log.Style != consts.StyleGemini {
		t.Errorf("Style = %q, want gemini（混合模式的原生对话应走本地流水线）", log.Style)
	}
	if log.ProviderName != "eta-name" {
		t.Errorf("ProviderName = %q, want eta-name", log.ProviderName)
	}
}

// 关闭模式只服务本地端点：其余路径一律 404，且不得触碰上游。三条分别是
// OpenAI 型地址的 embedding、非 Gemini 地址上的原生路径、Gemini 地址上的
// 非对话方法（:embedContent 没有本地实现）。
func TestDedicatedRouteOffModeKeepsNonLocalEndpoints404(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	targets := []string{
		"/p/gamma-name/v1/embeddings",
		"/p/gamma-name/v1beta/models/x:generateContent",
		"/p/epsilon-name/v1beta/models/gemini-native:embedContent",
	}
	for _, target := range targets {
		w := doDedicated(t, f, http.MethodPost, target, "Bearer sk-admin", `{"model":"latest","input":"x"}`)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s 关闭模式应 404, got %d: %s", target, w.Code, w.Body.String())
		}
	}
	if path, _, _, _ := f.upstream.snapshot(); path != "" {
		t.Errorf("关闭模式不得触碰上游, 上游收到 %q", path)
	}
}

// 混合模式下 embedding 属于"其余路径"，照旧透传给指名的提供商，路径经版本段
// 归一化后上游收到 /embeddings（alpha 的 base_url 没有版本段）。
func TestDedicatedRouteMixedModeForwardsEmbeddings(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")
	// 关掉模型名改写，断言透传的就是客户端原文；改写本身是别的用例的契约。
	patchProvider(t, "alpha-name", map[string]any{"forward_rewrite": consts.ForwardRewriteNone})
	body := `{"model":"latest","input":"hello"}`

	w := doDedicated(t, f, http.MethodPost, "/p/alpha-name/v1/embeddings", "Bearer sk-admin", body)
	if w.Code != http.StatusOK {
		t.Fatalf("混合模式 embedding 应透传成功, got %d: %s", w.Code, w.Body.String())
	}
	path, _, _, sent := f.upstream.snapshot()
	if path != "/embeddings" {
		t.Errorf("上游 path = %q, want /embeddings", path)
	}
	if sent != body {
		t.Errorf("透传应保留原始请求体, got %q", sent)
	}
}

// GET /v1/models 的返回形状按请求里的协议判断：anthropic-version 头优先，
// 没有头时按地址协议——Anthropic 型地址给 Anthropic 形状，其余给 OpenAI 形状。
func TestDedicatedRouteModelsShapeFollowsProtocol(t *testing.T) {
	f := setupDedicatedRoute(t, "sk-admin")

	// OpenAI 型地址 + 无协议头 → OpenAI 形状
	w := doDedicated(t, f, http.MethodGet, "/p/alpha-name/v1/models", "Bearer sk-admin", "")
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"owned_by":"llmio"`) {
		t.Errorf("OpenAI 形状应带 owned_by, got %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "has_more") {
		t.Errorf("OpenAI 形状不应带 has_more, got %s", w.Body.String())
	}

	// 同一个 OpenAI 型地址 + anthropic-version 头 → Anthropic 形状
	req := httptest.NewRequest(http.MethodGet, "/p/alpha-name/v1/models", nil)
	req.Header.Set("User-Agent", "llmio-test/1.0")
	req.Header.Set("Authorization", "Bearer sk-admin")
	req.Header.Set("anthropic-version", "2023-06-01")
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"has_more":false`) {
		t.Errorf("带 anthropic-version 头应返回 Anthropic 形状, got %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "owned_by") {
		t.Errorf("Anthropic 形状不应带 owned_by, got %s", rec.Body.String())
	}

	// Anthropic 型地址 + 无协议头 → 也按地址协议给 Anthropic 形状，且模型名对得上
	patchProvider(t, "alpha-name", map[string]any{"type": "anthropic"})
	w = doDedicated(t, f, http.MethodGet, "/p/alpha-name/v1/models", "Bearer sk-admin", "")
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"has_more":false`) {
		t.Errorf("Anthropic 型地址应返回 Anthropic 形状, got %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"id":"latest"`) {
		t.Errorf("应列出该 Anthropic 地址可服务的模型, got %s", w.Body.String())
	}
}
