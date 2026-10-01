package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// 峰谷的两半各有归属，测试也按这条线分开：
//
//	工作日日历（时区 / 星期几 / 日期覆盖 / 节假日同步）是全局的 → /api/peak-calendar
//	计费条款（开关 + 时段）是**上游的商务条款** → 挂在「模型 × 上游」关联行上
//
// 因此除了日历端点的四态，这里还要钉住"条款确实随关联落库、也确实能被清掉"。

func setupPeakHandlerDB(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "peak-handler.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.Config{}, &models.ModelWithProvider{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	prev := models.DB
	models.DB = db
	service.InvalidatePeakCalendar()
	t.Cleanup(func() {
		models.DB = prev
		service.InvalidatePeakCalendar()
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

// withPathParam 补上路径参数（jsonCtx 只造请求体，路径由路由器负责，
// 这里不经路由器直接调 handler，需手动挂上）。
func withPathParam(c *gin.Context, key, value string) {
	c.Params = gin.Params{{Key: key, Value: value}}
}

// offlineHolidays 把节假日数据源指向必然连不上的地址，
// 逼取数走内置数据：既离线又确定，结论不跟着外网变。
func offlineHolidays(t *testing.T) {
	t.Helper()
	prev := service.HolidaySourceURLs
	service.HolidaySourceURLs = []string{"http://127.0.0.1:1/%d.json"}
	t.Cleanup(func() { service.HolidaySourceURLs = prev })
}

func peakBool(v bool) *bool { return &v }

// ---------------------------------------------------------------------------
// 全局工作日日历
// ---------------------------------------------------------------------------

func TestGetPeakCalendarHandler(t *testing.T) {
	setupPeakHandlerDB(t)

	c, w := jsonCtx(t, http.MethodGet, "/api/peak-calendar", nil)
	GetPeakCalendar(c)

	var env struct {
		Code int                 `json:"code"`
		Data models.PeakCalendar `json:"data"`
	}
	decodeEnvelope(t, w, &env)

	if env.Code != 200 {
		t.Fatalf("业务码应为 200，实得 %d", env.Code)
	}
	// 未配置时应返回可用默认值而非空对象
	if env.Data.Timezone == "" {
		t.Fatal("默认应带时区：留空会让部署机时区悄悄决定峰谷窗口落在哪几个小时")
	}
	if len(env.Data.Weekdays) == 0 {
		t.Fatal("默认应带工作日定义")
	}
	if env.Data.DateOverrides == nil {
		t.Fatal("默认应带非 nil 的覆盖表，省得每个调用方各判一次空")
	}
}

func TestUpdatePeakCalendarHandler(t *testing.T) {
	setupPeakHandlerDB(t)

	// 先读一次把默认日历灌进缓存：写入端点若不失效缓存，下面的读取会拿到它
	if warm := service.GetPeakCalendar(context.Background()); warm.Timezone == "UTC" {
		t.Fatal("前置条件不成立：默认日历不该已经是 UTC")
	}

	body := models.PeakCalendar{
		Timezone:      "UTC",
		Weekdays:      []int{1, 3, 5},
		DateOverrides: map[string]string{"2026-10-10": models.DateOverrideWork},
	}
	c, w := jsonCtx(t, http.MethodPut, "/api/peak-calendar", body)
	UpdatePeakCalendar(c)

	var env struct {
		Code int                 `json:"code"`
		Data models.PeakCalendar `json:"data"`
	}
	decodeEnvelope(t, w, &env)
	if env.Code != 200 {
		t.Fatalf("业务码应为 200，实得 %d：%s", env.Code, w.Body.String())
	}
	if env.Data.Timezone != "UTC" || len(env.Data.Weekdays) != 3 {
		t.Fatalf("返回的应是已保存日历：%+v", env.Data)
	}

	// 重新读取应拿到同样的值（写入时缓存已失效）
	c2, w2 := jsonCtx(t, http.MethodGet, "/api/peak-calendar", nil)
	GetPeakCalendar(c2)
	var env2 struct {
		Data models.PeakCalendar `json:"data"`
	}
	decodeEnvelope(t, w2, &env2)
	if env2.Data.DateOverrides["2026-10-10"] != models.DateOverrideWork {
		t.Fatalf("持久化后读取不符：%+v", env2.Data)
	}
}

func TestUpdatePeakCalendarHandlerInvalidJSON(t *testing.T) {
	setupPeakHandlerDB(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/peak-calendar", strings.NewReader("{not json"))
	c.Request.Header.Set("Content-Type", "application/json")

	UpdatePeakCalendar(c)

	var env struct {
		Code int `json:"code"`
	}
	decodeEnvelope(t, w, &env)
	if env.Code != 400 {
		t.Fatalf("非法 JSON 应返回 400 业务码，实得 %d", env.Code)
	}
}

func TestUpdatePeakCalendarHandlerSaveError(t *testing.T) {
	setupPeakHandlerDB(t)

	if err := models.DB.Exec("DROP TABLE configs").Error; err != nil {
		t.Fatalf("drop: %v", err)
	}

	c, w := jsonCtx(t, http.MethodPut, "/api/peak-calendar", models.DefaultPeakCalendar())
	UpdatePeakCalendar(c)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("持久化失败应返回 500，实得 %d", w.Code)
	}
}

func TestUpdatePeakCalendarHandlerRejectsInvalidCalendar(t *testing.T) {
	setupPeakHandlerDB(t)

	body := models.PeakCalendar{Timezone: "Mars/Olympus"}
	c, w := jsonCtx(t, http.MethodPut, "/api/peak-calendar", body)
	UpdatePeakCalendar(c)

	var env struct {
		Code int `json:"code"`
	}
	decodeEnvelope(t, w, &env)
	if env.Code != 400 {
		t.Fatalf("非法日历应被拦下并返回 400，实得 %d", env.Code)
	}
}

// ---------------------------------------------------------------------------
// 校验
// ---------------------------------------------------------------------------

func TestValidatePeakTerms(t *testing.T) {
	t.Parallel()

	valid := models.PeakTerms{
		Enabled: true,
		Periods: []models.PeakPeriod{{Name: "ok", Start: "08:00", End: "20:00", Multiplier: 1}},
	}

	tests := []struct {
		name    string
		mutate  func(*models.PeakTerms)
		wantErr string
	}{
		{name: "合法条款", mutate: func(*models.PeakTerms) {}},
		{
			name:    "起始时间非法",
			mutate:  func(p *models.PeakTerms) { p.Periods[0].Start = "25:00" },
			wantErr: "start",
		},
		{
			name:    "结束时间非法",
			mutate:  func(p *models.PeakTerms) { p.Periods[0].End = "abc" },
			wantErr: "end",
		},
		{
			name:    "起止相同会覆盖全天",
			mutate:  func(p *models.PeakTerms) { p.Periods[0].End = p.Periods[0].Start },
			wantErr: "same",
		},
		{
			name:    "时段星期越界",
			mutate:  func(p *models.PeakTerms) { p.Periods[0].Days = []int{7} },
			wantErr: "weekday 7 out of range",
		},
		{
			// 关掉的条款也照拦：否则用户填错后一开开关就是一段坏配置生效，
			// 而错误要到算出成本时才会被发现
			name:    "关闭状态的坏条款同样拦下",
			mutate:  func(p *models.PeakTerms) { p.Enabled = false; p.Periods[0].End = p.Periods[0].Start },
			wantErr: "same",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := valid
			cfg.Periods = append([]models.PeakPeriod(nil), valid.Periods...)
			tc.mutate(&cfg)

			err := validatePeakTerms(cfg)
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

func TestValidatePeakCalendar(t *testing.T) {
	t.Parallel()

	valid := models.PeakCalendar{
		Timezone:      "Asia/Shanghai",
		Weekdays:      []int{1, 2, 3, 4, 5},
		DateOverrides: map[string]string{"2026-10-10": models.DateOverrideWork},
	}

	tests := []struct {
		name    string
		mutate  func(*models.PeakCalendar)
		wantErr string
	}{
		{name: "合法日历", mutate: func(*models.PeakCalendar) {}},
		{
			name:    "工作日星期越界",
			mutate:  func(p *models.PeakCalendar) { p.Weekdays = []int{-1} },
			wantErr: "weekday -1 out of range",
		},
		{
			name: "日期覆盖键非法",
			mutate: func(p *models.PeakCalendar) {
				p.DateOverrides = map[string]string{"2026/10/01": models.DateOverrideRest}
			},
			wantErr: "invalid date override key",
		},
		{
			name: "日期覆盖值非法",
			mutate: func(p *models.PeakCalendar) {
				p.DateOverrides = map[string]string{"2026-10-01": "holiday"}
			},
			wantErr: "invalid date override for",
		},
		{
			name:    "时区非法",
			mutate:  func(p *models.PeakCalendar) { p.Timezone = "Mars/Olympus" },
			wantErr: "invalid timezone",
		},
		{
			name:    "空时区允许（回落本地时区）",
			mutate:  func(p *models.PeakCalendar) { p.Timezone = "" },
			wantErr: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := valid
			cfg.Weekdays = append([]int(nil), valid.Weekdays...)
			tc.mutate(&cfg)

			err := validatePeakCalendar(cfg)
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

// ---------------------------------------------------------------------------
// 时间轴预览
// ---------------------------------------------------------------------------

func TestPreviewPeakTermsHandler(t *testing.T) {
	setupPeakHandlerDB(t)

	// 预览按**已保存**的日历算，条款来自请求体
	if err := service.SavePeakCalendar(context.Background(), models.DefaultPeakCalendar()); err != nil {
		t.Fatalf("seed calendar: %v", err)
	}

	body := models.PeakTerms{
		Enabled: true,
		Periods: []models.PeakPeriod{
			{Name: "夜间", Start: "00:00", End: "08:00", Multiplier: 0.25},
			{Name: "白天", Start: "08:00", End: "24:00", Multiplier: 1},
		},
	}
	c, w := jsonCtx(t, http.MethodPost, "/api/peak-calendar/preview?days=1", body)
	PreviewPeakTerms(c)

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

// 预览的日历必须取自库里那份：条款在别的页面存、日历在全局维护，
// 若这里改用内置默认日历，用户改完日历回到编辑器预览时看到的会是另一套。
//
// 构造：把"每天都是工作日"存进日历，再要求时段只在工作日生效。
// 默认日历（周一~周五）下 7 天的窗口必然被周末切成多段，因此段数就是判据。
func TestPreviewPeakTermsHandlerUsesSavedCalendar(t *testing.T) {
	setupPeakHandlerDB(t)

	cal := models.DefaultPeakCalendar()
	cal.Weekdays = []int{0, 1, 2, 3, 4, 5, 6}
	if err := service.SavePeakCalendar(context.Background(), cal); err != nil {
		t.Fatalf("seed calendar: %v", err)
	}

	body := models.PeakTerms{
		Enabled: true,
		Periods: []models.PeakPeriod{{
			Name: "全天", Start: "00:00", End: "24:00", Multiplier: 1.5,
			Workday: peakBool(true),
		}},
	}

	// 不传 days，走默认的 7 天：一周里必然包含周末
	c, w := jsonCtx(t, http.MethodPost, "/api/peak-calendar/preview", body)
	PreviewPeakTerms(c)

	var env struct {
		Code int                     `json:"code"`
		Data []service.SchedulePoint `json:"data"`
	}
	decodeEnvelope(t, w, &env)
	if env.Code != 200 {
		t.Fatalf("业务码应为 200，实得 %d：%s", env.Code, w.Body.String())
	}
	if len(env.Data) != 1 {
		t.Fatalf("按已保存的日历（每天都是工作日）整周应合并成一段，实得 %d 段：%+v", len(env.Data), env.Data)
	}
	if env.Data[0].Multiplier != 1.5 || !env.Data[0].Workday {
		t.Fatalf("整段都应命中全天时段且为工作日：%+v", env.Data[0])
	}
}

func TestPreviewPeakTermsHandlerInvalidDays(t *testing.T) {
	setupPeakHandlerDB(t)

	for _, days := range []string{"0", "-1", "32", "abc"} {
		c, w := jsonCtx(t, http.MethodPost, "/api/peak-calendar/preview?days="+days, models.DefaultPeakTerms())
		PreviewPeakTerms(c)

		var env struct {
			Code int `json:"code"`
		}
		decodeEnvelope(t, w, &env)
		if env.Code != 400 {
			t.Fatalf("days=%s 应被拒绝，实得业务码 %d", days, env.Code)
		}
	}
}

func TestPreviewPeakTermsHandlerInvalidBody(t *testing.T) {
	setupPeakHandlerDB(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/peak-calendar/preview", strings.NewReader("{"))
	c.Request.Header.Set("Content-Type", "application/json")

	PreviewPeakTerms(c)

	var env struct {
		Code int `json:"code"`
	}
	decodeEnvelope(t, w, &env)
	if env.Code != 400 {
		t.Fatalf("非法请求体应返回 400，实得 %d", env.Code)
	}
}

func TestPreviewPeakTermsHandlerInvalidTerms(t *testing.T) {
	setupPeakHandlerDB(t)

	body := models.PeakTerms{Periods: []models.PeakPeriod{{Name: "坏", Start: "99:99", End: "10:00"}}}
	c, w := jsonCtx(t, http.MethodPost, "/api/peak-calendar/preview", body)
	PreviewPeakTerms(c)

	var env struct {
		Code int `json:"code"`
	}
	decodeEnvelope(t, w, &env)
	if env.Code != 400 {
		t.Fatalf("非法条款应返回 400，实得 %d", env.Code)
	}
}

// ---------------------------------------------------------------------------
// 节假日同步
// ---------------------------------------------------------------------------

func TestSyncPeakHolidaysHandler(t *testing.T) {
	setupPeakHandlerDB(t)
	offlineHolidays(t)

	// 内置数据覆盖 2024-2026，因此该年份即使离线也应同步成功
	c, w := jsonCtx(t, http.MethodPost, "/api/peak-calendar/holidays/sync?year=2026", nil)
	SyncPeakHolidays(c)

	var env struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Year     int                 `json:"year"`
			Count    int                 `json:"count"`
			Source   string              `json:"source"`
			Calendar models.PeakCalendar `json:"calendar"`
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
	// 数据源强制指向必然失败的地址，来源必然是内置那份；
	// 也顺带钉住"同步的是日历本身"而不是别的载荷
	if !strings.HasPrefix(env.Data.Source, "bundled:") {
		t.Fatalf("来源应为内置数据，实得 %q", env.Data.Source)
	}
	// 调休日必须在结果里
	if env.Data.Calendar.DateOverrides["2026-10-10"] != models.DateOverrideWork {
		t.Fatalf("调休日未写入：%v", env.Data.Calendar.DateOverrides)
	}

	// 同步结果要立刻对热路径可见（写入时缓存已失效）
	saved := service.GetPeakCalendar(context.Background())
	if saved.DateOverrides["2026-10-10"] != models.DateOverrideWork {
		t.Fatalf("同步后日历缓存未失效：%+v", saved.DateOverrides)
	}
}

// 不传年份时按当前年份同步——这个分支此前没有覆盖，
// 而它正是前端"一键同步今年"按钮走的那条路。
func TestSyncPeakHolidaysHandlerDefaultYear(t *testing.T) {
	setupPeakHandlerDB(t)

	// 假数据源按请求的年份回数据：既不依赖内置数据覆盖到哪一年，
	// 也不用联网
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		year := strings.TrimSuffix(filepath.Base(r.URL.Path), ".json")
		_, _ = w.Write([]byte(`{"year":` + year + `,"days":[{"date":"` + year + `-01-01","name":"元旦","isOffDay":true}]}`))
	}))
	defer srv.Close()

	prev := service.HolidaySourceURLs
	service.HolidaySourceURLs = []string{srv.URL + "/%d.json"}
	t.Cleanup(func() { service.HolidaySourceURLs = prev })

	c, w := jsonCtx(t, http.MethodPost, "/api/peak-calendar/holidays/sync", nil)
	SyncPeakHolidays(c)

	var env struct {
		Code int `json:"code"`
		Data struct {
			Year   int    `json:"year"`
			Count  int    `json:"count"`
			Source string `json:"source"`
		} `json:"data"`
	}
	decodeEnvelope(t, w, &env)

	if env.Code != 200 {
		t.Fatalf("业务码应为 200，实得 %d：%s", env.Code, w.Body.String())
	}
	if env.Data.Year != time.Now().Year() {
		t.Fatalf("不传年份时应同步当前年份，实得 %d", env.Data.Year)
	}
	if env.Data.Count != 1 {
		t.Fatalf("应写入 1 条覆盖，实得 %d", env.Data.Count)
	}
}

func TestSyncPeakHolidaysHandlerInvalidYear(t *testing.T) {
	setupPeakHandlerDB(t)

	for _, year := range []string{"1999", "2101", "abc"} {
		c, w := jsonCtx(t, http.MethodPost, "/api/peak-calendar/holidays/sync?year="+year, nil)
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

	c, w := jsonCtx(t, http.MethodPost, "/api/peak-calendar/holidays/sync?year=2020", nil)
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
	c0, _ := jsonCtx(t, http.MethodGet, "/api/peak-calendar", nil)
	GetPeakCalendar(c0)

	// 通过通用 config 端点写入日历
	cfg := models.DefaultPeakCalendar()
	cfg.Timezone = "UTC"
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/config/"+models.KeyPeakCalendar,
		strings.NewReader(`{"value":`+string(mustJSONString(t, string(raw)))+`}`))
	c.Request.Header.Set("Content-Type", "application/json")
	withPathParam(c, "key", models.KeyPeakCalendar)
	UpdateConfigByKey(c)

	if w.Code != http.StatusOK {
		t.Fatalf("写入配置失败：%d %s", w.Code, w.Body.String())
	}

	// 缓存应已失效，紧接着读取必须拿到新值
	got := service.GetPeakCalendar(context.Background())
	if got.Timezone != "UTC" {
		t.Fatalf("配置写入后缓存未失效，新值要等 TTL 才生效：%+v", got)
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

// ---------------------------------------------------------------------------
// 条款随「模型 × 上游」关联走
// ---------------------------------------------------------------------------

// peakBody 造一份关联请求体；peak 为 nil 时整个字段缺席（= 没配峰谷）。
func peakBody(providerModel string, peak map[string]any) map[string]any {
	body := map[string]any{
		"model_id":         1,
		"provider_id":      2,
		"provider_name":    providerModel,
		"weight":           1,
		"input_price":      1.0,
		"cache_read_price": 0.1,
		"output_price":     2.0,
	}
	if peak != nil {
		body["peak"] = peak
	}
	return body
}

func TestCreateModelProviderKeepsPeakTerms(t *testing.T) {
	setupPeakHandlerDB(t)

	body := peakBody("gpt-4o", map[string]any{
		"enabled": true,
		"periods": []map[string]any{
			{"name": "夜间", "start": "00:30", "end": "08:30", "multiplier": 0.25},
		},
	})
	c, w := jsonCtx(t, http.MethodPost, "/api/model-providers", body)
	CreateModelProvider(c)

	if w.Code != http.StatusOK {
		t.Fatalf("创建应成功，实得 %d：%s", w.Code, w.Body.String())
	}

	// 条款要落在这一条关联自己的行上，而不是某个全局配置里
	row, err := gorm.G[models.ModelWithProvider](models.DB).
		Where("provider_model = ?", "gpt-4o").First(context.Background())
	if err != nil {
		t.Fatalf("读回关联：%v", err)
	}
	if row.Peak == nil || !row.Peak.Enabled || len(row.Peak.Periods) != 1 {
		t.Fatalf("条款未随关联落库：%+v", row.Peak)
	}
	if row.Peak.Periods[0].Multiplier != 0.25 {
		t.Fatalf("乘数不符：%+v", row.Peak.Periods[0])
	}
}

func TestUpdateModelProviderKeepsPeakTerms(t *testing.T) {
	setupPeakHandlerDB(t)

	seed := models.ModelWithProvider{ModelID: 1, ProviderID: 2, ProviderModel: "gpt-4o"}
	if err := models.DB.Create(&seed).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	body := peakBody("gpt-4o", map[string]any{
		"enabled": true,
		"periods": []map[string]any{
			{"name": "峰时", "start": "09:00", "end": "18:00", "multiplier": 1.5},
		},
	})
	c, w := jsonCtx(t, http.MethodPut, "/api/model-providers/"+strconv.FormatUint(uint64(seed.ID), 10), body)
	withPathParam(c, "id", strconv.FormatUint(uint64(seed.ID), 10))
	UpdateModelProvider(c)

	if w.Code != http.StatusOK {
		t.Fatalf("更新应成功，实得 %d：%s", w.Code, w.Body.String())
	}

	row, err := gorm.G[models.ModelWithProvider](models.DB).
		Where("id = ?", seed.ID).First(context.Background())
	if err != nil {
		t.Fatalf("读回关联：%v", err)
	}
	if row.Peak == nil || len(row.Peak.Periods) != 1 || row.Peak.Periods[0].Multiplier != 1.5 {
		t.Fatalf("条款未随更新落库：%+v", row.Peak)
	}
}

// 关掉峰谷（请求体不带 peak）之后，旧条款必须从库里消失。
//
// 这条钉的是一个真实的陷阱：GORM 的结构体更新跳过零值，而"没配峰谷"
// 正好就是一个 nil 指针，于是它会被整条跳过——条款会留在行上，
// 用户下次打开开关时拿回一份早已作废的陈年配置。
func TestUpdateModelProviderClearsPeakTerms(t *testing.T) {
	setupPeakHandlerDB(t)

	terms := models.PeakTerms{
		Enabled: true,
		Periods: []models.PeakPeriod{{Name: "夜间", Start: "00:30", End: "08:30", Multiplier: 0.25}},
	}
	seed := models.ModelWithProvider{
		ModelID: 1, ProviderID: 2, ProviderModel: "gpt-4o", Peak: &terms,
	}
	if err := models.DB.Create(&seed).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	c, w := jsonCtx(t, http.MethodPut, "/api/model-providers/"+strconv.FormatUint(uint64(seed.ID), 10),
		peakBody("gpt-4o", nil))
	withPathParam(c, "id", strconv.FormatUint(uint64(seed.ID), 10))
	UpdateModelProvider(c)

	if w.Code != http.StatusOK {
		t.Fatalf("更新应成功，实得 %d：%s", w.Code, w.Body.String())
	}

	row, err := gorm.G[models.ModelWithProvider](models.DB).
		Where("id = ?", seed.ID).First(context.Background())
	if err != nil {
		t.Fatalf("读回关联：%v", err)
	}
	if row.Peak != nil {
		t.Fatalf("请求未带 peak 时旧条款应被清掉，实得 %+v", row.Peak)
	}
}
