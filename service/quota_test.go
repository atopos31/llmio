package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/quota"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// 纯函数：排序
// ---------------------------------------------------------------------------

func TestSortSourceResults(t *testing.T) {
	rs := []SourceResult{
		{Name: "zeta", OK: false},
		{Name: "beta", OK: true},
		{Name: "alpha", OK: true},
		{Name: "yankee", OK: false},
	}
	SortSourceResults(rs)

	// 成功的在前；同组内按名称升序
	want := []string{"alpha", "beta", "yankee", "zeta"}
	for i, w := range want {
		if rs[i].Name != w {
			t.Fatalf("第 %d 项应为 %s，实得 %s（全量 %v）", i, w, rs[i].Name, names(rs))
		}
	}
}

func names(rs []SourceResult) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	return out
}

// ---------------------------------------------------------------------------
// 纯函数：摘要
// ---------------------------------------------------------------------------

func TestBuildSummary(t *testing.T) {
	t.Run("空结果", func(t *testing.T) {
		sum := BuildSummary(nil)
		if sum.TotalSources != 0 || sum.Worst != nil {
			t.Fatalf("空结果摘要应全零且无 worst: %#v", sum)
		}
	})

	t.Run("失败源不计入条目与最差", func(t *testing.T) {
		sum := BuildSummary([]SourceResult{
			{ID: "bad", Name: "挂了的", OK: false, Status: quota.StatusExhausted,
				Items: []quota.Item{{ID: "x", Status: quota.StatusExhausted, Percent: ptr(99)}}},
			{ID: "good", Name: "好的", OK: true, Status: quota.StatusOK,
				Items: []quota.Item{{ID: "y", Label: "余额", Status: quota.StatusOK, Percent: ptr(10)}}},
		})
		// 失败源的 exhausted 不该成为"最差"——它没有可信数据
		if sum.Worst == nil || sum.Worst.ID != "y" {
			t.Fatalf("最差应取自成功源: %#v", sum.Worst)
		}
		if sum.TotalItems != 1 {
			t.Fatalf("只应统计成功源的条目，实得 %d", sum.TotalItems)
		}
		if sum.OKSources != 1 || sum.FailSources != 1 || sum.TotalSources != 2 {
			t.Fatalf("计数不符: %#v", sum)
		}
	})

	t.Run("取状态最差的跨源条目", func(t *testing.T) {
		sum := BuildSummary([]SourceResult{
			{ID: "a", Name: "A", OK: true, Items: []quota.Item{
				{ID: "a1", Status: quota.StatusOK, Percent: ptr(5)},
				{ID: "a2", Status: quota.StatusWarning, Percent: ptr(85)},
			}},
			{ID: "b", Name: "B", OK: true, Items: []quota.Item{
				{ID: "b1", Status: quota.StatusExhausted, Percent: ptr(100)},
			}},
		})
		if sum.Worst == nil || sum.Worst.ID != "b1" {
			t.Fatalf("应取 exhausted 那条: %#v", sum.Worst)
		}
		// 必须带上来源名，面板要显示"哪个源最紧张"
		if sum.Worst.SourceID != "b" || sum.Worst.SourceName != "B" {
			t.Fatalf("worst 应带来源: %#v", sum.Worst)
		}
	})

	t.Run("状态相同比百分比", func(t *testing.T) {
		sum := BuildSummary([]SourceResult{
			{ID: "a", Name: "A", OK: true, Items: []quota.Item{
				{ID: "lo", Status: quota.StatusWarning, Percent: ptr(81)},
				{ID: "hi", Status: quota.StatusWarning, Percent: ptr(95)},
			}},
		})
		if sum.Worst == nil || sum.Worst.ID != "hi" {
			t.Fatalf("同状态应取百分比更高的: %#v", sum.Worst)
		}
	})

	t.Run("缺百分比时退化为先遇到的", func(t *testing.T) {
		sum := BuildSummary([]SourceResult{
			{ID: "a", Name: "A", OK: true, Items: []quota.Item{
				{ID: "first", Status: quota.StatusUnknown},
				{ID: "second", Status: quota.StatusUnknown},
			}},
		})
		if sum.Worst == nil || sum.Worst.ID != "first" {
			t.Fatalf("无百分比时应取先遇到的: %#v", sum.Worst)
		}
		if sum.Worst.Percent != nil {
			t.Fatalf("缺百分比时应为 nil: %#v", sum.Worst.Percent)
		}
	})

	t.Run("成功但无条目", func(t *testing.T) {
		sum := BuildSummary([]SourceResult{{ID: "a", Name: "A", OK: true}})
		if sum.Worst != nil {
			t.Fatalf("没有条目时不应有 worst: %#v", sum.Worst)
		}
		if sum.OKSources != 1 || sum.TotalItems != 0 {
			t.Fatalf("计数不符: %#v", sum)
		}
	})
}

