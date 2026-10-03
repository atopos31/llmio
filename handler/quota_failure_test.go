package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/quota"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// 本文件覆盖配额端点的**故障诊断**分支：数据库不可用、配置读不出来、配置写不回去。
//
// 这些分支的价值不在覆盖率，而在于它们是"用户会不会拿到一条能自救的错误"的地方：
//   - 数据库出问题 -> 500 且带出底层原因，而不是伪装成 404「未找到供应商」
//   - 配置读不出来 -> 500（不是 400：用户没填错任何东西）
//   - 配置写不回去 -> 500，且**不能**报成功——报成功会让前端以为已经存上了

// withBrokenDB 让 models.DB 指向一个已关闭的连接。
//
// 关连接而不是用空指针：查询会正常返回错误，不会 panic 也不会挂起。
func withBrokenDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "broken.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("建库失败: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("取底层连接失败: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("关连接失败: %v", err)
	}
	prev := models.DB
	models.DB = db
	t.Cleanup(func() { models.DB = prev })
}

// withCorruptQuotaStore 把包级 store 换成一个配置已损坏的。
func withCorruptQuotaStore(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "quota.config.json")
	if err := os.WriteFile(path, []byte("{不是 JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	prev := quotaStore
	quotaStore = service.NewQuotaStore(path)
	t.Cleanup(func() { quotaStore = prev })
}

// withUnwritableQuotaStore 把包级 store 换成"读得到、写不回去"的。
//
// 造法：先存一个合法数据源，再占住配置文件的 `<path>.tmp` 位置——
// SaveConfig 是"先写 .tmp 再改名"，占住 .tmp 就精确地只让写这一步失败，
// 而 Load 照常成功。这比"目录只读"可靠：Windows 上目录只读位不拦写入。
func withUnwritableQuotaStore(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "quota.config.json")
	s := service.NewQuotaStore(path)
	if _, err := s.UpsertSource(quota.Source{
		ID: "a", Name: "A", Type: quota.TypeHTTP, URL: "https://a",
	}, false); err != nil {
		t.Fatalf("预置数据源应成功: %v", err)
	}
	if err := os.Mkdir(path+".tmp", 0o755); err != nil {
		t.Fatalf("占住 .tmp 应成功: %v", err)
	}
	prev := quotaStore
	quotaStore = s
	t.Cleanup(func() { quotaStore = prev })
}

// ---------------------------------------------------------------------------
// 数据库故障
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// 配置读不出来 / 写不回去
// ---------------------------------------------------------------------------

func TestQuotaEndpoints500WhenConfigUnreadable(t *testing.T) {
	cases := []struct {
		name string
		run  func(*gin.Context)
	}{
		{"读配置", func(c *gin.Context) { GetQuotaConfig(c) }},
		{"跑全部源", func(c *gin.Context) { RunQuotaSources(c) }},
		{"刷单个源", func(c *gin.Context) {
			c.Params = gin.Params{{Key: "id", Value: "a"}}
			RefreshQuotaSource(c)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withCorruptQuotaStore(t)
			c, w := jsonCtx(t, http.MethodPost, "/api/quota", nil)
			tc.run(c)
			// 配置损坏是服务端的问题，不是用户填错了：必须是 500 而不是 400
			if resp := decodeResp(t, w); resp.Code != http.StatusInternalServerError {
				t.Fatalf("应业务码 500，实得 %d（%s）", resp.Code, w.Body.String())
			}
		})
	}
}

