package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/quota"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// 本文件测配额端点的接线与权限。
//
// quotaStore 是包级单例，测试要改它的配置路径，因此在每个用例开头
// 用 withTempQuotaStore 换一个临时路径的 store，用完恢复。

func init() { gin.SetMode(gin.TestMode) }

// withTempQuotaStore 把包级 store 换成指向临时文件的，并返回清理函数。
func withTempQuotaStore(t *testing.T) {
	t.Helper()
	prev := quotaStore
	quotaStore = service.NewQuotaStore(filepath.Join(t.TempDir(), "quota.config.json"))
	t.Cleanup(func() { quotaStore = prev })
}

// decodeResp 解出统一响应体。
func decodeResp(t *testing.T, w *httptest.ResponseRecorder) common.Response {
	t.Helper()
	var resp common.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应不是合法 JSON: %v（原文 %q）", err, w.Body.String())
	}
	return resp
}

// ---------------------------------------------------------------------------
// 读端点
// ---------------------------------------------------------------------------

func TestGetQuotaConfig(t *testing.T) {
	withTempQuotaStore(t)
	c, w := jsonCtx(t, http.MethodGet, "/api/quota/config", nil)

	GetQuotaConfig(c)

	resp := decodeResp(t, w)
	if resp.Code != http.StatusOK {
		t.Fatalf("应成功: %#v", resp)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data 应为对象: %#v", resp.Data)
	}
	// 前端要凭这些渲染编辑器
	for _, k := range []string{"config", "builtins", "configPath", "writeEnabled", "defaultRefresh", "defaultWarning"} {
		if _, has := data[k]; !has {
			t.Fatalf("缺少字段 %q: %#v", k, data)
		}
	}
	// 内置适配器清单不能是空的，否则前端编辑器没有可选项
	builtins, ok := data["builtins"].([]any)
	if !ok || len(builtins) == 0 {
		t.Fatalf("builtins 应为非空数组: %#v", data["builtins"])
	}
}

