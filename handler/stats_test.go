package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

// newCtx 构造一个仅带查询串的 gin 上下文。
func newCtx(t *testing.T, rawQuery string) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/metrics/stats?"+rawQuery, nil)
	return c
}

func setupHandlerDB(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "handler.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.ChatLog{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	prev := models.DB
	models.DB = db
	// 先关连接再让 t.TempDir 清理（Windows 上文件被占用会导致清理失败）
	t.Cleanup(func() {
		models.DB = prev
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
}

// ---------------------------------------------------------------------------
// 时间解析
// ---------------------------------------------------------------------------

func TestParseFlexibleTime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    time.Time
		wantErr bool
	}{
		{name: "unix 秒", in: "1700000000", want: time.Unix(1700000000, 0)},
		{name: "10 位数字视为秒", in: "1700000000", want: time.Unix(1700000000, 0)},
		// 11-13 位视为毫秒
		{name: "13 位数字视为毫秒", in: "1700000000000", want: time.UnixMilli(1700000000000)},
		{name: "11 位数字视为毫秒", in: "17000000000", want: time.UnixMilli(17000000000)},
		{name: "14 位数字过长为错误", in: "17000000000000", wantErr: true},
		// 纯数字但超出 int64 范围
		{name: "超出 int64 的数字报错", in: "99999999999999999999999999", wantErr: true},
		{name: "RFC3339", in: "2026-09-29T10:00:00Z", want: time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)},
		{name: "仅日期（本地时区）", in: "2026-09-29", want: time.Date(2026, 9, 29, 0, 0, 0, 0, time.Local)},
		{name: "空格分隔日期时间", in: "2026-09-29 10:30:00", want: time.Date(2026, 9, 29, 10, 30, 0, 0, time.Local)},
		{name: "T 分隔无时区", in: "2026-09-29T10:30:00", want: time.Date(2026, 9, 29, 10, 30, 0, 0, time.Local)},
		{name: "精确到分", in: "2026-09-29 10:30", want: time.Date(2026, 9, 29, 10, 30, 0, 0, time.Local)},
		{name: "前后空白被忽略", in: "  1700000000  ", want: time.Unix(1700000000, 0)},
		{name: "空串报错", in: "", wantErr: true},
		{name: "仅空白报错", in: "   ", wantErr: true},
		{name: "无法识别的格式报错", in: "not-a-time", wantErr: true},
		{name: "月份越界报错", in: "2026-13-99", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseFlexibleTime(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Equal(tc.want) {
				t.Fatalf("want %v, got %v", tc.want, got)
			}
		})
	}
}

func TestIsAllDigits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want bool
	}{
		{in: "0", want: true},
		{in: "1234567890", want: true},
		{in: "", want: false},
		{in: "12a", want: false},
		{in: "-123", want: false},
		{in: "1.5", want: false},
		{in: " 12", want: false},
	}
	for _, tc := range tests {
		if got := isAllDigits(tc.in); got != tc.want {
			t.Fatalf("isAllDigits(%q): want %v, got %v", tc.in, tc.want, got)
		}
	}
}

// ---------------------------------------------------------------------------
// 多值参数
// ---------------------------------------------------------------------------

func TestSplitCSV(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "空串返回 nil", in: "", want: nil},
		{name: "仅空白返回 nil", in: "   ", want: nil},
		{name: "单项", in: "a", want: []string{"a"}},
		{name: "多项", in: "a,b,c", want: []string{"a", "b", "c"}},
		{name: "去除各项空白", in: " a , b ", want: []string{"a", "b"}},
		{name: "丢弃空项", in: "a,,b", want: []string{"a", "b"}},
		{name: "全为空项返回 nil", in: ",,", want: nil},
		{name: "保留含空格的值内部空格", in: "a b", want: []string{"a b"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := splitCSV(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("want %v, got %v", tc.want, got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("want %v, got %v", tc.want, got)
				}
			}
		})
	}
}

