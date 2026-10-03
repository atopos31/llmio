package service

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/atopos31/llmio/models"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// 补齐容错分支
// ---------------------------------------------------------------------------

func TestParseClockMinuteNotNumeric(t *testing.T) {
	t.Parallel()

	// 小时可解析、分钟不可解析：必须走到分钟的错误分支，
	// 而不是被小时的分支兜住
	if _, err := ParseClock("08:xx"); err == nil {
		t.Fatal("非法分钟应报错")
	}
}

func TestResolvePeriodInvalidEndClockSkipped(t *testing.T) {
	t.Parallel()

	// Start 合法、End 非法：应跳过该时段继续匹配后续时段
	cfg := peakFixture{
		Enabled: true,
		Periods: []models.PeakPeriod{
			{Name: "起点合法终点坏", Start: "00:00", End: "25:99", Multiplier: 9},
			{Name: "正常", Start: "00:00", End: "24:00", Multiplier: 1.5},
		},
	}
	p := resolveWith(cfg).ResolvePeriod(sh(t, 2026, 9, 28, 12, 0))
	if p == nil || p.Name != "正常" {
		t.Fatalf("应跳过终点非法的时段，实得 %+v", p)
	}
}

func TestLoadPeakCalendarNormalizesMissingOverrides(t *testing.T) {
	setupPeakDB(t)

	// 存量配置可能没有 dateOverrides 字段（更早版本写入，或手工编辑省略），
	// 读取时必须补齐为非 nil，否则调用方要做额外的空值判断
	if err := models.DB.Create(&models.Config{
		Key:   models.KeyPeakCalendar,
		Value: `{"timezone":"UTC"}`,
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	InvalidatePeakCalendar()

	cfg := GetPeakCalendar(context.Background())
	if cfg.Timezone != "UTC" {
		t.Fatalf("配置未正确读取：%+v", cfg)
	}
	if cfg.DateOverrides == nil {
		t.Fatal("缺失 dateOverrides 时应补齐为非 nil map")
	}
}

func TestSavePeakCalendarUpdateBranchError(t *testing.T) {
	setupPeakDB(t)
	ctx := context.Background()

	// 先建行，使后续写入走更新分支
	if err := SavePeakCalendar(ctx, models.DefaultPeakCalendar()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 用回调强制 Update 失败
	name := "test:fail-update"
	models.DB.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		tx.AddError(errors.New("forced update failure"))
	})
	t.Cleanup(func() { models.DB.Callback().Update().Remove(name) })

	if err := SavePeakCalendar(ctx, models.DefaultPeakCalendar()); err == nil {
		t.Fatal("更新失败时应返回错误")
	}
}

func TestSavePeakCalendarCreateBranchError(t *testing.T) {
	setupPeakDB(t)

	name := "test:fail-create"
	models.DB.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
		tx.AddError(errors.New("forced create failure"))
	})
	t.Cleanup(func() { models.DB.Callback().Create().Remove(name) })

	if err := SavePeakCalendar(context.Background(), models.DefaultPeakCalendar()); err == nil {
		t.Fatal("创建失败时应返回错误")
	}
}

func TestSavePeakCalendarQueryError(t *testing.T) {
	setupPeakDB(t)

	// 删表后查询返回的不是 ErrRecordNotFound 而是"表不存在"，
	// 应走 default 分支原样透出错误
	if err := models.DB.Exec("DROP TABLE configs").Error; err != nil {
		t.Fatalf("drop: %v", err)
	}

	if err := SavePeakCalendar(context.Background(), models.DefaultPeakCalendar()); err == nil {
		t.Fatal("查询失败时应返回错误")
	}
}

func TestSyncHolidaysSaveFailurePropagates(t *testing.T) {
	setupPeakDB(t)
	ctx := context.Background()

	fake := func(_ context.Context, _ int) ([]models.HolidayDay, string, error) {
		return []models.HolidayDay{{Date: "2026-10-01", Name: "国庆", IsOffDay: true}}, "fake", nil
	}

	// 让保存阶段失败：删表后 SavePeakPricing 的查询会报"表不存在"
	if err := models.DB.Exec("DROP TABLE configs").Error; err != nil {
		t.Fatalf("drop: %v", err)
	}

	if _, _, err := SyncHolidays(ctx, 2026, fake); err == nil {
		t.Fatal("持久化失败时同步应返回错误")
	}
}

// ---------------------------------------------------------------------------
// 内置数据源的容错分支（通过注入 fs.FS 覆盖）
// ---------------------------------------------------------------------------

// withHolidayFS 临时替换内置数据源句柄。
func withHolidayFS(t *testing.T, f fstest.MapFS) {
	t.Helper()
	prev := embeddedHolidayFS
	embeddedHolidayFS = f
	t.Cleanup(func() { embeddedHolidayFS = prev })
}

