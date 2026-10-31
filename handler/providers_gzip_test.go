package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type gzipProbeUpstream struct {
	mu          sync.Mutex
	encodings   []string
	gzipStatus  int
	plainStatus int
	contentType string
}

func (u *gzipProbeUpstream) handler(w http.ResponseWriter, r *http.Request) {
	encoding := r.Header.Get("Content-Encoding")
	u.mu.Lock()
	u.encodings = append(u.encodings, encoding)
	u.mu.Unlock()

	status := u.plainStatus
	if encoding == "gzip" {
		status = u.gzipStatus
	}
	if status == 0 {
		status = http.StatusOK
	}
	contentType := u.contentType
	if contentType == "" {
		contentType = "application/json"
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	if status >= 200 && status < 300 {
		_, _ = w.Write([]byte(`{"id":"ok"}`))
		return
	}
	_, _ = w.Write([]byte(`{"error":{"message":"rejected by mock upstream"}}`))
}

func (u *gzipProbeUpstream) requests() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.encodings...)
}

func setupGzipProbeDB(t *testing.T, baseURL string) (models.Provider, models.ModelWithProvider) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gzip-probe.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.Provider{}, &models.Model{}, &models.ModelWithProvider{}, &models.ChatLog{}, &models.Config{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}

	provider := models.Provider{
		Name:   "probe-prov",
		Type:   consts.StyleOpenAI,
		Config: fmt.Sprintf(`{"base_url":%q,"api_key":"sk-test"}`, baseURL),
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model := models.Model{Name: "probe-model", MaxRetry: 1, TimeOut: 5, Strategy: consts.BalancerDefault}
	if err := db.Create(&model).Error; err != nil {
		t.Fatalf("create model: %v", err)
	}
	enabled := true
	assoc := models.ModelWithProvider{
		ModelID:       model.ID,
		ProviderID:    provider.ID,
		ProviderModel: "upstream-model",
		Status:        &enabled,
		Weight:        1,
	}
	if err := db.Create(&assoc).Error; err != nil {
		t.Fatalf("create assoc: %v", err)
	}
	if err := db.Create(&models.ChatLog{
		Name:          model.Name,
		ProviderName:  provider.Name,
		ProviderModel: assoc.ProviderModel,
		Status:        string(consts.StatusSuccess),
	}).Error; err != nil {
		t.Fatalf("create success log: %v", err)
	}

	prev := models.DB
	models.DB = db
	t.Cleanup(func() {
		models.DB = prev
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return provider, assoc
}

func gzipProbeCtx(t *testing.T, method, path, body string, providerID uint) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(uint64(providerID), 10)}}
	return c, rec
}

type probeEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type probeResult struct {
	Status          string          `json:"status"`
	HTTPStatus      int             `json:"http_status"`
	Detail          string          `json:"detail"`
	ModelProviderID uint            `json:"model_provider_id"`
	Provider        models.Provider `json:"provider"`
}

func decodeProbeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) probeEnvelope {
	t.Helper()
	var env probeEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v, body=%s", err, rec.Body.String())
	}
	return env
}

