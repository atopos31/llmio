package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

/**
 * 转发设置端点 /api/providers/:id/forward 的契约。
 *
 * 钉住三件事：
 *  1. 校验：模式三态、改写三态、slug 规则与唯一性、all 必须带目标模型；
 *  2. 解耦：通用 PUT /providers/:id 不得覆盖 forward_* 列；
 *  3. 默认值：新建提供商时安全名称回填 slug、改写策略 known。
 */

type forwardSettingsFixture struct {
	router *gin.Engine
}

type forwardSettingsResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    models.Provider `json:"data"`
}

func setupForwardSettings(t *testing.T) (*forwardSettingsFixture, models.Provider) {
	t.Helper()

	gin.SetMode(gin.TestMode)

	path := filepath.Join(t.TempDir(), "forward-settings.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.Provider{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}

	provider := models.Provider{
		Name:           "alpha-name",
		Type:           "openai",
		Config:         `{"base_url":"http://upstream.local","api_key":"sk"}`,
		ForwardSlug:    new("alpha-name"),
		ForwardRewrite: consts.ForwardRewriteDefault,
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("create provider: %v", err)
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
	api := router.Group("/api")
	{
		api.POST("/providers", CreateProvider)
		api.PUT("/providers/:id", UpdateProvider)
		api.PUT("/providers/:id/forward", UpdateProviderForward)
	}

	return &forwardSettingsFixture{router: router}, provider
}

func callForwardAPI(t *testing.T, f *forwardSettingsFixture, method, target, body string) (*httptest.ResponseRecorder, forwardSettingsResponse) {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)

	var decoded forwardSettingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("response is not JSON: %s", w.Body.String())
	}
	return w, decoded
}

func loadProvider(t *testing.T, id uint) models.Provider {
	t.Helper()
	provider, err := gorm.G[models.Provider](models.DB).Where("id = ?", id).First(t.Context())
	if err != nil {
		t.Fatalf("load provider %d: %v", id, err)
	}
	return provider
}

func forwardPath(id uint) string {
	return "/api/providers/" + strconv.FormatUint(uint64(id), 10) + "/forward"
}

// 保存完整设置：地址、模式、改写策略、目标模型都落库，并回显更新后的提供商。
func TestUpdateProviderForwardSavesSettings(t *testing.T) {
	f, provider := setupForwardSettings(t)

	w, body := callForwardAPI(t, f, http.MethodPut, forwardPath(provider.ID),
		`{"slug":"my-slug","mode":"pure","rewrite":"all","target_model":"upstream-model"}`)
	if w.Code != http.StatusOK || body.Code != http.StatusOK {
		t.Fatalf("应保存成功, http=%d code=%d msg=%s", w.Code, body.Code, body.Message)
	}
	if body.Data.ForwardSlug == nil || *body.Data.ForwardSlug != "my-slug" {
		t.Errorf("slug = %v, want my-slug", body.Data.ForwardSlug)
	}
	if body.Data.ForwardMode == nil || !*body.Data.ForwardMode {
		t.Errorf("pure 应落 true, got %v", body.Data.ForwardMode)
	}
	if body.Data.ForwardRewrite != consts.ForwardRewriteAll || body.Data.ForwardTargetModel != "upstream-model" {
		t.Errorf("rewrite/target = %q/%q", body.Data.ForwardRewrite, body.Data.ForwardTargetModel)
	}

	stored := loadProvider(t, provider.ID)
	if stored.ForwardSlug == nil || *stored.ForwardSlug != "my-slug" {
		t.Errorf("库内 slug = %v, want my-slug", stored.ForwardSlug)
	}
}

// off 允许清空地址：slug 与目标模型都写回 NULL/空串，模式置 nil。
func TestUpdateProviderForwardOffClearsSlug(t *testing.T) {
	f, provider := setupForwardSettings(t)

	if _, body := callForwardAPI(t, f, http.MethodPut, forwardPath(provider.ID),
		`{"slug":"my-slug","mode":"pure","rewrite":"all","target_model":"up"}`); body.Code != 200 {
		t.Fatalf("precondition save failed: %d %s", body.Code, body.Message)
	}

	_, body := callForwardAPI(t, f, http.MethodPut, forwardPath(provider.ID),
		`{"slug":"","mode":"off","rewrite":"none"}`)
	if body.Code != http.StatusOK {
		t.Fatalf("off 应保存成功, code=%d msg=%s", body.Code, body.Message)
	}
	stored := loadProvider(t, provider.ID)
	if stored.ForwardMode != nil {
		t.Errorf("off 应落 NULL, got %v", stored.ForwardMode)
	}
	if stored.ForwardSlug != nil {
		t.Errorf("off 应清空 slug, got %v", *stored.ForwardSlug)
	}
	if stored.ForwardTargetModel != "" {
		t.Errorf("非 all 应清空 target, got %q", stored.ForwardTargetModel)
	}
	if stored.ForwardRewrite != consts.ForwardRewriteNone {
		t.Errorf("rewrite = %q, want none", stored.ForwardRewrite)
	}
}

