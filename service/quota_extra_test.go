package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atopos31/llmio/quota"
)

// 本文件补 service/quota.go 的错误路径与边界分支。
// 纯逻辑与主流程在 quota_test.go；这里只针对 IO 失败、指针带过、默认值回退等。

// badConfigStore 返回一个配置文件损坏的 store，用来覆盖各处的 load 错误分支。
func badConfigStore(t *testing.T) *QuotaStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "quota.config.json")
	if err := os.WriteFile(path, []byte("{不是 JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	return NewQuotaStore(path)
}

func TestStoreLoadErrorsPropagate(t *testing.T) {
	t.Run("SafeConfig", func(t *testing.T) {
		if _, err := badConfigStore(t).SafeConfig(); err == nil {
			t.Fatal("配置损坏应报错")
		}
	})

	t.Run("RunAll", func(t *testing.T) {
		if _, err := badConfigStore(t).RunAll(context.Background(), false, nil); err == nil {
			t.Fatal("配置损坏应报错")
		}
	})

	t.Run("UpsertSource", func(t *testing.T) {
		if _, err := badConfigStore(t).UpsertSource(quota.Source{Type: quota.TypeHTTP, URL: "https://x"}, false); err == nil {
			t.Fatal("配置损坏应报错")
		}
	})

	t.Run("RemoveSource", func(t *testing.T) {
		if _, err := badConfigStore(t).RemoveSource("x"); err == nil {
			t.Fatal("配置损坏应报错")
		}
	})

	t.Run("TestSource 校验先于读配置", func(t *testing.T) {
		res := badConfigStore(t).TestSource(context.Background(), quota.Source{Type: quota.TypeHTTP})
		if res.OK || !strings.Contains(res.Error, "url") {
			t.Fatalf("应先报校验错误: %#v", res)
		}
	})

	t.Run("TestSource 配置损坏时报读配置失败", func(t *testing.T) {
		srv := newDeepseekStub(t, nil)
		res := badConfigStore(t).TestSource(context.Background(), quota.Source{
			Type: quota.TypeBuiltin, Builtin: "deepseek", BaseURL: srv.URL, ID: "x",
		})
		if res.OK || res.Error == "" {
			t.Fatalf("配置损坏应报错: %#v", res)
		}
	})
}

func TestToHTTPConfigWithAuth(t *testing.T) {
	src := quota.Source{
		ID: "h", Name: "H", URL: "https://x", Method: "POST",
		Query:     map[string]any{"page": "1"},
		Headers:   map[string]any{"X-A": "1"},
		Body:      map[string]any{"k": 1},
		Auth:      &quota.HTTPAuth{Type: "bearer", Header: "Authorization"},
		ItemsPath: "d.items", Map: map[string]any{"used": "u"},
		Constants: map[string]any{"unit": "CNY"},
		Timeout:   7,
	}
	got := toHTTPConfig(src)
	if got.URL != "https://x" || got.Method != "POST" || got.TimeoutMs != 7000 {
		t.Fatalf("基础字段不符: %#v", got)
	}
	// 这一栏以前没被拷贝：编辑器填得下、配置里存得住，取数时不发出去
	if got.Query == nil || got.Query.(map[string]any)["page"] != "1" {
		t.Fatalf("查询参数丢失: %#v", got.Query)
	}
	// Auth 是指针，必须解引用带过去，否则鉴权配置会丢
	if got.Auth.Type != "bearer" || got.Auth.Header != "Authorization" {
		t.Fatalf("鉴权配置丢失: %#v", got.Auth)
	}
	if got.ItemsPath != "d.items" || got.Constants["unit"] != "CNY" {
		t.Fatalf("映射/常量不符: %#v", got)
	}
}

func TestToBuiltinConfig(t *testing.T) {
	src := quota.Source{
		ID: "b", Name: "B", Builtin: "custom", BaseURL: "https://x", APIKey: "k",
		Path: "/p", Method: "GET", Query: map[string]any{"a": "1"},
		Headers: map[string]any{"X": "1"}, ItemsPath: "rows", Map: map[string]any{"used": "u"},
		Timeout: 5, Env: map[string]string{"K": "V"},
	}
	got := toBuiltinConfig(src)
	if got.BuiltinID != "custom" || got.Path != "/p" || got.TimeoutMs != 5000 {
		t.Fatalf("基础字段不符: %#v", got)
	}
	if got.Env["K"] != "V" || got.ID != "b" || got.Name != "B" {
		t.Fatalf("env/id/name 不符: %#v", got)
	}
	if got.Query == nil || got.Query.(map[string]any)["a"] != "1" {
		t.Fatalf("查询参数丢失: %#v", got.Query)
	}
}

func TestSourceTimeout(t *testing.T) {
	fallback := 9 * time.Second
	if got := sourceTimeout(quota.Source{}, fallback); got != fallback {
		t.Fatalf("未指定应取默认，实得 %v", got)
	}
	if got := sourceTimeout(quota.Source{Timeout: 3}, fallback); got != 3*time.Second {
		t.Fatalf("应取源配置，实得 %v", got)
	}
}

func TestBuiltinHelpers(t *testing.T) {
	keys, needs := builtinLoginEnvKeys("scnet")
	if !needs || len(keys) == 0 {
		t.Fatalf("scnet 应需要登录并给出 env 键: %v %v", keys, needs)
	}
	if _, needs := builtinLoginEnvKeys("deepseek"); needs {
		t.Fatal("deepseek 不需要登录")
	}
	if _, needs := builtinLoginEnvKeys("nope"); needs {
		t.Fatal("不存在的适配器不应报需要登录")
	}
}

func TestNewQuotaStoreDefaultPath(t *testing.T) {
	t.Setenv("LLMIO_QUOTA_CONFIG", "/tmp/x.json")
	if got := NewQuotaStore("").Path(); got != "/tmp/x.json" {
		t.Fatalf("空白 path 应用 quota.ConfigPath()，实得 %q", got)
	}
	if got := NewQuotaStore("  ").Path(); got != "/tmp/x.json" {
		t.Fatalf("纯空白也应回退，实得 %q", got)
	}
	if got := NewQuotaStore("/custom/p.json").Path(); got != "/custom/p.json" {
		t.Fatalf("显式 path 应原样，实得 %q", got)
	}
}

func TestStoreSaveInvalidConfig(t *testing.T) {
	s := newTestStore(t)
	if err := s.Save(&quota.Config{Sources: []quota.Source{{ID: "x", Type: "???"}}}); err == nil {
		t.Fatal("非法类型应被校验拦下")
	}
}

func TestStoreUpsertNewWithExistingIDRejected(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.UpsertSource(quota.Source{ID: "dup", Type: quota.TypeHTTP, URL: "https://a"}, false); err != nil {
		t.Fatal(err)
	}
	// isNew=true 且 id 已存在 -> 明确拒绝，而不是悄悄覆盖
	if _, err := s.UpsertSource(quota.Source{ID: "dup", Type: quota.TypeHTTP, URL: "https://b"}, true); err == nil {
		t.Fatal("新增时 id 重复应被拒")
	}
}

func TestStoreUpsertWithoutIDAppends(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.UpsertSource(quota.Source{Type: quota.TypeHTTP, URL: "https://a"}, false); err != nil {
		t.Fatal(err)
	}
	cfg, _ := s.Load()
	if len(cfg.Sources) != 1 || !strings.HasPrefix(cfg.Sources[0].ID, "src-") {
		t.Fatalf("应生成 id 并追加: %#v", cfg.Sources)
	}
}

func TestStoreRunAllEmptyConfig(t *testing.T) {
	res, err := newTestStore(t).RunAll(context.Background(), false, nil)
	if err != nil {
		t.Fatalf("空配置不应报错: %v", err)
	}
	if len(res.Sources) != 0 || res.Summary.TotalSources != 0 {
		t.Fatalf("空配置应得空结果: %#v", res)
	}
	// 默认值仍要带上，前端据此显示刷新间隔
	if res.RefreshInterval != quota.DefaultRefreshInterval {
		t.Fatalf("应带默认刷新间隔，实得 %d", res.RefreshInterval)
	}
}

func TestStoreRefreshSourceLoadError(t *testing.T) {
	if _, err := badConfigStore(t).RefreshSource(context.Background(), "x"); err == nil {
		t.Fatal("配置损坏应报错")
	}
}

func TestFetchRowsUnknownType(t *testing.T) {
	s := newTestStore(t)
	_, _, err := s.fetchRows(context.Background(), quota.Source{Type: "???"})
	if err == nil || !strings.Contains(err.Error(), "未知数据源类型") {
		t.Fatalf("未知类型应报错，实得 %v", err)
	}
}

func TestFetchRowsHTTPWithAuth(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"used":1,"total":4}]}`))
	}))
	defer srv.Close()

	s := newTestStore(t)
	raw, _, err := s.fetchRows(context.Background(), quota.Source{
		ID: "h", Type: quota.TypeHTTP, URL: srv.URL + "/usage", APIKey: "sk-abcdefghij",
		Auth: &quota.HTTPAuth{Type: "bearer"}, ItemsPath: "items",
	})
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if gotAuth != "Bearer sk-abcdefghij" {
		t.Fatalf("鉴权头不符: %q", gotAuth)
	}
	if gotPath != "/usage" {
		t.Fatalf("路径不符: %q", gotPath)
	}
	if raw == nil {
		t.Fatal("应带回原始行")
	}
}

func TestFetchRowsScriptNoOutput(t *testing.T) {
	s := newTestStore(t)
	// 脚本正常退出但没有任何可识别产出 -> RunScript 自己会报错
	_, _, err := s.fetchRows(context.Background(), quota.Source{
		ID: "scr", Type: quota.TypeScript, ScriptSource: `var x = 1;`,
	})
	if err == nil {
		t.Fatal("脚本无产出应报错")
	}
}

func TestFetchRowsScriptWarningSurfaces(t *testing.T) {
	s := newTestStore(t)
	// 未调用 output()，从 console.log 里的 JSON 取值 -> 应带出"取值来自"提示
	raw, warning, err := s.fetchRows(context.Background(), quota.Source{
		ID: "scr", Type: quota.TypeScript,
		ScriptSource: `console.log(JSON.stringify({used: 2, total: 8}))`,
	})
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if raw == nil {
		t.Fatal("应带回值")
	}
	if !strings.Contains(warning, "取值来自") {
		t.Fatalf("应提示取值来源，实得 %q", warning)
	}
}

func TestRunSourcePerSourceWarningAt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":5}]}`))
	}))
	defer srv.Close()

	s := newTestStore(t)
	cfg := &quota.Config{WarningAt: 80, Sources: []quota.Source{{
		ID: "d", Name: "D", Type: quota.TypeBuiltin, Builtin: "deepseek",
		BaseURL: srv.URL, Enabled: true, WarningAt: 1, // 极低阈值 -> 必然 warning
	}}}
	if err := s.Save(cfg); err != nil {
		t.Fatal(err)
	}
	res, err := s.RunAll(context.Background(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 只有 remaining 没有 total，推不出已用百分比；这里主要验证源级 WarningAt
	// 被采用（不 panic、源仍成功），百分比推导本身由契约的测试覆盖。
	if !res.Sources[0].OK {
		t.Fatalf("应成功: %s", res.Sources[0].Error)
	}
}

// ---------------------------------------------------------------------------
// 再补几条可达分支
// ---------------------------------------------------------------------------

func TestBuildSummaryLowerRankAfterHigher(t *testing.T) {
	// 前面的条目状态更差、后面的更好 -> 走 cmp < 0 的 continue 分支。
	// 这是常见的真实数据形状（一个源里既有用尽的也有健康的）。
	sum := BuildSummary([]SourceResult{
		{ID: "a", Name: "A", OK: true, Items: []quota.Item{
			{ID: "bad", Status: quota.StatusExhausted, Percent: ptr(100)},
			{ID: "good", Status: quota.StatusOK, Percent: ptr(1)},
		}},
	})
	if sum.Worst == nil || sum.Worst.ID != "bad" {
		t.Fatalf("应保持先遇到的更差条目: %#v", sum.Worst)
	}
}

func TestStoreRemoveSourceKeepsOthers(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.UpsertSource(quota.Source{ID: "a", Name: "A", Type: quota.TypeHTTP, URL: "https://a"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSource(quota.Source{ID: "b", Name: "B", Type: quota.TypeHTTP, URL: "https://b"}, false); err != nil {
		t.Fatal(err)
	}
	ok, err := s.RemoveSource("a")
	if err != nil || !ok {
		t.Fatalf("删除应成功: %v %v", ok, err)
	}
	cfg, _ := s.Load()
	if len(cfg.Sources) != 1 || cfg.Sources[0].ID != "b" {
		t.Fatalf("未被删除的源应保留: %#v", cfg.Sources)
	}
}

// newUnrecognizableStub 返回 200 且是合法 JSON，但契约认不出任何余量数值。
// 这是真实场景：对端接口变了字段名，或 itemsPath/map 配错了。
func newUnrecognizableStub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"foo":"bar"}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRunSourceNormalizeFailure(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.UpsertSource(quota.Source{
		ID: "u", Name: "认不出", Type: quota.TypeHTTP, URL: newUnrecognizableStub(t).URL,
		ItemsPath: "items", Map: map[string]any{"label": "foo"}, Enabled: true,
	}, false); err != nil {
		t.Fatal(err)
	}
	res, err := s.RunAll(context.Background(), false, nil)
	if err != nil {
		t.Fatalf("整体不应报错: %v", err)
	}
	src := res.Sources[0]
	if src.OK {
		t.Fatal("契约认不出时应算失败")
	}
	// 错误必须说清"认不出余量"而不是含糊的解析失败——
	// 用户据此判断是接口换了字段还是映射配错了
	if !strings.Contains(src.Error, "没有任何可识别的余量数值") {
		t.Fatalf("错误应说明认不出余量，实得 %q", src.Error)
	}
}

func TestTestSourceNormalizeFailure(t *testing.T) {
	s := newTestStore(t)
	res := s.TestSource(context.Background(), quota.Source{
		ID: "u", Type: quota.TypeHTTP, URL: newUnrecognizableStub(t).URL,
		ItemsPath: "items", Map: map[string]any{"label": "foo"},
	})
	if res.OK {
		t.Fatal("认不出余量时应算失败")
	}
	if !strings.Contains(res.Error, "没有任何可识别的余量数值") {
		t.Fatalf("错误应说明认不出余量，实得 %q", res.Error)
	}
	// 试跑即使归一失败也要回原始值：这正是用户排查所依据的东西
	if res.RawValue == nil {
		t.Fatal("归一失败时仍应带回原始值供排查")
	}
}

func TestTestSourceLoginTypeFillsEnvKeys(t *testing.T) {
	s := newTestStore(t)
	// 登录型、env 为空：试跑前应补上待填的键，让校验给出"缺账号口令"
	// 而不是含糊的"未知数据源类型"
	res := s.TestSource(context.Background(), quota.Source{
		ID: "sc", Type: quota.TypeBuiltin, Builtin: "scnet", BaseURL: "http://127.0.0.1:1",
	})
	if res.OK {
		t.Fatal("缺账号口令应失败")
	}
	// 补了键但值是空的 -> 校验通过（env 非空）-> 走到真正登录并失败
	// 两种都可以接受，但错误不能是"未知数据源类型"
	if strings.Contains(res.Error, "未知数据源类型") {
		t.Fatalf("不应报未知类型: %q", res.Error)
	}
}