func TestParseUintCSV(t *testing.T) {
	t.Parallel()

	t.Run("空返回 nil", func(t *testing.T) {
		got, err := parseUintCSV("")
		if err != nil || got != nil {
			t.Fatalf("want nil,nil got %v,%v", got, err)
		}
	})

	t.Run("单项", func(t *testing.T) {
		got, err := parseUintCSV("7")
		if err != nil || len(got) != 1 || got[0] != 7 {
			t.Fatalf("want [7], got %v,%v", got, err)
		}
	})

	t.Run("多项含 0", func(t *testing.T) {
		// 0 代表管理台 TOKEN 直连，必须可被显式选中
		got, err := parseUintCSV("0,42")
		if err != nil || len(got) != 2 || got[0] != 0 || got[1] != 42 {
			t.Fatalf("want [0 42], got %v,%v", got, err)
		}
	})

	t.Run("含空白", func(t *testing.T) {
		got, err := parseUintCSV(" 1 , 2 ")
		if err != nil || len(got) != 2 || got[0] != 1 || got[1] != 2 {
			t.Fatalf("want [1 2], got %v,%v", got, err)
		}
	})

	t.Run("非法值报错", func(t *testing.T) {
		if _, err := parseUintCSV("abc"); err == nil {
			t.Fatal("非法 key_id 应报错")
		}
	})

	t.Run("负数报错", func(t *testing.T) {
		if _, err := parseUintCSV("-1"); err == nil {
			t.Fatal("负数应报错")
		}
	})
}

// ---------------------------------------------------------------------------
// 参数解析
// ---------------------------------------------------------------------------

func TestParseStatsFilterDefaults(t *testing.T) {
	t.Parallel()

	f, err := parseStatsFilter(newCtx(t, ""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 未指定 from 时回看默认窗口
	if got := f.To.Sub(f.From); got != defaultStatsWindow {
		t.Fatalf("默认窗口应为 %v，实得 %v", defaultStatsWindow, got)
	}
	if f.Granularity != "" {
		t.Fatalf("档位缺省应为空（由聚合侧按 auto 处理），实得 %q", f.Granularity)
	}
	if f.Statuses != nil || f.Providers != nil || f.Models != nil ||
		f.Names != nil || f.UserAgents != nil || f.KeyIDs != nil {
		t.Fatal("未指定时各筛选切片应为 nil")
	}
}

func TestParseStatsFilterAllParams(t *testing.T) {
	t.Parallel()

	q := "from=1700000000&to=1700086400&granularity=1h" +
		"&status=success,error&provider=p1,p2&model=m1&name=n1&ua=ua1&key_id=0,3"
	f, err := parseStatsFilter(newCtx(t, q))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !f.From.Equal(time.Unix(1700000000, 0)) {
		t.Fatalf("From 不符：%v", f.From)
	}
	if !f.To.Equal(time.Unix(1700086400, 0)) {
		t.Fatalf("To 不符：%v", f.To)
	}
	if f.Granularity != "1h" {
		t.Fatalf("Granularity 不符：%q", f.Granularity)
	}
	if len(f.Statuses) != 2 || f.Statuses[0] != "success" || f.Statuses[1] != "error" {
		t.Fatalf("Statuses 不符：%v", f.Statuses)
	}
	if len(f.Providers) != 2 {
		t.Fatalf("Providers 不符：%v", f.Providers)
	}
	if len(f.Models) != 1 || f.Models[0] != "m1" {
		t.Fatalf("Models 不符：%v", f.Models)
	}
	if len(f.Names) != 1 || len(f.UserAgents) != 1 {
		t.Fatalf("Names/UserAgents 不符：%v %v", f.Names, f.UserAgents)
	}
	if len(f.KeyIDs) != 2 || f.KeyIDs[0] != 0 || f.KeyIDs[1] != 3 {
		t.Fatalf("KeyIDs 不符：%v", f.KeyIDs)
	}
}

func TestParseStatsFilterErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		q    string
	}{
		{name: "to 无法解析", q: "to=garbage"},
		{name: "from 无法解析", q: "from=garbage"},
		{name: "from 晚于 to", q: "from=1700086400&to=1700000000"},
		{name: "from 等于 to", q: "from=1700000000&to=1700000000"},
		{name: "非法档位", q: "granularity=3m"},
		{name: "非法 key_id", q: "key_id=abc"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := parseStatsFilter(newCtx(t, tc.q)); err == nil {
				t.Fatal("应返回错误")
			}
		})
	}
}

func TestParseStatsFilterAcceptsAllGranularities(t *testing.T) {
	t.Parallel()

	// 保证 handle 层放行的档位与聚合层已知的档位一致
	for _, g := range service.BucketGranularities() {
		q := "from=1700000000&to=1700086400&granularity=" + g
		if _, err := parseStatsFilter(newCtx(t, q)); err != nil {
			t.Fatalf("档位 %q 应被接受：%v", g, err)
		}
	}
}

// ---------------------------------------------------------------------------
// 端点
// ---------------------------------------------------------------------------