// 改写策略缺省为 known（兼容升级前老行）。
func TestUpdateProviderForwardDefaultsRewriteKnown(t *testing.T) {
	f, provider := setupForwardSettings(t)

	_, body := callForwardAPI(t, f, http.MethodPut, forwardPath(provider.ID),
		`{"slug":"alpha","mode":"mixed"}`)
	if body.Code != http.StatusOK {
		t.Fatalf("应保存成功, code=%d msg=%s", body.Code, body.Message)
	}
	if body.Data.ForwardRewrite != consts.ForwardRewriteKnown {
		t.Errorf("rewrite 默认应为 known, got %q", body.Data.ForwardRewrite)
	}
}

// 非法 slug 拒绝。
func TestUpdateProviderForwardRejectsInvalidSlug(t *testing.T) {
	f, provider := setupForwardSettings(t)

	_, body := callForwardAPI(t, f, http.MethodPut, forwardPath(provider.ID),
		`{"slug":"bad slug","mode":"mixed"}`)
	if body.Code != http.StatusBadRequest {
		t.Fatalf("非法 slug 应 400, code=%d", body.Code)
	}
	if !strings.Contains(body.Message, "invalid forward slug") {
		t.Errorf("错误信息应指向 slug 规则, got %q", body.Message)
	}
}

// 除 off 之外模式必须给 slug。
func TestUpdateProviderForwardRequiresSlug(t *testing.T) {
	f, provider := setupForwardSettings(t)

	_, body := callForwardAPI(t, f, http.MethodPut, forwardPath(provider.ID),
		`{"slug":"","mode":"mixed"}`)
	if body.Code != http.StatusBadRequest || !strings.Contains(body.Message, "forward slug is required") {
		t.Fatalf("缺 slug 应 400, code=%d msg=%q", body.Code, body.Message)
	}
}

// slug 全局唯一：占用他人 slug 拒绝。
func TestUpdateProviderForwardRejectsDuplicateSlug(t *testing.T) {
	f, provider := setupForwardSettings(t)

	other := models.Provider{Name: "beta-name", Type: "openai", Config: `{}`, ForwardSlug: new("beta-slug"), ForwardRewrite: consts.ForwardRewriteDefault}
	if err := models.DB.Create(&other).Error; err != nil {
		t.Fatalf("create other provider: %v", err)
	}

	_, body := callForwardAPI(t, f, http.MethodPut, forwardPath(other.ID),
		`{"slug":"alpha-name","mode":"mixed"}`)
	if body.Code != http.StatusBadRequest || !strings.Contains(body.Message, "forward slug already in use") {
		t.Fatalf("重复 slug 应 400, code=%d msg=%q", body.Code, body.Message)
	}
	_ = provider
}

// all 改写必须带目标模型。
func TestUpdateProviderForwardRequiresTargetForAll(t *testing.T) {
	f, provider := setupForwardSettings(t)

	_, body := callForwardAPI(t, f, http.MethodPut, forwardPath(provider.ID),
		`{"slug":"alpha","mode":"mixed","rewrite":"all"}`)
	if body.Code != http.StatusBadRequest || !strings.Contains(body.Message, "forward target model is required") {
		t.Fatalf("all 缺 target 应 400, code=%d msg=%q", body.Code, body.Message)
	}
}

// 未知模式拒绝。
func TestUpdateProviderForwardRejectsUnknownMode(t *testing.T) {
	f, provider := setupForwardSettings(t)

	_, body := callForwardAPI(t, f, http.MethodPut, forwardPath(provider.ID),
		`{"slug":"alpha","mode":"weird"}`)
	if body.Code != http.StatusBadRequest || !strings.Contains(body.Message, "invalid forward mode") {
		t.Fatalf("非法模式应 400, code=%d msg=%q", body.Code, body.Message)
	}
}