func TestGetQuotaConfigMasksSecret(t *testing.T) {
	withTempQuotaStore(t)
	if _, err := quotaStore.UpsertSource(quota.Source{
		ID: "s", Type: quota.TypeHTTP, URL: "https://x", APIKey: "sk-abcdefghijklmn",
	}, false); err != nil {
		t.Fatal(err)
	}

	c, w := jsonCtx(t, http.MethodGet, "/api/quota/config", nil)
	GetQuotaConfig(c)

	// 明文密钥绝不能出现在响应体里
	if strings.Contains(w.Body.String(), "sk-abcdefghijklmn") {
		t.Fatalf("响应体泄漏了明文密钥: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "****") {
		t.Fatalf("响应体应含掩码标记: %s", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 写端点：权限与校验
// ---------------------------------------------------------------------------

func TestQuotaWritesBlockedWhenReadOnly(t *testing.T) {
	t.Setenv("LLMIO_QUOTA_ALLOW_WRITE", "false")
	withTempQuotaStore(t)

	cases := []struct {
		name string
		run  func(*gin.Context)
	}{
		{"新增", func(c *gin.Context) { UpsertQuotaSource(c) }},
		{"删除", func(c *gin.Context) { c.Params = gin.Params{{Key: "id", Value: "x"}}; DeleteQuotaSource(c) }},
		{"导入", func(c *gin.Context) { ImportQuotaSource(c) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, w := jsonCtx(t, http.MethodPost, "/api/quota", map[string]any{"upstreamId": 1})
			tc.run(c)
			// 约定：HTTP 恒 200，业务码在 body.code 里
			if resp := decodeResp(t, w); resp.Code != http.StatusForbidden {
				t.Fatalf("只读模式应业务码 403，实得 %d（%s）", resp.Code, w.Body.String())
			}
		})
	}
}

func TestTestQuotaSourceAllowedWhenReadOnly(t *testing.T) {
	t.Setenv("LLMIO_QUOTA_ALLOW_WRITE", "false")
	withTempQuotaStore(t)

	// 试跑不改变任何状态，只读模式下必须仍可用——否则用户没法调脚本
	c, w := jsonCtx(t, http.MethodPost, "/api/quota/test", quota.Source{
		ID: "t", Type: quota.TypeHTTP, URL: "http://127.0.0.1:1/x",
	})
	TestQuotaSource(c)

	resp := decodeResp(t, w)
	if resp.Code == http.StatusForbidden {
		t.Fatal("试跑不改变状态，不应被只读模式拦下")
	}
	// 连不上必然失败，但失败要体现在结果体的 ok=false 里，而不是 HTTP 权限码
	data := resp.Data.(map[string]any)
	if ok, _ := data["ok"].(bool); ok {
		t.Fatalf("连不上不该报成功: %#v", data)
	}
}

func TestUpsertQuotaSourceCreate(t *testing.T) {
	withTempQuotaStore(t)
	c, w := jsonCtx(t, http.MethodPost, "/api/quota/sources", map[string]any{
		"name": "源一", "type": "http", "url": "https://x", "apiKey": "sk-abcdefghijklmn",
	})
	UpsertQuotaSource(c)

	resp := decodeResp(t, w)
	if resp.Code != http.StatusOK {
		t.Fatalf("应成功: %#v", resp)
	}
	// 返回体里的密钥必须是掩码
	if strings.Contains(w.Body.String(), "sk-abcdefghijklmn") {
		t.Fatalf("响应泄漏了明文密钥: %s", w.Body.String())
	}
	cfg, _ := quotaStore.Load()
	if len(cfg.Sources) != 1 {
		t.Fatalf("应落盘 1 个源: %#v", cfg.Sources)
	}
}

func TestUpsertQuotaSourceValidationIs400(t *testing.T) {
	withTempQuotaStore(t)
	// 缺 url -> 用户输入问题 -> 400 而非 500
	c, w := jsonCtx(t, http.MethodPost, "/api/quota/sources", map[string]any{
		"name": "坏的", "type": "http",
	})
	UpsertQuotaSource(c)

	if resp := decodeResp(t, w); resp.Code != http.StatusBadRequest {
		t.Fatalf("校验失败应业务码 400，实得 %d（%s）", resp.Code, w.Body.String())
	}
}

func TestUpsertQuotaSourceUnknownBuiltinIs400(t *testing.T) {
	withTempQuotaStore(t)
	c, w := jsonCtx(t, http.MethodPost, "/api/quota/sources", map[string]any{
		"type": "builtin", "builtin": "nope",
	})
	UpsertQuotaSource(c)
	if resp := decodeResp(t, w); resp.Code != http.StatusBadRequest {
		t.Fatalf("未知适配器应业务码 400，实得 %d", resp.Code)
	}
}

func TestUpsertQuotaSourceDuplicateIs400(t *testing.T) {
	withTempQuotaStore(t)
	body := map[string]any{"id": "dup", "name": "A", "type": "http", "url": "https://a"}
	c1, w1 := jsonCtx(t, http.MethodPut, "/api/quota/sources", body)
	UpsertQuotaSource(c1)
	if resp := decodeResp(t, w1); resp.Code != http.StatusOK {
		t.Fatalf("首次应成功: %s", w1.Body.String())
	}

	c2, w2 := jsonCtx(t, http.MethodPost, "/api/quota/sources", body)
	UpsertQuotaSource(c2)
	if resp := decodeResp(t, w2); resp.Code != http.StatusBadRequest {
		t.Fatalf("新增时 id 冲突应业务码 400，实得 %d（%s）", resp.Code, w2.Body.String())
	}
}

func TestUpsertQuotaSourceBadBody(t *testing.T) {
	withTempQuotaStore(t)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/quota/sources", strings.NewReader("{不是 json"))
	c.Request.Header.Set("Content-Type", "application/json")

	UpsertQuotaSource(c)
	if resp := decodeResp(t, w); resp.Code != http.StatusBadRequest {
		t.Fatalf("脏请求体应业务码 400，实得 %d（%s）", resp.Code, w.Body.String())
	}
}

func TestDeleteQuotaSource(t *testing.T) {
	withTempQuotaStore(t)
	if _, err := quotaStore.UpsertSource(quota.Source{
		ID: "s", Type: quota.TypeHTTP, URL: "https://x",
	}, false); err != nil {
		t.Fatal(err)
	}

	t.Run("删除存在的", func(t *testing.T) {
		c, w := jsonCtx(t, http.MethodDelete, "/api/quota/sources/s", nil)
		c.Params = gin.Params{{Key: "id", Value: "s"}}
		DeleteQuotaSource(c)
		if resp := decodeResp(t, w); resp.Code != http.StatusOK {
			t.Fatalf("应成功: %#v", resp)
		}
	})

	t.Run("删除不存在的 404", func(t *testing.T) {
		c, w := jsonCtx(t, http.MethodDelete, "/api/quota/sources/nope", nil)
		c.Params = gin.Params{{Key: "id", Value: "nope"}}
		DeleteQuotaSource(c)
		if resp := decodeResp(t, w); resp.Code != http.StatusNotFound {
			t.Fatalf("应业务码 404，实得 %d", resp.Code)
		}
	})
}

// ---------------------------------------------------------------------------
// 运行与刷新
// ---------------------------------------------------------------------------

func TestRunQuotaSourcesParsesForceAndIDs(t *testing.T) {
	withTempQuotaStore(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":1}]}`))
	}))
	defer srv.Close()

	for _, id := range []string{"a", "b"} {
		if _, err := quotaStore.UpsertSource(quota.Source{
			ID: id, Name: id, Type: quota.TypeBuiltin, Builtin: "deepseek",
			BaseURL: srv.URL, Enabled: true,
		}, false); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("不带参数跑全部", func(t *testing.T) {
		c, w := jsonCtx(t, http.MethodPost, "/api/quota/run", nil)
		RunQuotaSources(c)
		resp := decodeResp(t, w)
		data := resp.Data.(map[string]any)
		if n := len(data["sources"].([]any)); n != 2 {
			t.Fatalf("应跑 2 个源，实得 %d", n)
		}
	})

	t.Run("ids 过滤", func(t *testing.T) {
		c, w := jsonCtx(t, http.MethodPost, "/api/quota/run?ids=%20a%20%2C%2C%20b%20", nil)
		RunQuotaSources(c)
		resp := decodeResp(t, w)
		data := resp.Data.(map[string]any)
		// 空白与空项要被剔掉，但两个 id 都保留
		if n := len(data["sources"].([]any)); n != 2 {
			t.Fatalf("应跑 2 个源，实得 %d", n)
		}
	})

	t.Run("force=true 生效", func(t *testing.T) {
		c, w := jsonCtx(t, http.MethodPost, "/api/quota/run?force=true", nil)
		RunQuotaSources(c)
		resp := decodeResp(t, w)
		data := resp.Data.(map[string]any)
		for _, s := range data["sources"].([]any) {
			if cached, _ := s.(map[string]any)["cached"].(bool); cached {
				t.Fatal("force 时不该有缓存命中")
			}
		}
	})
}

func TestRefreshQuotaSource(t *testing.T) {
	withTempQuotaStore(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":1}]}`))
	}))
	defer srv.Close()
	if _, err := quotaStore.UpsertSource(quota.Source{
		ID: "a", Name: "A", Type: quota.TypeBuiltin, Builtin: "deepseek",
		BaseURL: srv.URL, Enabled: true,
	}, false); err != nil {
		t.Fatal(err)
	}

	t.Run("缺 id 报 400", func(t *testing.T) {
		c, w := jsonCtx(t, http.MethodPost, "/api/quota/sources//refresh", nil)
		c.Params = gin.Params{{Key: "id", Value: "  "}}
		RefreshQuotaSource(c)
		if resp := decodeResp(t, w); resp.Code != http.StatusBadRequest {
			t.Fatalf("缺 id 应业务码 400，实得 %d", resp.Code)
		}
	})

	t.Run("正常刷新", func(t *testing.T) {
		c, w := jsonCtx(t, http.MethodPost, "/api/quota/sources/a/refresh", nil)
		c.Params = gin.Params{{Key: "id", Value: "a"}}
		RefreshQuotaSource(c)
		resp := decodeResp(t, w)
		if resp.Code != http.StatusOK {
			t.Fatalf("应成功: %#v", resp)
		}
	})
}

// ---------------------------------------------------------------------------
// 试跑
// ---------------------------------------------------------------------------

func TestTestQuotaSource(t *testing.T) {
	withTempQuotaStore(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":7}]}`))
	}))
	defer srv.Close()

	c, w := jsonCtx(t, http.MethodPost, "/api/quota/test", map[string]any{
		"type": "builtin", "builtin": "deepseek", "baseUrl": srv.URL,
	})
	TestQuotaSource(c)

	resp := decodeResp(t, w)
	if resp.Code != http.StatusOK {
		t.Fatalf("应成功: %#v", resp)
	}
	data := resp.Data.(map[string]any)
	if ok, _ := data["ok"].(bool); !ok {
		t.Fatalf("试跑应成功: %#v", data)
	}
	// 试跑不落盘
	cfg, _ := quotaStore.Load()
	if len(cfg.Sources) != 0 {
		t.Fatal("试跑不应落盘")
	}
}