func TestStatsHandler(t *testing.T) {
	setupHandlerDB(t)

	// 时间基准取"现在"，**不写字面日期**：不带 from/to 时默认回看
	// defaultStatsWindow（7 天），钉死一个绝对日期会让这条测试写下的第七天
	// 开始失败，而且再也回不去。
	base := time.Now().Add(-time.Hour).Truncate(time.Minute)
	ok := models.ChatLog{CreatedAt: base, Status: consts.StatusSuccess, Name: "gpt-4o", ProviderName: "p1", Currency: "CNY"}
	bad := models.ChatLog{CreatedAt: base.Add(time.Minute), Status: consts.StatusError, Name: "claude", ProviderName: "p2", Error: `status: 429, body: rate limited`, Currency: "CNY"}
	for _, l := range []models.ChatLog{ok, bad} {
		row := l
		if err := models.DB.Create(&row).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/metrics/stats?granularity=1h", nil)

	Stats(c)

	if w.Code != http.StatusOK {
		t.Fatalf("HTTP 状态应为 200，实得 %d", w.Code)
	}

	var envelope struct {
		Code int `json:"code"`
		Data struct {
			KPI struct {
				Total   int64 `json:"total"`
				Success int64 `json:"success"`
				Failed  int64 `json:"failed"`
			} `json:"kpi"`
			Errors []struct {
				Code  string `json:"code"`
				Count int64  `json:"count"`
			} `json:"errors"`
			BucketMs  int64 `json:"bucketMs"`
			Truncated bool  `json:"truncated"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("反序列化响应失败：%v body=%s", err, w.Body.String())
	}

	if envelope.Code != 200 {
		t.Fatalf("业务码应为 200，实得 %d", envelope.Code)
	}
	if envelope.Data.KPI.Total != 2 || envelope.Data.KPI.Success != 1 || envelope.Data.KPI.Failed != 1 {
		t.Fatalf("KPI 不符：%+v", envelope.Data.KPI)
	}
	if envelope.Data.BucketMs != time.Hour.Milliseconds() {
		t.Fatalf("BucketMs 应为 1h，实得 %d", envelope.Data.BucketMs)
	}
	if envelope.Data.Truncated {
		t.Fatal("不应标记截断")
	}
	if len(envelope.Data.Errors) != 1 || envelope.Data.Errors[0].Code != "429" || envelope.Data.Errors[0].Count != 1 {
		t.Fatalf("错误归类不符：%+v", envelope.Data.Errors)
	}
}

func TestStatsHandlerBadRequest(t *testing.T) {
	setupHandlerDB(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/metrics/stats?granularity=bogus", nil)

	Stats(c)

	// common.BadRequest 以 HTTP 200 + 业务码 400 返回，沿用既有信封约定
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP 状态应为 200，实得 %d", w.Code)
	}
	var envelope struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("反序列化失败：%v", err)
	}
	if envelope.Code != 400 {
		t.Fatalf("业务码应为 400，实得 %d", envelope.Code)
	}
}

func TestStatsHandlerInternalError(t *testing.T) {
	setupHandlerDB(t)

	if err := models.DB.Exec("DROP TABLE chat_logs").Error; err != nil {
		t.Fatalf("drop table: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/metrics/stats", nil)

	Stats(c)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("HTTP 状态应为 500，实得 %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Failed to compute stats") {
		t.Fatalf("错误信息不符：%s", w.Body.String())
	}
}

func TestStatsGranularitiesHandler(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/metrics/granularities", nil)

	StatsGranularities(c)

	var envelope struct {
		Code int      `json:"code"`
		Data []string `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("反序列化失败：%v", err)
	}
	if envelope.Code != 200 {
		t.Fatalf("业务码应为 200，实得 %d", envelope.Code)
	}
	if len(envelope.Data) != len(service.BucketGranularities()) {
		t.Fatalf("档位数量应为 %d，实得 %d", len(service.BucketGranularities()), len(envelope.Data))
	}
	if envelope.Data[0] != "5m" {
		t.Fatalf("首个档位应为 5m，实得 %q", envelope.Data[0])
	}
}

// 保证 StatsFilter 的字段名与端点文档一致，避免改了参数名却忘了文档。
func TestStatsFilterJSONTagsUnchanged(t *testing.T) {
	t.Parallel()

	// 这些是聚合响应里前端直接消费的字段名
	want := []string{"total", "success", "successRate", "promptTokens", "cost", "currency"}
	b, err := json.Marshal(service.KPI{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range want {
		if !strings.Contains(string(b), `"`+key+`"`) {
			t.Fatalf("KPI 缺少字段 %q：%s", key, b)
		}
	}
	_ = context.Background()
}