func TestQuotaWrites500WhenConfigUnwritable(t *testing.T) {
	t.Run("新增源", func(t *testing.T) {
		withUnwritableQuotaStore(t)
		c, w := jsonCtx(t, http.MethodPost, "/api/quota/sources", map[string]any{
			"id": "new", "name": "新的", "type": "http", "url": "https://new",
		})
		UpsertQuotaSource(c)
		if resp := decodeResp(t, w); resp.Code != http.StatusInternalServerError {
			t.Fatalf("写盘失败应业务码 500，实得 %d（%s）", resp.Code, w.Body.String())
		}
	})

	t.Run("更新已有源", func(t *testing.T) {
		withUnwritableQuotaStore(t)
		c, w := jsonCtx(t, http.MethodPut, "/api/quota/sources", map[string]any{
			"id": "a", "name": "改名", "type": "http", "url": "https://a2",
		})
		UpsertQuotaSource(c)
		if resp := decodeResp(t, w); resp.Code != http.StatusInternalServerError {
			t.Fatalf("写盘失败应业务码 500，实得 %d（%s）", resp.Code, w.Body.String())
		}
	})

	t.Run("删除源", func(t *testing.T) {
		withUnwritableQuotaStore(t)
		c, w := jsonCtx(t, http.MethodDelete, "/api/quota/sources/a", nil)
		c.Params = gin.Params{{Key: "id", Value: "a"}}
		DeleteQuotaSource(c)
		// 关键：命中并删除了，但落盘失败 —— 必须报错，
		// 否则前端会把源从列表里划掉，而磁盘上它还在
		if resp := decodeResp(t, w); resp.Code != http.StatusInternalServerError {
			t.Fatalf("写盘失败应业务码 500，实得 %d（%s）", resp.Code, w.Body.String())
		}
	})
}

func TestUpdateQuotaConfig(t *testing.T) {
	t.Run("正常更新并落盘", func(t *testing.T) {
		withTempQuotaStore(t)
		c, w := jsonCtx(t, http.MethodPut, "/api/quota/config", map[string]any{
			"refreshInterval": 300, "warningAt": 70,
		})
		UpdateQuotaConfig(c)
		if resp := decodeResp(t, w); resp.Code != http.StatusOK {
			t.Fatalf("应成功: %s", w.Body.String())
		}
		cfg, err := quotaStore.Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.RefreshInterval != 300 || cfg.WarningAt != 70 {
			t.Fatalf("应落盘新值: %#v", cfg)
		}
	})

	t.Run("负数是用户输入问题 400", func(t *testing.T) {
		withTempQuotaStore(t)
		c, w := jsonCtx(t, http.MethodPut, "/api/quota/config", map[string]any{
			"refreshInterval": -1, "warningAt": 70,
		})
		UpdateQuotaConfig(c)
		if resp := decodeResp(t, w); resp.Code != http.StatusBadRequest {
			t.Fatalf("应业务码 400，实得 %d（%s）", resp.Code, w.Body.String())
		}
	})

	t.Run("阈值越界 400", func(t *testing.T) {
		withTempQuotaStore(t)
		c, w := jsonCtx(t, http.MethodPut, "/api/quota/config", map[string]any{
			"refreshInterval": 60, "warningAt": 101,
		})
		UpdateQuotaConfig(c)
		if resp := decodeResp(t, w); resp.Code != http.StatusBadRequest {
			t.Fatalf("应业务码 400，实得 %d（%s）", resp.Code, w.Body.String())
		}
	})

	t.Run("脏请求体 400", func(t *testing.T) {
		withTempQuotaStore(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPut, "/api/quota/config", strings.NewReader("nope"))
		UpdateQuotaConfig(c)
		if resp := decodeResp(t, w); resp.Code != http.StatusBadRequest {
			t.Fatalf("应业务码 400，实得 %d（%s）", resp.Code, w.Body.String())
		}
	})

	t.Run("只读模式 403", func(t *testing.T) {
		t.Setenv("LLMIO_QUOTA_ALLOW_WRITE", "false")
		withTempQuotaStore(t)
		c, w := jsonCtx(t, http.MethodPut, "/api/quota/config", map[string]any{"refreshInterval": 60})
		UpdateQuotaConfig(c)
		if resp := decodeResp(t, w); resp.Code != http.StatusForbidden {
			t.Fatalf("应业务码 403，实得 %d", resp.Code)
		}
	})

	t.Run("写盘失败 500", func(t *testing.T) {
		withUnwritableQuotaStore(t)
		c, w := jsonCtx(t, http.MethodPut, "/api/quota/config", map[string]any{
			"refreshInterval": 300, "warningAt": 70,
		})
		UpdateQuotaConfig(c)
		if resp := decodeResp(t, w); resp.Code != http.StatusInternalServerError {
			t.Fatalf("应业务码 500，实得 %d（%s）", resp.Code, w.Body.String())
		}
	})
}