func TestGetProviderGzipAssociations(t *testing.T) {
	up := &gzipProbeUpstream{}
	server := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(server.Close)
	provider, assoc := setupGzipProbeDB(t, server.URL)

	c, rec := gzipProbeCtx(t, http.MethodGet, "/api/providers/gzip-probe/1/associations", "", provider.ID)
	GetProviderGzipAssociations(c)
	env := decodeProbeEnvelope(t, rec)
	if env.Code != http.StatusOK {
		t.Fatalf("code = %d, message=%s", env.Code, env.Message)
	}
	var data struct {
		SuggestedID uint `json:"suggested_id"`
		Items       []struct {
			ID            uint       `json:"id"`
			Model         string     `json:"model"`
			ProviderModel string     `json:"provider_model"`
			LastSuccessAt *time.Time `json:"last_success_at"`
		} `json:"items"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if data.SuggestedID != assoc.ID {
		t.Fatalf("suggested_id = %d, want %d", data.SuggestedID, assoc.ID)
	}
	if len(data.Items) != 1 || data.Items[0].Model != "probe-model" || data.Items[0].ProviderModel != "upstream-model" {
		t.Fatalf("items = %+v", data.Items)
	}
	if data.Items[0].LastSuccessAt == nil {
		t.Fatal("最近成功时间应存在")
	}
}

func TestProbeProviderGzipSupportedAndEnable(t *testing.T) {
	up := &gzipProbeUpstream{gzipStatus: http.StatusOK, plainStatus: http.StatusOK}
	server := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(server.Close)
	provider, assoc := setupGzipProbeDB(t, server.URL)

	c, rec := gzipProbeCtx(t, http.MethodPost, "/api/providers/1/gzip-probe", `{}`, provider.ID)
	ProbeProviderGzip(c)
	env := decodeProbeEnvelope(t, rec)
	if env.Code != http.StatusOK {
		t.Fatalf("probe code = %d, message=%s", env.Code, env.Message)
	}
	var result probeResult
	if err := json.Unmarshal(env.Data, &result); err != nil {
		t.Fatalf("decode probe result: %v", err)
	}
	if result.Status != gzipProbeSupported {
		t.Fatalf("status = %q, want supported, detail=%s", result.Status, result.Detail)
	}
	if result.ModelProviderID != assoc.ID {
		t.Fatalf("model_provider_id = %d, want %d", result.ModelProviderID, assoc.ID)
	}
	if result.Provider.RequestGzipSupported == nil || !*result.Provider.RequestGzipSupported {
		t.Fatal("支持状态应写入 true")
	}
	if result.Provider.RequestGzipEnabled {
		t.Fatal("探测成功不应自动打开提供商开关")
	}
	if result.Provider.RequestGzipProbeHash == "" {
		t.Fatal("探测成功应写入配置指纹")
	}
	if got := up.requests(); len(got) != 1 || got[0] != "gzip" {
		t.Fatalf("探测请求 = %v, want [gzip]", got)
	}

	c, rec = gzipProbeCtx(t, http.MethodPatch, "/api/providers/1/request-gzip", `{"enabled":true}`, provider.ID)
	UpdateProviderRequestGzip(c)
	env = decodeProbeEnvelope(t, rec)
	if env.Code != http.StatusOK {
		t.Fatalf("enable code = %d, message=%s", env.Code, env.Message)
	}
	var updated models.Provider
	if err := json.Unmarshal(env.Data, &updated); err != nil {
		t.Fatalf("decode provider: %v", err)
	}
	if !updated.RequestGzipEnabled {
		t.Fatal("开关应写入 true")
	}
}

func TestProbeProviderGzipUnsupported(t *testing.T) {
	up := &gzipProbeUpstream{gzipStatus: http.StatusBadRequest, plainStatus: http.StatusOK}
	server := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(server.Close)
	provider, _ := setupGzipProbeDB(t, server.URL)

	c, rec := gzipProbeCtx(t, http.MethodPost, "/api/providers/1/gzip-probe", `{}`, provider.ID)
	ProbeProviderGzip(c)
	env := decodeProbeEnvelope(t, rec)
	if env.Code != http.StatusOK {
		t.Fatalf("probe code = %d, message=%s", env.Code, env.Message)
	}
	var result probeResult
	if err := json.Unmarshal(env.Data, &result); err != nil {
		t.Fatalf("decode probe result: %v", err)
	}
	if result.Status != gzipProbeUnsupported {
		t.Fatalf("status = %q, want unsupported, detail=%s", result.Status, result.Detail)
	}
	if result.Provider.RequestGzipSupported == nil || *result.Provider.RequestGzipSupported {
		t.Fatal("不支持应写入 false")
	}
	if result.Provider.RequestGzipEnabled {
		t.Fatal("不支持时提供商开关必须关闭")
	}
	if got := up.requests(); len(got) != 2 || got[0] != "gzip" || got[1] != "" {
		t.Fatalf("探测请求 = %v, want [gzip plaintext]", got)
	}

	c, rec = gzipProbeCtx(t, http.MethodPatch, "/api/providers/1/request-gzip", `{"enabled":true}`, provider.ID)
	UpdateProviderRequestGzip(c)
	env = decodeProbeEnvelope(t, rec)
	if env.Code != http.StatusConflict {
		t.Fatalf("不支持时开启应返回 409，got %d", env.Code)
	}
}

func TestProbeProviderGzipInconclusiveKeepsPreviousSupport(t *testing.T) {
	up := &gzipProbeUpstream{gzipStatus: http.StatusUnauthorized, plainStatus: http.StatusOK}
	server := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(server.Close)
	provider, _ := setupGzipProbeDB(t, server.URL)

	if err := models.DB.Model(&models.Provider{}).Where("id = ?", provider.ID).Updates(map[string]any{
		"request_gzip_supported":  true,
		"request_gzip_probe_hash": service.ProviderGzipHash(provider),
	}).Error; err != nil {
		t.Fatalf("seed previous support: %v", err)
	}

	c, rec := gzipProbeCtx(t, http.MethodPost, "/api/providers/1/gzip-probe", `{}`, provider.ID)
	ProbeProviderGzip(c)
	env := decodeProbeEnvelope(t, rec)
	if env.Code != http.StatusOK {
		t.Fatalf("probe code = %d, message=%s", env.Code, env.Message)
	}
	var result probeResult
	if err := json.Unmarshal(env.Data, &result); err != nil {
		t.Fatalf("decode probe result: %v", err)
	}
	if result.Status != gzipProbeInconclusive {
		t.Fatalf("status = %q, want inconclusive, detail=%s", result.Status, result.Detail)
	}
	if result.Provider.RequestGzipSupported == nil || !*result.Provider.RequestGzipSupported {
		t.Fatal("无法判定不应覆盖之前的支持结论")
	}
	if result.Provider.RequestGzipProbedAt == nil {
		t.Fatal("无法判定也要更新检测时间")
	}
	if got := up.requests(); len(got) != 1 || got[0] != "gzip" {
		t.Fatalf("401 不应发明文对照，requests = %v", got)
	}
}

func TestProbeProviderGzipHTMLIsInconclusive(t *testing.T) {
	up := &gzipProbeUpstream{gzipStatus: http.StatusOK, plainStatus: http.StatusOK, contentType: "text/html; charset=utf-8"}
	server := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(server.Close)
	provider, _ := setupGzipProbeDB(t, server.URL)

	c, rec := gzipProbeCtx(t, http.MethodPost, "/api/providers/1/gzip-probe", `{}`, provider.ID)
	ProbeProviderGzip(c)
	env := decodeProbeEnvelope(t, rec)
	if env.Code != http.StatusOK {
		t.Fatalf("probe code = %d, message=%s", env.Code, env.Message)
	}
	var result probeResult
	if err := json.Unmarshal(env.Data, &result); err != nil {
		t.Fatalf("decode probe result: %v", err)
	}
	if result.Status != gzipProbeInconclusive {
		t.Fatalf("status = %q, want inconclusive", result.Status)
	}
	if result.Provider.RequestGzipSupported != nil {
		t.Fatal("HTML 响应不应写入支持结论")
	}
}

func TestUpdateProviderResetsGzipCapabilityOnEndpointChange(t *testing.T) {
	up := &gzipProbeUpstream{}
	server := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(server.Close)
	provider, _ := setupGzipProbeDB(t, server.URL)

	probedAt := time.Now()
	if err := models.DB.Model(&models.Provider{}).Where("id = ?", provider.ID).Updates(map[string]any{
		"request_gzip_enabled":      true,
		"request_gzip_supported":    true,
		"request_gzip_probed_at":    probedAt,
		"request_gzip_probe_detail": "HTTP 200",
		"request_gzip_probe_hash":   service.ProviderGzipHash(provider),
	}).Error; err != nil {
		t.Fatalf("seed capability: %v", err)
	}

	// 只改名称：探测结论应保留。
	body := fmt.Sprintf(`{"name":"probe-renamed","type":"openai","config":%q,"console":"","proxy":"","error_matcher":""}`, provider.Config)
	c, rec := gzipProbeCtx(t, http.MethodPut, "/api/providers/1", body, provider.ID)
	UpdateProvider(c)
	if env := decodeProbeEnvelope(t, rec); env.Code != http.StatusOK {
		t.Fatalf("rename code = %d, message=%s", env.Code, env.Message)
	}
	var afterRename models.Provider
	if err := models.DB.First(&afterRename, provider.ID).Error; err != nil {
		t.Fatalf("reload after rename: %v", err)
	}
	if !afterRename.RequestGzipEnabled || afterRename.RequestGzipSupported == nil || !*afterRename.RequestGzipSupported {
		t.Fatal("只改名称不应清空探测结论")
	}

	// 改基础地址：探测结论必须失效。
	changedConfig := fmt.Sprintf(`{"base_url":%q,"api_key":"sk-test"}`, "https://other.example.com")
	body = fmt.Sprintf(`{"name":"probe-renamed","type":"openai","config":%q,"console":"","proxy":"","error_matcher":""}`, changedConfig)
	c, rec = gzipProbeCtx(t, http.MethodPut, "/api/providers/1", body, provider.ID)
	UpdateProvider(c)
	if env := decodeProbeEnvelope(t, rec); env.Code != http.StatusOK {
		t.Fatalf("config change code = %d, message=%s", env.Code, env.Message)
	}
	var afterChange models.Provider
	if err := models.DB.First(&afterChange, provider.ID).Error; err != nil {
		t.Fatalf("reload after config change: %v", err)
	}
	if afterChange.RequestGzipEnabled {
		t.Fatal("端点变化后提供商开关必须关闭")
	}
	if afterChange.RequestGzipSupported != nil {
		t.Fatal("端点变化后支持状态必须清空")
	}
	if afterChange.RequestGzipProbedAt != nil {
		t.Fatal("端点变化后检测时间必须清空")
	}
	if afterChange.RequestGzipProbeHash != "" {
		t.Fatal("端点变化后配置指纹必须清空")
	}
}

func TestUpdateProviderRequestGzipRejectsStaleProbe(t *testing.T) {
	up := &gzipProbeUpstream{}
	server := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(server.Close)
	provider, _ := setupGzipProbeDB(t, server.URL)

	if err := models.DB.Model(&models.Provider{}).Where("id = ?", provider.ID).Updates(map[string]any{
		"request_gzip_supported":  true,
		"request_gzip_probe_hash": "stale",
	}).Error; err != nil {
		t.Fatalf("seed stale probe: %v", err)
	}

	c, rec := gzipProbeCtx(t, http.MethodPatch, "/api/providers/1/request-gzip", `{"enabled":true}`, provider.ID)
	UpdateProviderRequestGzip(c)
	env := decodeProbeEnvelope(t, rec)
	if env.Code != http.StatusConflict {
		t.Fatalf("失效指纹应返回 409，got %d", env.Code)
	}
}
