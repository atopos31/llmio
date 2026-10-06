package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/atopos31/llmio/models"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// 这一组守的是 §4.2 那句"**保存路径完全不变**"。
//
// 自动填写是**纯前端**的：它只往用户正看着的表单里预填值，服务端一个字段都
// 不补。这条约定没有类型系统兜着，只能靠测试钉住——一旦有人在 Create/Update
// 里"顺手"补个默认值，症状会是"某些关联明明没勾工具调用，却开始接工具调用
// 请求了"，而接口不会有任何报错。
//
// 反过来的那一半同样重要：用户明确勾掉的 false 必须原样落库，不能被当成
// "没填"再补回 true。

func setupWritePathDB(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "writepath.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.Model{}, &models.Provider{}, &models.ModelWithProvider{}, &models.Config{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}

	model := models.Model{Name: "gpt-4o"}
	model.ID = 1
	provider := models.Provider{Name: "p1", Type: "openai"}
	provider.ID = 1
	if err := db.Create(&model).Error; err != nil {
		t.Fatalf("seed model: %v", err)
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	// 策略显式打开。保存路径**不该读它**——这条测试就是在证明这一点。
	if err := db.Create(&models.Config{
		Key:   models.KeyModelAutofillPolicy,
		Value: `{"enabled":true,"overwrite":true,"allow_deprecated":true}`,
	}).Error; err != nil {
		t.Fatalf("seed config: %v", err)
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

// doJSON 与 log_compress_test.go 里的 postJSON 分开：那个不带路径参数，
// 这个要能塞 :id。
func doJSON(t *testing.T, handler gin.HandlerFunc, method, path string, body any, params gin.Params) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = params
	handler(c)
	return w
}

func envelopeCode(t *testing.T, w *httptest.ResponseRecorder) int {
	t.Helper()
	var env struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("解响应: %v（原文 %s）", err, w.Body.String())
	}
	return env.Code
}

// TestCreateModelProviderWritesExactlyWhatIsSent：请求里是什么就写什么，
// 一个字段都不补。
func TestCreateModelProviderWritesExactlyWhatIsSent(t *testing.T) {
	setupWritePathDB(t)

	// 三档价格全 0、币种留空、三个能力全 false——这正是"源没给"时前端
	// 保存下来的形态（表单默认值）。服务端若按策略补值，这里就会被改写成别的。
	w := doJSON(t, CreateModelProvider, http.MethodPost, "/api/model-providers", map[string]any{
		"model_id":          1,
		"provider_id":       1,
		"provider_name":     "gpt-4o",
		"tool_call":         false,
		"structured_output": false,
		"image":             false,
		"with_header":       false,
		"weight":            1,
		"input_price":       0,
		"cache_read_price":  0,
		"output_price":      0,
		"currency":          "",
	}, nil)

	if code := envelopeCode(t, w); code != http.StatusOK {
		t.Fatalf("业务码 = %d，期望 200（原文 %s）", code, w.Body.String())
	}

	// 注意：请求体的 JSON 键是 provider_name，落库的列却叫 provider_model——
	// 这是既有契约，不去动它，但测试里得跟着走。
	row, err := gorm.G[models.ModelWithProvider](models.DB).Where("provider_model = ?", "gpt-4o").First(context.Background())
	if err != nil {
		t.Fatalf("load row: %v", err)
	}
	// false 是**非 nil 的 false**，不是"没填"。
	if row.ToolCall == nil || *row.ToolCall {
		t.Errorf("tool_call = %v，期望非 nil 的 false", row.ToolCall)
	}
	if row.StructuredOutput == nil || *row.StructuredOutput {
		t.Errorf("structured_output = %v，期望非 nil 的 false", row.StructuredOutput)
	}
	if row.Image == nil || *row.Image {
		t.Errorf("image = %v，期望非 nil 的 false", row.Image)
	}
	if row.InputPrice == nil || *row.InputPrice != 0 {
		t.Errorf("input_price = %v，期望 0", row.InputPrice)
	}
	if row.Currency != "" {
		t.Errorf("currency = %q，期望原样（空串）", row.Currency)
	}
}

// TestUpdateModelProviderDoesNotFillFromPolicy：更新路径同样一个字段都不补。
func TestUpdateModelProviderDoesNotFillFromPolicy(t *testing.T) {
	setupWritePathDB(t)

	existing := models.ModelWithProvider{
		ModelID: 1, ProviderID: 1, ProviderModel: "gpt-4o",
		ToolCall: boolPtr(false), StructuredOutput: boolPtr(false), Image: boolPtr(false),
		InputPrice: floatPtr(0), CacheReadPrice: floatPtr(0), OutputPrice: floatPtr(0),
		Currency: "CNY",
	}
	existing.ID = 7
	if err := models.DB.Create(&existing).Error; err != nil {
		t.Fatalf("seed row: %v", err)
	}

	w := doJSON(t, UpdateModelProvider, http.MethodPut, "/api/model-providers/7", map[string]any{
		"model_id":          1,
		"provider_id":       1,
		"provider_name":     "gpt-4o-2024",
		"tool_call":         false,
		"structured_output": false,
		"image":             false,
		"with_header":       false,
		"weight":            2,
		"input_price":       0,
		"cache_read_price":  0,
		"output_price":      0,
		"currency":          "CNY",
	}, gin.Params{{Key: "id", Value: "7"}})

	if code := envelopeCode(t, w); code != http.StatusOK {
		t.Fatalf("业务码 = %d，期望 200（原文 %s）", code, w.Body.String())
	}

	row, err := gorm.G[models.ModelWithProvider](models.DB).Where("id = ?", 7).First(context.Background())
	if err != nil {
		t.Fatalf("load row: %v", err)
	}
	if row.ProviderModel != "gpt-4o-2024" {
		t.Errorf("provider_model = %q，期望按请求更新", row.ProviderModel)
	}
	if row.ToolCall == nil || *row.ToolCall {
		t.Errorf("tool_call = %v，期望非 nil 的 false", row.ToolCall)
	}
	if row.Weight != 2 {
		t.Errorf("weight = %d，期望 2", row.Weight)
	}
	if row.Currency != "CNY" {
		t.Errorf("currency = %q，期望 CNY（没被换成 USD）", row.Currency)
	}
}