func ptr(f float64) *float64 { return &f }

// ---------------------------------------------------------------------------
// 纯函数：发现建议
// ---------------------------------------------------------------------------

func TestSuggestForUpstream(t *testing.T) {
	cases := []struct {
		name, url string
		wantType  quota.DataSourceType
		wantBuilt string
	}{
		{"DeepSeek 官方", "https://api.deepseek.com", quota.TypeBuiltin, "deepseek"},
		{"我的 deepseek 中转", "https://x.com", quota.TypeBuiltin, "deepseek"},
		{"Moonshot", "https://api.moonshot.cn", quota.TypeBuiltin, "moonshot"},
		{"Kimi 便宜版", "", quota.TypeBuiltin, "moonshot"},
		{"国家超算", "https://www.scnet.cn", quota.TypeBuiltin, "scnet"},
		{"超算中心", "", quota.TypeBuiltin, "scnet"},
		{"opencode zen", "", quota.TypeBuiltin, "opencode"},
		{"某个小厂", "https://small.example.com", quota.TypeHTTP, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SuggestForUpstream(c.name, c.url)
			if got.Type != c.wantType || got.Builtin != c.wantBuilt {
				t.Fatalf("建议不符：实得 %+v，期望 type=%s builtin=%s", got, c.wantType, c.wantBuilt)
			}
			if got.Note == "" {
				t.Fatal("建议应带说明，否则界面上用户不知道为何这样推荐")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 上游配置解析
// ---------------------------------------------------------------------------

func TestParseUpstreamProviderConfig(t *testing.T) {
	if u, k := ParseUpstreamProviderConfig(`{"base_url":"https://a","api_key":"sk-1"}`); u != "https://a" || k != "sk-1" {
		t.Fatalf("解析不符: %q %q", u, k)
	}
	// 坏配置不该报错，只给空值——一个供应商配坏了不该让整张发现表打不开
	if u, k := ParseUpstreamProviderConfig(`{not json`); u != "" || k != "" {
		t.Fatalf("坏 JSON 应给空值，实得 %q %q", u, k)
	}
	if u, k := ParseUpstreamProviderConfig(``); u != "" || k != "" {
		t.Fatalf("空串应给空值，实得 %q %q", u, k)
	}
}

func TestImportedSourceID(t *testing.T) {
	if got := ImportedSourceID(7); got != "llmio-7" {
		t.Fatalf("id 格式不符: %q", got)
	}
	// 同一个上游必须得到同一个 id —— 否则重复导入会产生第二个源
	if ImportedSourceID(7) != ImportedSourceID(7) {
		t.Fatal("同一上游的 id 必须稳定")
	}
}

func TestBuildCandidates(t *testing.T) {
	providers := []models.Provider{
		{Model: gorm.Model{ID: 1}, Name: "DeepSeek", Type: "openai",
			Config: `{"base_url":"https://api.deepseek.com","api_key":"sk-abcdefghijklmn"}`},
		{Model: gorm.Model{ID: 2}, Name: "小厂", Type: "openai",
			Config: `{"base_url":"https://small.x","api_key":""}`},
	}
	got := BuildCandidates(providers, map[string]bool{ImportedSourceID(1): true})

	if len(got) != 2 {
		t.Fatalf("应得 2 项，实得 %d", len(got))
	}
	if !got[0].AlreadyImported {
		t.Fatal("第一个应标为已导入")
	}
	if got[1].AlreadyImported {
		t.Fatal("第二个不应标为已导入")
	}
	// 密钥必须掩码下发，绝不回传明文
	if strings.Contains(got[0].APIKeyMasked, "abcdefghijklmn") {
		t.Fatalf("密钥未掩码: %q", got[0].APIKeyMasked)
	}
	if !got[0].HasAPIKey || got[1].HasAPIKey {
		t.Fatalf("HasAPIKey 判断不符: %#v", got)
	}
}

// ---------------------------------------------------------------------------
// BuildImportedSource
// ---------------------------------------------------------------------------

func TestBuildImportedSource(t *testing.T) {
	t.Run("内置非登录型直接启用", func(t *testing.T) {
		src, err := BuildImportedSource(models.Provider{
			Name: "DeepSeek", Config: `{"base_url":"https://api.deepseek.com","api_key":"sk-1"}`,
		})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if src.Type != quota.TypeBuiltin || src.Builtin != "deepseek" {
			t.Fatalf("类型不符: %#v", src)
		}
		if !src.Enabled {
			t.Fatal("非登录型应直接启用")
		}
		if src.APIKey != "sk-1" {
			t.Fatalf("密钥应取自上游配置: %q", src.APIKey)
		}
	})

	t.Run("登录型先禁用并留出待填的 env", func(t *testing.T) {
		src, err := BuildImportedSource(models.Provider{
			Model: gorm.Model{ID: 2}, Name: "国家超算", Config: `{"base_url":"https://www.scnet.cn"}`,
		})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if src.Enabled {
			t.Fatal("登录型应先禁用，等用户补账号口令")
		}
		if _, ok := src.Env["SCNET_USER"]; !ok {
			t.Fatalf("应留出 SCNET_USER: %#v", src.Env)
		}
		if _, ok := src.Env["SCNET_PASS"]; !ok {
			t.Fatalf("应留出 SCNET_PASS: %#v", src.Env)
		}
	})

	t.Run("opencode 留出会话键", func(t *testing.T) {
		src, err := BuildImportedSource(models.Provider{Model: gorm.Model{ID: 3}, Name: "opencode"})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if src.Builtin != "opencode" || src.Enabled {
			t.Fatalf("应禁用且指定 opencode: %#v", src)
		}
		if _, ok := src.Env["OPENCODE_COOKIE"]; !ok {
			t.Fatalf("应留出 OPENCODE_COOKIE: %#v", src.Env)
		}
	})

	t.Run("未识别供应商给禁用的 HTTP 占位", func(t *testing.T) {
		src, err := BuildImportedSource(models.Provider{
			Model: gorm.Model{ID: 4}, Name: "小厂", Config: `{"base_url":"https://small.example.com/"}`,
		})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if src.Type != quota.TypeHTTP || src.Enabled {
			t.Fatalf("应为禁用的 HTTP: %#v", src)
		}
		// 末尾斜杠要被吃掉，避免拼出双斜杠
		if src.URL != "https://small.example.com/user/balance" {
			t.Fatalf("占位地址不符: %q", src.URL)
		}
	})
}

// ---------------------------------------------------------------------------
// Store：配置 CRUD
// ---------------------------------------------------------------------------

func newTestStore(t *testing.T) *QuotaStore {
	t.Helper()
	return NewQuotaStore(filepath.Join(t.TempDir(), "quota.config.json"))
}

func TestStoreUpsertAndRemove(t *testing.T) {
	s := newTestStore(t)

	t.Run("新增", func(t *testing.T) {
		got, err := s.UpsertSource(quota.Source{
			ID: "s1", Name: "源一", Type: quota.TypeHTTP, URL: "https://x",
			APIKey: "sk-abcdefghijklmn",
		}, true)
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		// 返回给前端的一律是掩码
		if !quota.IsMasked(got.APIKey) {
			t.Fatalf("返回值应掩码: %q", got.APIKey)
		}
		// 但落盘的必须是明文
		cfg, _ := s.Load()
		if cfg.Sources[0].APIKey != "sk-abcdefghijklmn" {
			t.Fatalf("落盘应为明文: %q", cfg.Sources[0].APIKey)
		}
	})

	t.Run("新增重复 id 报错", func(t *testing.T) {
		_, err := s.UpsertSource(quota.Source{ID: "s1", Type: quota.TypeHTTP, URL: "https://y"}, true)
		if err == nil || !strings.Contains(err.Error(), "已存在") {
			t.Fatalf("应报 id 已存在，实得 %v", err)
		}
	})

	t.Run("更新时掩码密钥还原成已保存的明文", func(t *testing.T) {
		_, err := s.UpsertSource(quota.Source{
			ID: "s1", Name: "改名了", Type: quota.TypeHTTP, URL: "https://x",
			APIKey: quota.MaskSecret("sk-abcdefghijklmn"),
		}, false)
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		cfg, _ := s.Load()
		if cfg.Sources[0].APIKey != "sk-abcdefghijklmn" {
			t.Fatalf("掩码应还原，实得 %q", cfg.Sources[0].APIKey)
		}
		if cfg.Sources[0].Name != "改名了" {
			t.Fatalf("名称应更新，实得 %q", cfg.Sources[0].Name)
		}
	})

	t.Run("校验失败时不落盘", func(t *testing.T) {
		_, err := s.UpsertSource(quota.Source{ID: "bad", Type: quota.TypeHTTP}, false)
		if err == nil {
			t.Fatal("缺 url 应报错")
		}
		cfg, _ := s.Load()
		for _, src := range cfg.Sources {
			if src.ID == "bad" {
				t.Fatal("校验失败的数据源不应被保存")
			}
		}
	})

	t.Run("删除", func(t *testing.T) {
		ok, err := s.RemoveSource("s1")
		if err != nil || !ok {
			t.Fatalf("删除应成功: %v %v", ok, err)
		}
		cfg, _ := s.Load()
		if len(cfg.Sources) != 0 {
			t.Fatalf("应已删除: %#v", cfg.Sources)
		}
	})

	t.Run("删除不存在的返回 false 且不报错", func(t *testing.T) {
		ok, err := s.RemoveSource("nope")
		if err != nil || ok {
			t.Fatalf("应返回 false 且无错: %v %v", ok, err)
		}
	})
}

func TestStoreSaveValidates(t *testing.T) {
	s := newTestStore(t)
	err := s.Save(&quota.Config{Sources: []quota.Source{{ID: "x", Type: quota.TypeHTTP}}})
	if err == nil || !strings.Contains(err.Error(), "url") {
		t.Fatalf("保存应校验，实得 %v", err)
	}
}

func TestStoreInvalidate(t *testing.T) {
	s := newTestStore(t)
	s.cache["a"] = cachedResult{data: SourceResult{ID: "a"}}
	s.cache["b"] = cachedResult{data: SourceResult{ID: "b"}}

	s.Invalidate("a")
	if _, ok := s.cache["a"]; ok {
		t.Fatal("a 应被清掉")
	}
	if _, ok := s.cache["b"]; !ok {
		t.Fatal("b 不该受影响")
	}

	s.Invalidate("")
	if len(s.cache) != 0 {
		t.Fatal("空 id 应清空全部")
	}
}

func TestCacheTTLFloor(t *testing.T) {
	s := newTestStore(t)
	// 刷新间隔再短也不能低于下限，否则会把上游打穿
	if got := s.cacheTTL(&quota.Config{RefreshInterval: 1}); got != MinCacheTTL {
		t.Fatalf("应抬到下限 %v，实得 %v", MinCacheTTL, got)
	}
	if got := s.cacheTTL(&quota.Config{RefreshInterval: 300}); got != 300*time.Second {
		t.Fatalf("应取配置值，实得 %v", got)
	}
}

func TestStoreCacheOnlyStoresSuccess(t *testing.T) {
	s := newTestStore(t)
	// 失败常是瞬时的，缓存它会把一次抖动放大成整个 TTL 的持续报错
	s.store("bad", SourceResult{ID: "bad", OK: false})
	if _, ok := s.cache["bad"]; ok {
		t.Fatal("失败结果不应进缓存")
	}
	s.store("good", SourceResult{ID: "good", OK: true})
	if _, ok := s.cache["good"]; !ok {
		t.Fatal("成功结果应进缓存")
	}
}

func TestStoreCachedRespectsForceAndTTL(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	s.cache["a"] = cachedResult{ts: now, data: SourceResult{ID: "a", OK: true, Name: "A"}}

	if _, ok := s.cached("a", time.Minute, now, true); ok {
		t.Fatal("force 时应忽略缓存")
	}
	got, ok := s.cached("a", time.Minute, now, false)
	if !ok {
		t.Fatal("未过期应命中")
	}
	if !got.Cached {
		t.Fatal("命中的结果应标记 Cached")
	}
	// 过期：把 now 推到 TTL 之后（不能靠传一个极小的 TTL 来制造过期，
	// 同一纳秒内两次 time.Now() 的差可能是 0，那样并不过期）
	if _, ok := s.cached("a", time.Second, now.Add(2*time.Second), false); ok {
		t.Fatal("过期不应命中")
	}
}

// ---------------------------------------------------------------------------
// 端到端：假上游
// ---------------------------------------------------------------------------

func writeEnvelope(w http.ResponseWriter, dataJSON string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"code":"0","data":` + dataJSON + `}`))
}

// newDeepseekStub 起一个假的 DeepSeek 余额接口。
func newDeepseekStub(t *testing.T, hits *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			*hits++
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":12.5}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestStoreRunAllEndToEnd(t *testing.T) {
	srv := newDeepseekStub(t, nil)
	s := newTestStore(t)

	if _, err := s.UpsertSource(quota.Source{
		ID: "d", Name: "DeepSeek", Type: quota.TypeBuiltin, Builtin: "deepseek",
		BaseURL: srv.URL, Enabled: true,
	}, false); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	// Enabled 由配置决定，UpsertSource 不会改它，这里确认一下
	cfg, _ := s.Load()
	if !cfg.Sources[0].Enabled {
		t.Fatal("应已启用")
	}

	res, err := s.RunAll(context.Background(), false, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(res.Sources) != 1 {
		t.Fatalf("应得 1 个源，实得 %d", len(res.Sources))
	}
	src := res.Sources[0]
	if !src.OK {
		t.Fatalf("应成功: %s", src.Error)
	}
	if len(src.Items) != 1 || src.Items[0].Remaining == nil || *src.Items[0].Remaining != 12.5 {
		t.Fatalf("条目不符: %#v", src.Items)
	}
	if res.Summary.OKSources != 1 || res.Summary.TotalItems != 1 {
		t.Fatalf("摘要不符: %#v", res.Summary)
	}
	if res.Summary.Worst == nil || res.Summary.Worst.SourceName != "DeepSeek" {
		t.Fatalf("worst 应带来源名: %#v", res.Summary.Worst)
	}
	if res.RefreshInterval != quota.DefaultRefreshInterval || res.WarningAt != quota.DefaultWarningAt {
		t.Fatalf("全局字段不符: %#v", res)
	}
}

func TestStoreRunAllSkipsDisabled(t *testing.T) {
	srv := newDeepseekStub(t, nil)
	s := newTestStore(t)
	cfg := &quota.Config{Sources: []quota.Source{
		{ID: "on", Name: "启用", Type: quota.TypeBuiltin, Builtin: "deepseek", BaseURL: srv.URL, Enabled: true},
		{ID: "off", Name: "停用", Type: quota.TypeBuiltin, Builtin: "deepseek", BaseURL: srv.URL, Enabled: false},
	}}
	if err := s.Save(cfg); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	res, err := s.RunAll(context.Background(), false, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(res.Sources) != 1 || res.Sources[0].ID != "on" {
		t.Fatalf("停用源不该被跑: %#v", res.Sources)
	}
}

func TestStoreRunAllWithIDsFilter(t *testing.T) {
	srv := newDeepseekStub(t, nil)
	s := newTestStore(t)
	cfg := &quota.Config{Sources: []quota.Source{
		{ID: "a", Name: "A", Type: quota.TypeBuiltin, Builtin: "deepseek", BaseURL: srv.URL, Enabled: true},
		{ID: "b", Name: "B", Type: quota.TypeBuiltin, Builtin: "deepseek", BaseURL: srv.URL, Enabled: true},
	}}
	if err := s.Save(cfg); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	res, _ := s.RunAll(context.Background(), false, []string{"b"})
	if len(res.Sources) != 1 || res.Sources[0].ID != "b" {
		t.Fatalf("应按 id 过滤: %#v", res.Sources)
	}
}

func TestStoreRunAllUsesCacheThenForce(t *testing.T) {
	var hits int32
	srv := newDeepseekStub(t, &hits)
	s := newTestStore(t)
	if _, err := s.UpsertSource(quota.Source{
		ID: "d", Name: "D", Type: quota.TypeBuiltin, Builtin: "deepseek", BaseURL: srv.URL, Enabled: true,
	}, false); err != nil {
		t.Fatal(err)
	}

	if _, err := s.RunAll(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("首次应实取，命中数 %d", hits)
	}

	first, _ := s.RunAll(context.Background(), false, nil)
	if hits != 1 {
		t.Fatalf("第二次应命中缓存，命中数 %d", hits)
	}
	if !first.Sources[0].Cached {
		t.Fatal("缓存命中应标记 Cached")
	}

	second, _ := s.RunAll(context.Background(), true, nil)
	if hits != 2 {
		t.Fatalf("force 应重新实取，命中数 %d", hits)
	}
	if second.Sources[0].Cached {
		t.Fatal("force 的结果不应标记 Cached")
	}
}

func TestStoreRunAllRecordsFailure(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.UpsertSource(quota.Source{
		ID: "bad", Name: "坏源", Type: quota.TypeHTTP, URL: "http://127.0.0.1:1/x", Enabled: true,
	}, false); err != nil {
		t.Fatal(err)
	}
	res, err := s.RunAll(context.Background(), false, nil)
	if err != nil {
		t.Fatalf("整体不应报错（失败应在单个源里）: %v", err)
	}
	src := res.Sources[0]
	if src.OK || src.Status != quota.StatusUnknown || src.Error == "" {
		t.Fatalf("应记录失败: %#v", src)
	}
	if res.Summary.FailSources != 1 || res.Summary.Worst != nil {
		t.Fatalf("摘要应反映失败且无 worst: %#v", res.Summary)
	}
}

func TestStoreRefreshSource(t *testing.T) {
	var hits int32
	srv := newDeepseekStub(t, &hits)
	s := newTestStore(t)
	if _, err := s.UpsertSource(quota.Source{
		ID: "d", Name: "D", Type: quota.TypeBuiltin, Builtin: "deepseek", BaseURL: srv.URL, Enabled: true,
	}, false); err != nil {
		t.Fatal(err)
	}
	// 先让缓存里有值
	if _, err := s.RunAll(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	before := hits
	res, err := s.RefreshSource(context.Background(), "d")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if hits != before+1 {
		t.Fatalf("应只实取该源一次，命中数 %d -> %d", before, hits)
	}
	if !res.Sources[0].OK {
		t.Fatalf("结果应成功: %s", res.Sources[0].Error)
	}
}

// ---------------------------------------------------------------------------
// 试跑
// ---------------------------------------------------------------------------

func TestStoreTestSource(t *testing.T) {
	srv := newDeepseekStub(t, nil)
	s := newTestStore(t)

	res := s.TestSource(context.Background(), quota.Source{
		ID: "t", Type: quota.TypeBuiltin, Builtin: "deepseek", BaseURL: srv.URL,
	})
	if !res.OK {
		t.Fatalf("试跑应成功: %s", res.Error)
	}
	if len(res.Items) != 1 {
		t.Fatalf("应得 1 条，实得 %d", len(res.Items))
	}
	if res.DurationMs < 0 {
		t.Fatal("耗时应非负")
	}
	// 试跑不写缓存、不落盘
	if len(s.cache) != 0 {
		t.Fatal("试跑不应写缓存")
	}
	cfg, _ := s.Load()
	if len(cfg.Sources) != 0 {
		t.Fatal("试跑不应落盘")
	}
}

func TestStoreTestSourceValidation(t *testing.T) {
	s := newTestStore(t)
	res := s.TestSource(context.Background(), quota.Source{ID: "x", Type: quota.TypeHTTP})
	if res.OK || !strings.Contains(res.Error, "url") {
		t.Fatalf("缺 url 应报错: %#v", res)
	}
}

func TestStoreTestSourceFailure(t *testing.T) {
	s := newTestStore(t)
	res := s.TestSource(context.Background(), quota.Source{
		ID: "x", Type: quota.TypeHTTP, URL: "http://127.0.0.1:1/x",
	})
	if res.OK || res.Error == "" {
		t.Fatalf("连不上应报错: %#v", res)
	}
}

func TestStoreTestSourceBackfillsMaskedSecret(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"used":1,"total":4}`))
	}))
	defer srv.Close()

	s := newTestStore(t)
	realKey := "sk-realkey12345"
	if _, err := s.UpsertSource(quota.Source{
		ID: "h", Name: "H", Type: quota.TypeHTTP, URL: srv.URL, APIKey: realKey,
	}, false); err != nil {
		t.Fatal(err)
	}

	// 前端回传掩码密钥试跑：必须还原成已保存的真实值，否则试跑永远 401
	res := s.TestSource(context.Background(), quota.Source{
		ID: "h", Type: quota.TypeHTTP, URL: srv.URL, APIKey: quota.MaskSecret(realKey),
	})
	if !res.OK {
		t.Fatalf("试跑应成功: %s", res.Error)
	}
	if gotAuth != "Bearer "+realKey {
		t.Fatalf("应带上已保存的真实密钥，实得 %q", gotAuth)
	}
}

