package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupPeakHandlerDB(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "peak-handler.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.Config{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	prev := models.DB
	models.DB = db
	service.InvalidatePeakPricing()
	t.Cleanup(func() {
		models.DB = prev
		service.InvalidatePeakPricing()
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
}

// jsonCtx 构造带 JSON 请求体的 gin 上下文。
func jsonCtx(t *testing.T, method, target string, body any) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, &buf)
	c.Request.Header.Set("Content-Type", "application/json")
	return c, w
}

func decodeEnvelope(t *testing.T, w *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
		t.Fatalf("unmarshal response: %v body=%s", err, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 配置读写
// ---------------------------------------------------------------------------

func TestGetPeakPricingHandler(t *testing.T) {
	setupPeakHandlerDB(t)

	c, w := jsonCtx(t, http.MethodGet, "/api/peak-pricing", nil)
	GetPeakPricing(c)

	var env struct {
		Code int                `json:"code"`
		Data models.PeakPricing `json:"data"`
	}
	decodeEnvelope(t, w, &env)

	if env.Code != 200 {
		t.Fatalf("业务码应为 200，实得 %d", env.Code)
	}
	// 未配置时应返回可用默认值而非空对象
	if env.Data.Enabled {
		t.Fatal("默认应为关闭")
	}
	if len(env.Data.Periods) == 0 {
		t.Fatal("默认应带示例时段，便于前端直接展示")
	}
}

func TestUpdatePeakPricingHandler(t *testing.T) {
	setupPeakHandlerDB(t)

	body := models.PeakPricing{
		Enabled:       true,
		Timezone:      "Asia/Shanghai",
		Weekdays:      []int{1, 2, 3, 4, 5},
		Periods:       []models.PeakPeriod{{Name: "夜间", Start: "00:30", End: "08:30", Multiplier: 0.25}},
		DateOverrides: map[string]string{"2026-10-10": models.DateOverrideWork},
	}
	c, w := jsonCtx(t, http.MethodPut, "/api/peak-pricing", body)
	UpdatePeakPricing(c)

	var env struct {
		Code int                `json:"code"`
		Data models.PeakPricing `json:"data"`
	}
	decodeEnvelope(t, w, &env)
	if env.Code != 200 {
		t.Fatalf("业务码应为 200，实得 %d：%s", env.Code, w.Body.String())
	}
	if !env.Data.Enabled || len(env.Data.Periods) != 1 {
		t.Fatalf("返回的应是已保存配置：%+v", env.Data)
	}

	// 重新读取应拿到同样的值（缓存已失效）
	c2, w2 := jsonCtx(t, http.MethodGet, "/api/peak-pricing", nil)
	GetPeakPricing(c2)
	var env2 struct {
		Data models.PeakPricing `json:"data"`
	}
	decodeEnvelope(t, w2, &env2)
	if !env2.Data.Enabled || env2.Data.Periods[0].Name != "夜间" {
		t.Fatalf("持久化后读取不符：%+v", env2.Data)
	}
}

func TestUpdatePeakPricingHandlerInvalidJSON(t *testing.T) {
	setupPeakHandlerDB(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/peak-pricing", strings.NewReader("{not json"))
	c.Request.Header.Set("Content-Type", "application/json")

	UpdatePeakPricing(c)

	var env struct {
		Code int `json:"code"`
	}
	decodeEnvelope(t, w, &env)
	if env.Code != 400 {
		t.Fatalf("非法 JSON 应返回 400 业务码，实得 %d", env.Code)
	}
}

func TestUpdatePeakPricingHandlerSaveError(t *testing.T) {
	setupPeakHandlerDB(t)

	if err := models.DB.Exec("DROP TABLE configs").Error; err != nil {
		t.Fatalf("drop: %v", err)
	}

	c, w := jsonCtx(t, http.MethodPut, "/api/peak-pricing", models.DefaultPeakPricing())
	UpdatePeakPricing(c)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("持久化失败应返回 500，实得 %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// 配置校验
// ---------------------------------------------------------------------------

func TestValidatePeakPricing(t *testing.T) {
	t.Parallel()

	valid := models.PeakPricing{
		Timezone: "Asia/Shanghai",
		Weekdays: []int{1, 2, 3, 4, 5},
		Periods:  []models.PeakPeriod{{Name: "ok", Start: "08:00", End: "20:00", Multiplier: 1}},
	}

	tests := []struct {
		name    string
		mutate  func(*models.PeakPricing)
		wantErr string
	}{
		{name: "合法配置", mutate: func(*models.PeakPricing) {}},
		{
			name:    "起始时间非法",
			mutate:  func(p *models.PeakPricing) { p.Periods[0].Start = "25:00" },
			wantErr: "start",
		},
		{
			name:    "结束时间非法",
			mutate:  func(p *models.PeakPricing) { p.Periods[0].End = "abc" },
			wantErr: "end",
		},
		{
			name:    "起止相同会覆盖全天",
			mutate:  func(p *models.PeakPricing) { p.Periods[0].End = p.Periods[0].Start },
			wantErr: "same",
		},
		{
			name:    "时段星期越界",
			mutate:  func(p *models.PeakPricing) { p.Periods[0].Days = []int{7} },
			wantErr: "weekday 7 out of range",
		},
		{
			name:    "全局星期越界",
			mutate:  func(p *models.PeakPricing) { p.Weekdays = []int{-1} },
			wantErr: "weekday -1 out of range",
		},
		{
			name: "日期覆盖键非法",
			mutate: func(p *models.PeakPricing) {
				p.DateOverrides = map[string]string{"2026/10/01": models.DateOverrideRest}
			},
			wantErr: "invalid date override key",
		},
		{
			name: "日期覆盖值非法",
			mutate: func(p *models.PeakPricing) {
				p.DateOverrides = map[string]string{"2026-10-01": "holiday"}
			},
			wantErr: "invalid date override for",
		},
		{
			name:    "时区非法",
			mutate:  func(p *models.PeakPricing) { p.Timezone = "Mars/Olympus" },
			wantErr: "invalid timezone",
		},
		{
			name:    "空时区允许（回落本地时区）",
			mutate:  func(p *models.PeakPricing) { p.Timezone = "" },
			wantErr: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := valid
			cfg.Periods = append([]models.PeakPeriod(nil), valid.Periods...)
			tc.mutate(&cfg)

			err := validatePeakPricing(cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("应通过校验，实得 %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("应校验失败")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("错误信息应含 %q，实得 %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestUpdatePeakPricingHandlerRejectsInvalidConfig(t *testing.T) {
	setupPeakHandlerDB(t)

	body := models.PeakPricing{
		Timezone: "Asia/Shanghai",
		Periods:  []models.PeakPeriod{{Name: "坏", Start: "10:00", End: "10:00"}},
	}
	c, w := jsonCtx(t, http.MethodPut, "/api/peak-pricing", body)
	UpdatePeakPricing(c)

	var env struct {
		Code int `json:"code"`
	}
	decodeEnvelope(t, w, &env)
	if env.Code != 400 {
		t.Fatalf("非法配置应被拦下并返回 400，实得 %d", env.Code)
	}
}

// ---------------------------------------------------------------------------
// 时间轴预览
// ---------------------------------------------------------------------------

func TestPreviewPeakPricingHandler(t *testing.T) {
	setupPeakHandlerDB(t)

	body := models.PeakPricing{
		Enabled:  true,
		Timezone: "Asia/Shanghai",
		Periods: []models.PeakPeriod{
			{Name: "夜间", Start: "00:00", End: "08:00", Multiplier: 0.25},
			{Name: "白天", Start: "08:00", End: "24:00", Multiplier: 1},
		},
	}
	c, w := jsonCtx(t, http.MethodPost, "/api/peak-pricing/preview?days=1", body)
	PreviewPeakPricing(c)

	var env struct {
		Code int                     `json:"code"`
		Data []service.SchedulePoint `json:"data"`
	}
	decodeEnvelope(t, w, &env)

	if env.Code != 200 {
		t.Fatalf("业务码应为 200，实得 %d：%s", env.Code, w.Body.String())
	}
	if len(env.Data) < 2 {
		t.Fatalf("一天两个时段应至少切出 2 段：%+v", env.Data)
	}

	// 预览从"当前时刻"起算而非零点，因此不能断言首段是哪个时段。
	// 改为断言两个时段都出现且各自乘数正确。
	byName := map[string]float64{}
	for _, p := range env.Data {
		byName[p.Period] = p.Multiplier
	}
	if byName["夜间"] != 0.25 {
		t.Fatalf("夜间时段乘数应为 0.25：%+v", env.Data)
	}
	if byName["白天"] != 1 {
		t.Fatalf("白天时段乘数应为 1：%+v", env.Data)
	}

	// 各段首尾相接且覆盖所请求的跨度
	for i := 1; i < len(env.Data); i++ {
		if env.Data[i].Start != env.Data[i-1].End {
			t.Fatalf("相邻段应首尾相接：%+v", env.Data)
		}
	}
	span := env.Data[len(env.Data)-1].End - env.Data[0].Start
	if span < (23 * time.Hour).Milliseconds() {
		t.Fatalf("预览 1 天应覆盖近 24 小时，实得 %d ms", span)
	}
}

func TestPreviewPeakPricingHandlerInvalidDays(t *testing.T) {
	setupPeakHandlerDB(t)

	for _, days := range []string{"0", "-1", "32", "abc"} {
		c, w := jsonCtx(t, http.MethodPost, "/api/peak-pricing/preview?days="+days, models.DefaultPeakPricing())
		PreviewPeakPricing(c)

		var env struct {
			Code int `json:"code"`
		}
		decodeEnvelope(t, w, &env)
		if env.Code != 400 {
			t.Fatalf("days=%s 应被拒绝，实得业务码 %d", days, env.Code)
		}
	}
}

func TestPreviewPeakPricingHandlerInvalidBody(t *testing.T) {
	setupPeakHandlerDB(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/peak-pricing/preview", strings.NewReader("{"))
	c.Request.Header.Set("Content-Type", "application/json")

	PreviewPeakPricing(c)

	var env struct {
		Code int `json:"code"`
	}
	decodeEnvelope(t, w, &env)
	if env.Code != 400 {
		t.Fatalf("非法请求体应返回 400，实得 %d", env.Code)
	}
}

func TestPreviewPeakPricingHandlerInvalidConfig(t *testing.T) {
	setupPeakHandlerDB(t)

	body := models.PeakPricing{Periods: []models.PeakPeriod{{Name: "坏", Start: "99:99", End: "10:00"}}}
	c, w := jsonCtx(t, http.MethodPost, "/api/peak-pricing/preview", body)
	PreviewPeakPricing(c)

	var env struct {
		Code int `json:"code"`
	}
	decodeEnvelope(t, w, &env)
	if env.Code != 400 {
		t.Fatalf("非法配置应返回 400，实得 %d", env.Code)
	}
}

// ---------------------------------------------------------------------------
// 节假日同步
// ---------------------------------------------------------------------------

func TestSyncPeakHolidaysHandler(t *testing.T) {
	setupPeakHandlerDB(t)

	// 内置数据覆盖 2024-2026，因此该年份即使离线也应同步成功
	c, w := jsonCtx(t, http.MethodPost, "/api/peak-pricing/holidays/sync?year=2026", nil)
	SyncPeakHolidays(c)

	var env struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Year   int                `json:"year"`
			Count  int                `json:"count"`
			Source string             `json:"source"`
			Config models.PeakPricing `json:"config"`
		} `json:"data"`
	}
	decodeEnvelope(t, w, &env)

	if env.Code != 200 {
		t.Fatalf("业务码应为 200，实得 %d：%s", env.Code, w.Body.String())
	}
	if env.Message != "synced" {
		t.Fatalf("消息应为 synced，实得 %q", env.Message)
	}
	if env.Data.Year != 2026 || env.Data.Count == 0 {
		t.Fatalf("同步结果不符：%+v", env.Data)
	}
	if env.Data.Source == "" {
		t.Fatal("应回传数据来源以便界面展示可信度")
	}
	// 调休日必须在结果里
	if env.Data.Config.DateOverrides["2026-10-10"] != models.DateOverrideWork {
		t.Fatalf("调休日未写入：%v", env.Data.Config.DateOverrides)
	}
}

func TestSyncPeakHolidaysHandlerInvalidYear(t *testing.T) {
	setupPeakHandlerDB(t)

	for _, year := range []string{"1999", "2101", "abc"} {
		c, w := jsonCtx(t, http.MethodPost, "/api/peak-pricing/holidays/sync?year="+year, nil)
		SyncPeakHolidays(c)

		var env struct {
			Code int `json:"code"`
		}
		decodeEnvelope(t, w, &env)
		if env.Code != 400 {
			t.Fatalf("year=%s 应被拒绝，实得业务码 %d", year, env.Code)
		}
	}
}

func TestSyncPeakHolidaysHandlerUpstreamFailure(t *testing.T) {
	setupPeakHandlerDB(t)

	// 把远端指向必然失败的地址，并请求一个未内置的年份 →
	// 远端与内置都不可用，应返回 502（失败源自外部数据源，非本服务内部错误）
	prev := service.HolidaySourceURLs
	service.HolidaySourceURLs = []string{"http://127.0.0.1:1/%d.json"}
	t.Cleanup(func() { service.HolidaySourceURLs = prev })

	ctx := context.Background()
	_ = ctx

	c, w := jsonCtx(t, http.MethodPost, "/api/peak-pricing/holidays/sync?year=2020", nil)
	SyncPeakHolidays(c)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("外部数据源失败应返回 502，实得 %d：%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Failed to sync holidays") {
		t.Fatalf("错误信息不符：%s", w.Body.String())
	}
}

// 配置写入后必须让缓存失效，否则界面改完要等 TTL 过期才生效。
func TestUpdateConfigByKeyInvalidatesPeakCache(t *testing.T) {
	setupPeakHandlerDB(t)

	// 先读一次把默认值灌进缓存
	c0, _ := jsonCtx(t, http.MethodGet, "/api/peak-pricing", nil)
	GetPeakPricing(c0)

	// 通过通用 config 端点写入启用状态
	cfg := models.DefaultPeakPricing()
	cfg.Enabled = true
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/config/"+models.KeyPeakPricing,
		strings.NewReader(`{"value":`+string(mustJSONString(t, string(raw)))+`}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "key", Value: models.KeyPeakPricing}}
	UpdateConfigByKey(c)

	if w.Code != http.StatusOK {
		t.Fatalf("写入配置失败：%d %s", w.Code, w.Body.String())
	}

	// 缓存应已失效，紧接着读取必须拿到新值
	got := service.GetPeakPricing(context.Background())
	if !got.Enabled {
		t.Fatal("配置写入后缓存未失效，新值要等 TTL 才生效")
	}
}

func mustJSONString(t *testing.T, s string) []byte {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal string: %v", err)
	}
	return b
}

var _ = time.Now