func TestEmbeddedHolidayYearsSkipsNonYearEntries(t *testing.T) {
	withHolidayFS(t, fstest.MapFS{
		"holidays/2026.json":  &fstest.MapFile{Data: []byte(`{"year":2026,"days":[{"date":"2026-01-01","isOffDay":true}]}`)},
		"holidays/README.txt": &fstest.MapFile{Data: []byte("not a year")},
		// 必须带 fs.ModeDir，否则 fstest 不会把它当成目录
		"holidays/sub": &fstest.MapFile{Mode: fs.ModeDir | 0o755},
	})

	years := EmbeddedHolidayYears()
	if len(years) != 1 || years[0] != 2026 {
		t.Fatalf("应只识别出 2026，实得 %v", years)
	}
}

func TestEmbeddedHolidayYearsMissingDir(t *testing.T) {
	withHolidayFS(t, fstest.MapFS{})

	if got := EmbeddedHolidayYears(); got != nil {
		t.Fatalf("目录不存在时应返回 nil，实得 %v", got)
	}
}

func TestLoadEmbeddedHolidaysCorruptJSON(t *testing.T) {
	withHolidayFS(t, fstest.MapFS{
		"holidays/2026.json": &fstest.MapFile{Data: []byte(`{not json`)},
	})

	if _, _, err := LoadEmbeddedHolidays(2026); err == nil {
		t.Fatal("损坏的内置 JSON 应报错")
	}
}

func TestLoadEmbeddedHolidaysEmptyDays(t *testing.T) {
	withHolidayFS(t, fstest.MapFS{
		"holidays/2026.json": &fstest.MapFile{Data: []byte(`{"year":2026,"days":[]}`)},
	})

	if _, _, err := LoadEmbeddedHolidays(2026); err == nil {
		t.Fatal("内置数据为空应报错")
	}
}

// ---------------------------------------------------------------------------
// 远端取数容错分支
// ---------------------------------------------------------------------------

func TestFetchHolidaysFromRemoteNoSourcesConfigured(t *testing.T) {
	prev := HolidaySourceURLs
	HolidaySourceURLs = nil
	t.Cleanup(func() { HolidaySourceURLs = prev })

	if _, _, err := FetchHolidaysFromRemote(context.Background(), 2026); err == nil {
		t.Fatal("未配置任何数据源时应报错")
	}
}

func TestFetchHolidaysFromRemoteInvalidURLTemplate(t *testing.T) {
	prev := HolidaySourceURLs
	// 无 scheme 的地址会让 http.NewRequestWithContext 直接失败，
	// 从而在各镜像都取不到时如实报错，而不是静默返回空
	HolidaySourceURLs = []string{"://no-scheme/%d.json"}
	t.Cleanup(func() { HolidaySourceURLs = prev })

	if _, _, err := FetchHolidaysFromRemote(context.Background(), 2026); err == nil {
		t.Fatal("非法地址应报错")
	}
}

func TestFetchHolidaysFromRemoteFallsThroughMirrors(t *testing.T) {
	// 首个镜像 404，第二个可用 → 应回退到第二个
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer failing.Close()

	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"year":2026,"days":[{"date":"2026-05-01","name":"劳动节","isOffDay":true}]}`))
	}))
	defer ok.Close()

	prev := HolidaySourceURLs
	HolidaySourceURLs = []string{failing.URL + "/%d.json", ok.URL + "/%d.json"}
	t.Cleanup(func() { HolidaySourceURLs = prev })

	days, source, err := FetchHolidaysFromRemote(context.Background(), 2026)
	if err != nil {
		t.Fatalf("应回退到可用镜像：%v", err)
	}
	if len(days) != 1 || days[0].Date != "2026-05-01" {
		t.Fatalf("应取自第二个镜像：%+v", days)
	}
	if !strings.Contains(source, ok.URL) {
		t.Fatalf("来源应为第二个镜像，实得 %q", source)
	}
}

func TestFetchHolidaysFromRemoteTruncatedBody(t *testing.T) {
	// 声明了 100 字节但只发一部分就断开 → io.ReadAll 报错
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"year":2026,"days":[`)) // 远少于 100 字节
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// 提前关闭连接制造不完整读取
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, err := hj.Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}
	}))
	defer srv.Close()

	prev := HolidaySourceURLs
	HolidaySourceURLs = []string{srv.URL + "/%d.json"}
	t.Cleanup(func() { HolidaySourceURLs = prev })

	if _, _, err := FetchHolidaysFromRemote(context.Background(), 2026); err == nil {
		t.Fatal("响应体被截断时应报错")
	}
}

func TestFetchHolidaysFromRemoteContextCancelledStopsEarly(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	prev := HolidaySourceURLs
	HolidaySourceURLs = []string{srv.URL + "/%d.json", srv.URL + "/%d.json"}
	t.Cleanup(func() { HolidaySourceURLs = prev })

	// 已取消的 context 不应继续尝试后续镜像
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := FetchHolidaysFromRemote(ctx, 2026); err == nil {
		t.Fatal("已取消的 context 应报错")
	}
}
