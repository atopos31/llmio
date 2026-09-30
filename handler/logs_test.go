package handler

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"github.com/atopos31/llmio/models"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

/**
 * 请求日志列表的筛选口径。
 *
 * 这一组断言钉的是"多选"这件事的**边界**：哪几个维度可以多选（逗号即或）、
 * 维度之间是什么关系（与）、哪些维度**不能**被逗号拆开（请求标识）、
 * 以及空值不等于筛一个空串。这些判错了，接口不报错，只是返回的行不对——
 * 是最不容易被联调发现的那类错。
 */

func setupLogsHandlerDB(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "logs-handler.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	// GetRequestLogs 查完日志还会按 auth_key_id 取一次密钥名，因此这张表也得建
	if err := db.AutoMigrate(&models.ChatLog{}, &models.AuthKey{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	seed := []models.ChatLog{
		{ID: 1, Name: "alpha", ProviderName: "p1", Status: "success", Style: "openai", AuthKeyID: 1, TraceID: "t-1"},
		{ID: 2, Name: "beta", ProviderName: "p2", Status: "error", Style: "openai", AuthKeyID: 2, TraceID: "t-2"},
		{ID: 3, Name: "alpha", ProviderName: "p1", Status: "error", Style: "anthropic", AuthKeyID: 1, TraceID: "t-3"},
		{ID: 4, Name: "gamma", ProviderName: "p3", Status: "success", Style: "gemini", AuthKeyID: 0, TraceID: "t-4"},
		// 假的 TraceID 里带逗号：用来验证请求标识不会被当成多值拆开
		{ID: 5, Name: "delta", ProviderName: "p4", Status: "success", Style: "openai", AuthKeyID: 0, TraceID: "a,b"},
	}
	if err := db.Create(&seed).Error; err != nil {
		t.Fatalf("seed logs: %v", err)
	}
	prev := models.DB
	models.DB = db
	t.Cleanup(func() {
		models.DB = prev
		// 先关连接再让 t.TempDir 清理（Windows 上文件被占用会导致清理失败）
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
}

// queryLogIDs 跑一次列表接口，返回命中的日志 ID（升序）与业务码。
func queryLogIDs(t *testing.T, rawQuery string) ([]uint, int) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/logs?"+rawQuery, nil)

	GetRequestLogs(c)

	var env struct {
		Code int `json:"code"`
		Data struct {
			Data []struct {
				ID uint `json:"ID"`
			} `json:"data"`
		} `json:"data"`
	}
	decodeEnvelope(t, w, &env)

	ids := make([]uint, 0, len(env.Data.Data))
	for _, row := range env.Data.Data {
		ids = append(ids, row.ID)
	}
	slices.Sort(ids)
	return ids, env.Code
}

func TestGetRequestLogsFilters(t *testing.T) {
	setupLogsHandlerDB(t)

	tests := []struct {
		name  string
		query string
		want  []uint
	}{
		{name: "不带筛选时全给", query: "", want: []uint{1, 2, 3, 4, 5}},

		// 单值仍然按精确匹配走：旧调用方（与旧链接）的语义不该变
		{name: "单值精确命中", query: "provider_name=p1", want: []uint{1, 3}},
		{name: "单值查不到就是空", query: "provider_name=nope", want: []uint{}},

		// 多值：逗号即"任一命中"
		{name: "多值供应商取并集", query: "provider_name=p1,p2", want: []uint{1, 2, 3}},
		{name: "多值模型名取并集", query: "name=alpha,gamma", want: []uint{1, 3, 4}},
		{name: "单值状态精确命中", query: "status=error", want: []uint{2, 3}},
		{name: "多值状态取并集", query: "status=error,running", want: []uint{2, 3}},
		{name: "多值协议取并集", query: "style=anthropic,gemini", want: []uint{3, 4}},
		{name: "多值密钥取并集", query: "auth_key_id=1,2", want: []uint{1, 2, 3}},

		// 逗号两侧的空白与多余的分隔符不算取值
		{name: "取值两侧空白被忽略", query: "provider_name=p1,+p2", want: []uint{1, 2, 3}},
		{name: "只有分隔符等于没筛", query: "provider_name=,,+", want: []uint{1, 2, 3, 4, 5}},

		// 维度之间是"与"：既是 p1 又要是 error
		{name: "维度之间取交集", query: "provider_name=p1,p2&status=error", want: []uint{2, 3}},

		// 请求标识不拆：这一串里真有一条 TraceID 就叫 "a,b"
		{name: "trace_id 整串匹配不被拆开", query: "trace_id=a%2Cb", want: []uint{5}},
		{name: "id 仍按单值匹配", query: "id=4", want: []uint{4}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ids, code := queryLogIDs(t, tt.query)
			if code != 200 {
				t.Fatalf("业务码应为 200，实得 %d", code)
			}
			if !slices.Equal(ids, tt.want) {
				t.Fatalf("命中的 ID 应为 %v，实得 %v", tt.want, ids)
			}
		})
	}
}

func TestGetRequestLogsInvalidAuthKeyID(t *testing.T) {
	setupLogsHandlerDB(t)

	// 密钥 id 是整型列：把 "abc" 当成字符串塞进 IN 会静默变成"查不到"，
	// 用户看到空列表而不知道自己的筛选值不合法。
	_, code := queryLogIDs(t, "auth_key_id=abc")
	if code != http.StatusBadRequest {
		t.Fatalf("非法 auth_key_id 应返回业务码 400，实得 %d", code)
	}
}

func TestGetRequestLogsKeyName(t *testing.T) {
	setupLogsHandlerDB(t)

	if err := models.DB.Create(&models.AuthKey{Model: gorm.Model{ID: 1}, Name: "生产"}).Error; err != nil {
		t.Fatalf("seed auth key: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/logs?id=1", nil)
	GetRequestLogs(c)

	var env struct {
		Data struct {
			Data []struct {
				KeyName string `json:"key_name"`
			} `json:"data"`
		} `json:"data"`
	}
	decodeEnvelope(t, w, &env)

	if len(env.Data.Data) != 1 || env.Data.Data[0].KeyName != "生产" {
		t.Fatalf("应带出密钥名，实得 %+v", env.Data.Data)
	}
}