// 提供商不存在返回 404（管理接口的 200+code 约定）。
func TestUpdateProviderForwardUnknownProvider(t *testing.T) {
	f, _ := setupForwardSettings(t)

	_, body := callForwardAPI(t, f, http.MethodPut, "/api/providers/99999/forward",
		`{"slug":"alpha","mode":"mixed"}`)
	if body.Code != http.StatusNotFound || !strings.Contains(body.Message, "Provider not found") {
		t.Fatalf("未知提供商应 404, code=%d msg=%q", body.Code, body.Message)
	}
}

// 通用 PUT 只改基础字段，不得清空已配置的转发设置。
func TestUpdateProviderPreservesForwardSettings(t *testing.T) {
	f, provider := setupForwardSettings(t)

	if _, body := callForwardAPI(t, f, http.MethodPut, forwardPath(provider.ID),
		`{"slug":"sticky-slug","mode":"pure","rewrite":"all","target_model":"sticky-target"}`); body.Code != 200 {
		t.Fatalf("precondition save failed: %d %s", body.Code, body.Message)
	}

	_, body := callForwardAPI(t, f, http.MethodPut, "/api/providers/"+strconv.FormatUint(uint64(provider.ID), 10),
		`{"name":"alpha-renamed","type":"openai","config":"{}","console":"","proxy":"","error_matcher":""}`)
	if body.Code != http.StatusOK {
		t.Fatalf("通用更新应成功, code=%d msg=%s", body.Code, body.Message)
	}

	stored := loadProvider(t, provider.ID)
	if stored.ForwardSlug == nil || *stored.ForwardSlug != "sticky-slug" {
		t.Errorf("通用更新后 slug 被清空: %v", stored.ForwardSlug)
	}
	if stored.ForwardMode == nil || !*stored.ForwardMode {
		t.Errorf("通用更新后模式被清空: %v", stored.ForwardMode)
	}
	if stored.ForwardRewrite != consts.ForwardRewriteAll || stored.ForwardTargetModel != "sticky-target" {
		t.Errorf("通用更新后改写设置被清空: %q/%q", stored.ForwardRewrite, stored.ForwardTargetModel)
	}
}

// 新建提供商：安全名称默认回填 slug 且改写策略为 known。
func TestCreateProviderDefaultsForwardSlug(t *testing.T) {
	f, _ := setupForwardSettings(t)

	_, body := callForwardAPI(t, f, http.MethodPost, "/api/providers",
		`{"name":"safe-name","type":"openai","config":"{}"}`)
	if body.Code != http.StatusOK {
		t.Fatalf("创建应成功, code=%d msg=%s", body.Code, body.Message)
	}
	if body.Data.ForwardSlug == nil || *body.Data.ForwardSlug != "safe-name" {
		t.Errorf("安全名称应回填 slug, got %v", body.Data.ForwardSlug)
	}
	if body.Data.ForwardRewrite != consts.ForwardRewriteKnown {
		t.Errorf("新建默认 rewritten = %q, want known", body.Data.ForwardRewrite)
	}
}

// 新建提供商：名称不是 URL 安全字符时不为地址标识（留空由用户设置）。
func TestCreateProviderUnsafeNameHasNoSlug(t *testing.T) {
	f, _ := setupForwardSettings(t)

	_, body := callForwardAPI(t, f, http.MethodPost, "/api/providers",
		`{"name":"unsafe name","type":"openai","config":"{}"}`)
	if body.Code != http.StatusOK {
		t.Fatalf("创建应成功, code=%d msg=%s", body.Code, body.Message)
	}
	if body.Data.ForwardSlug != nil {
		t.Errorf("不安全名称不应回填 slug, got %v", *body.Data.ForwardSlug)
	}
	if body.Data.ForwardRewrite != consts.ForwardRewriteKnown {
		t.Errorf("新建默认 rewritten = %q, want known", body.Data.ForwardRewrite)
	}
}

// 新建提供商：名称恰好被别人用作 slug 时不自动占用。
func TestCreateProviderSkipsTakenSlug(t *testing.T) {
	f, provider := setupForwardSettings(t)

	if _, body := callForwardAPI(t, f, http.MethodPut, forwardPath(provider.ID),
		`{"slug":"taken-slug","mode":"mixed"}`); body.Code != 200 {
		t.Fatalf("precondition save failed: %d %s", body.Code, body.Message)
	}

	_, body := callForwardAPI(t, f, http.MethodPost, "/api/providers",
		`{"name":"taken-slug","type":"openai","config":"{}"}`)
	if body.Code != http.StatusOK {
		t.Fatalf("创建应成功, code=%d msg=%s", body.Code, body.Message)
	}
	if body.Data.ForwardSlug != nil {
		t.Errorf("已被占用的 slug 不应回填, got %v", *body.Data.ForwardSlug)
	}
}