func TestTestQuotaSourceBadBody(t *testing.T) {
	withTempQuotaStore(t)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/quota/test", strings.NewReader("nope"))
	TestQuotaSource(c)
	// 空请求体能绑成零值结构体，因此这里走到"校验失败"，
	// 同样是业务码 400（未知数据源类型）
	if resp := decodeResp(t, w); resp.Code != http.StatusBadRequest {
		t.Fatalf("应是业务码 400 的失败结果，实得 %d（%s）", resp.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 发现与导入
// ---------------------------------------------------------------------------

// withTestDB 建一个独立库并插入供应商，供发现/导入用。
// 建库方式与 peak_test.go 一致：每例一库、用完关连接，
// 避免 Windows 上文件被占用导致 TempDir 清理失败。
func withTestDB(t *testing.T, providers ...models.Provider) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "quota-handler.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("建测试库失败: %v", err)
	}
	if err := db.AutoMigrate(&models.Provider{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	prev := models.DB
	models.DB = db
	t.Cleanup(func() {
		models.DB = prev
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	for i := range providers {
		if err := gorm.G[models.Provider](db).Create(t.Context(), &providers[i]); err != nil {
			t.Fatalf("插入供应商失败: %v", err)
		}
	}
}

func TestDiscoverQuotaSources(t *testing.T) {
	withTempQuotaStore(t)
	withTestDB(t, models.Provider{
		Name: "DeepSeek", Type: "openai",
		Config: `{"base_url":"https://api.deepseek.com","api_key":"sk-abcdefghij"}`,
	})

	c, w := jsonCtx(t, http.MethodGet, "/api/quota/discover", nil)
	DiscoverQuotaSources(c)

	resp := decodeResp(t, w)
	if resp.Code != http.StatusOK {
		t.Fatalf("应成功: %#v", resp)
	}
	list := resp.Data.([]any)
	if len(list) != 1 {
		t.Fatalf("应得 1 项，实得 %d", len(list))
	}
	item := list[0].(map[string]any)
	if item["suggested"].(map[string]any)["builtin"] != "deepseek" {
		t.Fatalf("建议不符: %#v", item["suggested"])
	}
	// 密钥必须掩码
	if strings.Contains(w.Body.String(), "sk-abcdefghij") {
		t.Fatalf("发现表泄漏明文密钥: %s", w.Body.String())
	}
}

func TestImportQuotaSource(t *testing.T) {
	withTempQuotaStore(t)
	withTestDB(t, models.Provider{
		Name: "DeepSeek", Type: "openai",
		Config: `{"base_url":"https://api.deepseek.com","api_key":"sk-abcdefghij"}`,
	})

	t.Run("缺 upstreamId 报 400", func(t *testing.T) {
		c, w := jsonCtx(t, http.MethodPost, "/api/quota/import", map[string]any{})
		ImportQuotaSource(c)
		if resp := decodeResp(t, w); resp.Code != http.StatusBadRequest {
			t.Fatalf("缺 id 应业务码 400，实得 %d", resp.Code)
		}
	})

	t.Run("供应商不存在 404", func(t *testing.T) {
		c, w := jsonCtx(t, http.MethodPost, "/api/quota/import", map[string]any{"upstreamId": 999})
		ImportQuotaSource(c)
		if resp := decodeResp(t, w); resp.Code != http.StatusNotFound {
			t.Fatalf("应业务码 404，实得 %d（%s）", resp.Code, w.Body.String())
		}
	})

	t.Run("正常导入且密钥不经过浏览器", func(t *testing.T) {
		c, w := jsonCtx(t, http.MethodPost, "/api/quota/import", map[string]any{"upstreamId": 1})
		ImportQuotaSource(c)
		resp := decodeResp(t, w)
		if resp.Code != http.StatusOK {
			t.Fatalf("应成功: %#v", resp)
		}
		// 响应里是掩码；磁盘上是明文——密钥全程由服务端经手
		if strings.Contains(w.Body.String(), "sk-abcdefghij") {
			t.Fatalf("响应泄漏明文密钥: %s", w.Body.String())
		}
		cfg, _ := quotaStore.Load()
		if len(cfg.Sources) != 1 || cfg.Sources[0].APIKey != "sk-abcdefghij" {
			t.Fatalf("落盘应含明文密钥: %#v", cfg.Sources)
		}
	})

	t.Run("重复导入报 400", func(t *testing.T) {
		c, w := jsonCtx(t, http.MethodPost, "/api/quota/import", map[string]any{"upstreamId": 1})
		ImportQuotaSource(c)
		if resp := decodeResp(t, w); resp.Code != http.StatusBadRequest {
			t.Fatalf("重复导入应业务码 400，实得 %d（%s）", resp.Code, w.Body.String())
		}
	})
}

func TestImportQuotaSourceBadBody(t *testing.T) {
	withTempQuotaStore(t)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/quota/import", strings.NewReader("nope"))
	ImportQuotaSource(c)
	// 空请求体绑成零值 -> 缺 upstreamId -> 业务码 400
	if resp := decodeResp(t, w); resp.Code != http.StatusBadRequest {
		t.Fatalf("应是业务码 400，实得 %d（%s）", resp.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 工具函数
// ---------------------------------------------------------------------------

func TestSplitIDs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"a", []string{"a"}},
		{"a,b", []string{"a", "b"}},
		{" a ,, b ", []string{"a", "b"}},
		{",a,", []string{"a"}},
	}
	for _, c := range cases {
		got := splitIDs(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("splitIDs(%q) = %v，期望 %v", c.in, got, c.want)
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Fatalf("splitIDs(%q) = %v，期望 %v", c.in, got, c.want)
			}
		}
	}
}

func TestIsQuotaInputError(t *testing.T) {
	if isQuotaInputError(nil) {
		t.Fatal("nil 不应判为输入错误")
	}
	// 校验类错误 -> 400
	for _, msg := range []string{
		"HTTP 类型需要填写 url",
		"未知内置适配器：nope",
		"未知数据源类型：???",
		"「国家超算」需要账号会话，请在 env 里配置 SCNET_USER",
		"数据源 id 已存在：x",
		"该供应商已导入为数据源 llmio-1",
		"数据源 id 重复：x",
		"无法从上游配置推导出接口地址，请手动配置后再导入",
	} {
		if !isQuotaInputError(errText(msg)) {
			t.Fatalf("%q 应判为用户输入错误（-> 400）", msg)
		}
	}
	// 落盘失败等 -> 500
	if isQuotaInputError(errText("写入配额配置失败: permission denied")) {
		t.Fatal("落盘失败应判为服务端错误（-> 500）")
	}
}

type errText string

func (e errText) Error() string { return string(e) }