func TestStoreTestSourceScript(t *testing.T) {
	s := newTestStore(t)
	res := s.TestSource(context.Background(), quota.Source{
		ID: "scr", Type: quota.TypeScript,
		ScriptSource: `output({used: 3, total: 12, unit: "CREDITS"})`,
	})
	// 脚本要拉子进程，用测试二进制的 TestMain 沙箱分支；这里只断言它能跑通
	if !res.OK {
		t.Fatalf("脚本试跑应成功: %s", res.Error)
	}
	if len(res.Items) != 1 || res.Items[0].Percent == nil || *res.Items[0].Percent != 25 {
		t.Fatalf("脚本产出归一不符: %#v", res.Items)
	}
}

func TestStoreTestSourceScriptBadOutput(t *testing.T) {
	s := newTestStore(t)
	res := s.TestSource(context.Background(), quota.Source{
		ID: "scr", Type: quota.TypeScript, ScriptSource: `console.log("没有 JSON")`,
	})
	if res.OK || res.Error == "" {
		t.Fatalf("脚本无产出应报错: %#v", res)
	}
}

// ---------------------------------------------------------------------------
// 发现与导入
// ---------------------------------------------------------------------------

func TestStoreDiscover(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.UpsertSource(quota.Source{
		ID: ImportedSourceID(1), Name: "已导入", Type: quota.TypeHTTP, URL: "https://x",
	}, false); err != nil {
		t.Fatal(err)
	}

	got, err := s.Discover([]models.Provider{
		{Model: gorm.Model{ID: 1}, Name: "DeepSeek",
			Config: `{"base_url":"https://api.deepseek.com","api_key":"sk-abcdefghij"}`},
	})
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(got) != 1 || !got[0].AlreadyImported {
		t.Fatalf("应识别为已导入: %#v", got)
	}
}

