package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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
 * 钉住四类契约：
 *  1. 鉴权：路由组挂着 authOpenAI，无凭据 401；受限密钥对无法定位模型的
 *     透传/纯转发请求 403（专属地址不允许成为绕过模型权限的后门）。
 *  2. 兼容：全局 /v1 的未知路径照旧 404（不引入全局透传），而专属地址的
 *     已知端点走与 /v1 相同的 handler（行为一致，不钉死提供商）。
 *  3. 透传：未知路径固定转给 URL 指名的提供商，绑定命中时改写模型名，
 *     未绑定原样；模型名不安全（路径穿越、控制字符）一律拒绝。
 *  4. 记账：透传请求落一条 Style=passthrough 的日志，token 为空。
 */

// dedicatedUpstream 记录假上游收到的请求与 body，供断言。
type dedicatedUpstream struct {
	mu     sync.Mutex
	path   string
	auth   string
	accept string
	body   string
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
		u.body = body
		u.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"output":"ok"}`))
	}
}

func (u *dedicatedUpstream) snapshot() (path, auth, accept, body string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.path, u.auth, u.accept, u.body
}

type dedicatedFixture struct {
	router   *gin.Engine
	upstream *dedicatedUpstream
}

// setupDedicatedRoute 搭建包含专属地址路由的 gin 引擎、数据库（两家提供商：
// alpha-name 可绑定、beta-name 纯转发），以及指向 alpha-name 的假上游。
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
	if err := db.AutoMigrate(&models.Provider{}, &models.Model{}, &models.ModelWithProvider{}, &models.ChatLog{}, &models.AuthKey{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}

	alpha := models.Provider{
		Name:   "alpha-name",
		Type:   "openai",
		Config: `{"base_url":"` + server.URL + `","api_key":"sk-upstream"}`,
	}
	beta := models.Provider{
		Name:        "beta-name",
		Type:        "openai",
		Config:      `{"base_url":"` + server.URL + `","api_key":"sk-upstream"}`,
		ForwardMode: true,
	}
	// 逐个直接 Create：循环变量副本会让 GORM 把自增 ID 写进副本而非 alpha/beta
	if err := db.Create(&alpha).Error; err != nil {
		t.Fatalf("create provider alpha: %v", err)
	}
	if err := db.Create(&beta).Error; err != nil {
		t.Fatalf("create provider beta: %v", err)
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

	prev := models.DB
	models.DB = db
	t.Cleanup(func() {
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
		p.Any("/:name/*path", ProviderDedicatedRoute)
	}

	return &dedicatedFixture{router: router, upstream: upstream}
}

// testAuth 直接复用 middleware.AuthOpenAI：真实实现保证测试与线上一致。
func testAuth(adminToken string) gin.HandlerFunc {
	return middleware.AuthOpenAI(adminToken)
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
	if path != "/v1/chat/completions" {
		t.Errorf("上游 path = %q, want /v1/chat/completions", path)
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