func TestStoreImportFromUpstream(t *testing.T) {
	s := newTestStore(t)
	got, err := s.ImportFromUpstream(models.Provider{
		Model: gorm.Model{ID: 1}, Name: "DeepSeek",
		Config: `{"base_url":"https://api.deepseek.com","api_key":"sk-abcdefghij"}`,
	})
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if !quota.IsMasked(got.APIKey) {
		t.Fatalf("返回值应掩码: %q", got.APIKey)
	}
	// 密钥在服务端写入，落盘是明文；前端从头到尾拿不到明文
	cfg, _ := s.Load()
	if cfg.Sources[0].APIKey != "sk-abcdefghij" {
		t.Fatalf("落盘应为明文: %q", cfg.Sources[0].APIKey)
	}
}

func TestStoreImportTwiceRejected(t *testing.T) {
	s := newTestStore(t)
	p := models.Provider{Model: gorm.Model{ID: 5}, Name: "DeepSeek",
		Config: `{"base_url":"https://api.deepseek.com","api_key":"k"}`}
	if _, err := s.ImportFromUpstream(p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportFromUpstream(p); err == nil || !strings.Contains(err.Error(), "已导入") {
		t.Fatalf("重复导入应被拒，实得 %v", err)
	}
}

func TestStoreImportLoginTypeSkipsFullValidation(t *testing.T) {
	s := newTestStore(t)
	// 登录型导入时 env 是空的，完整校验会拦下来；导入应当放行，等用户补
	if _, err := s.ImportFromUpstream(models.Provider{
		Model: gorm.Model{ID: 9}, Name: "国家超算", Config: `{"base_url":"https://www.scnet.cn"}`,
	}); err != nil {
		t.Fatalf("登录型导入应放行: %v", err)
	}
	cfg, _ := s.Load()
	if cfg.Sources[0].Enabled {
		t.Fatal("登录型导入后应保持禁用")
	}
}

func TestStoreImportUnknownProviderWithoutURL(t *testing.T) {
	s := newTestStore(t)
	// 未识别且没地址 -> 推导不出接口，明确报错而不是存一个坏配置
	_, err := s.ImportFromUpstream(models.Provider{Model: gorm.Model{ID: 11}, Name: "小厂"})
	if err == nil || !strings.Contains(err.Error(), "接口地址") {
		t.Fatalf("应报无法推导地址，实得 %v", err)
	}
}

// ---------------------------------------------------------------------------
// SafeConfig
// ---------------------------------------------------------------------------

func TestSafeConfigMasksSecrets(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.UpsertSource(quota.Source{
		ID: "s", Type: quota.TypeHTTP, URL: "https://x", APIKey: "sk-abcdefghijklmn",
	}, false); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.SafeConfig()
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if !quota.IsMasked(cfg.Sources[0].APIKey) {
		t.Fatalf("下发配置里的密钥必须掩码: %q", cfg.Sources[0].APIKey)
	}
	// 且不能改动原文件
	raw, _ := os.ReadFile(s.Path())
	if !strings.Contains(string(raw), "sk-abcdefghijklmn") {
		t.Fatal("SafeConfig 不应改动磁盘上的配置")
	}
}

// 保证 json 包被用到（上游配置解析测试依赖它）
var _ = json.Marshal
