package quota

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// LoadConfig / SaveConfig
// ---------------------------------------------------------------------------

func TestLoadConfigMissingFileReturnsDefaults(t *testing.T) {
	// 首次部署没有配置文件是正常的，不该报错
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("缺文件不应报错: %v", err)
	}
	if cfg.RefreshInterval != DefaultRefreshInterval || cfg.WarningAt != DefaultWarningAt {
		t.Fatalf("应填默认值，实得 %+v", cfg)
	}
	if cfg.Sources == nil {
		t.Fatal("Sources 应为空切片而非 nil（前端不用判空）")
	}
}

func TestLoadConfigInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "不是合法 JSON") {
		t.Fatalf("应报非法 JSON，实得 %v", err)
	}
}

func TestLoadConfigReadError(t *testing.T) {
	// 传一个目录当文件：ReadFile 会报非 ENOENT 的错误
	if _, err := LoadConfig(t.TempDir()); err == nil {
		t.Fatal("读目录应报错")
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "quota.config.json")
	in := &Config{
		RefreshInterval: 60,
		WarningAt:       90,
		Sources: []Source{
			{ID: "s1", Name: "DeepSeek", Type: TypeBuiltin, Builtin: "deepseek", Enabled: true, APIKey: "sk-secret"},
			{ID: "s2", Name: "自定义", Type: TypeHTTP, URL: "https://x/y"},
		},
	}
	if err := SaveConfig(path, in); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	out, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if out.RefreshInterval != 60 || out.WarningAt != 90 {
		t.Fatalf("全局字段不符: %+v", out)
	}
	if len(out.Sources) != 2 {
		t.Fatalf("数据源数不符: %d", len(out.Sources))
	}
	if out.Sources[0].APIKey != "sk-secret" {
		t.Fatal("密钥应原样保存（脱敏只在下发时做）")
	}
}

func TestSaveConfigCreatesDirAndUses0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deep", "nested", "c.json")
	if err := SaveConfig(path, &Config{}); err != nil {
		t.Fatalf("应自动建目录: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("文件应存在: %v", err)
	}
	// 配置里可能有明文密钥（R5），代码里收紧到 0600。
	// 但 Windows 用 ACL 而不是 Unix mode 位，os.WriteFile 的权限参数基本被忽略
	// （实测落成 0666），因此只在 Unix 上断言这一点。
	if runtime.GOOS == "windows" {
		t.Skip("Windows 以 ACL 取代 Unix mode 位，权限断言无意义")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("权限应为 0600，实得 %o", perm)
	}
}

func TestSaveConfigLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	if err := SaveConfig(path, &Config{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("原子写完成后不应残留 .tmp")
	}
}

// ---------------------------------------------------------------------------
// 归一
// ---------------------------------------------------------------------------

func TestNormalizeSourceFillsDefaults(t *testing.T) {
	cfg := &Config{Sources: []Source{{Type: "奇奇怪怪"}}}
	normalizeConfig(cfg)
	s := cfg.Sources[0]
	if s.ID == "" || !strings.HasPrefix(s.ID, "src-") {
		t.Fatalf("应生成 id，实得 %q", s.ID)
	}
	if s.Name != s.ID {
		t.Fatalf("缺名称时应回填 id，实得 %q", s.Name)
	}
	if s.Type != TypeBuiltin {
		t.Fatalf("未知类型应退回 builtin，实得 %q", s.Type)
	}
	if s.Builtin != "deepseek" {
		t.Fatalf("builtin 类型应填默认适配器，实得 %q", s.Builtin)
	}
}

func TestNormalizeSourceUppercasesMethod(t *testing.T) {
	s := Source{Type: TypeHTTP, Method: " post "}
	normalizeSource(&s)
	if s.Method != "POST" {
		t.Fatalf("方法应归一为大写，实得 %q", s.Method)
	}
}

func TestNormalizeConfigGlobalDefaults(t *testing.T) {
	cfg := &Config{RefreshInterval: -5, WarningAt: -1}
	normalizeConfig(cfg)
	if cfg.RefreshInterval != DefaultRefreshInterval || cfg.WarningAt != DefaultWarningAt {
		t.Fatalf("非法值应退回默认，实得 %+v", cfg)
	}
}

func TestNewSourceIDUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		id := newSourceID()
		if seen[id] {
			t.Fatalf("id 重复: %s", id)
		}
		seen[id] = true
	}
}

// ---------------------------------------------------------------------------
// 脱敏
// ---------------------------------------------------------------------------

func TestMaskSecret(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"空", "", ""},
		{"短值只留 2 位", "abc", "ab****"},
		{"正好 10 位", "0123456789", "01****"},
		{"长值留头 6 尾 4", "sk-abcdefghijklmn", "sk-abc****klmn"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MaskSecret(c.in); got != c.want {
				t.Fatalf("MaskSecret(%q) = %q，期望 %q", c.in, got, c.want)
			}
		})
	}
}

func TestIsMasked(t *testing.T) {
	if !IsMasked("sk-abc****klmn") {
		t.Fatal("含 **** 应判为掩码")
	}
	if IsMasked("sk-real-key") {
		t.Fatal("真实值不应判为掩码")
	}
}

func TestMaskSource(t *testing.T) {
	s := MaskSource(Source{
		APIKey: "sk-abcdefghijklmn",
		Env: map[string]string{
			"SCNET_USER":   "alice",            // 不像密钥 -> 原样
			"SCNET_PASS":   "hunter2hunter2",   // pass -> 打码
			"OPENCODE_KEY": "xxxxxxxxxxxx",     // key -> 打码
			"COOKIE_JAR":   "session=abcdefgh", // cookie -> 打码
			"SOME_TOKEN":   "t0k3n0k3n0k3n0k",  // token -> 打码
		},
		ScriptSource: "output({used:1})",
	})
	if !IsMasked(s.APIKey) {
		t.Fatalf("apiKey 应被打码，实得 %q", s.APIKey)
	}
	if s.Env["SCNET_USER"] != "alice" {
		t.Fatalf("非密钥变量不应打码，实得 %q", s.Env["SCNET_USER"])
	}
	for _, k := range []string{"SCNET_PASS", "OPENCODE_KEY", "COOKIE_JAR", "SOME_TOKEN"} {
		if !IsMasked(s.Env[k]) {
			t.Fatalf("变量 %s 应被打码，实得 %q", k, s.Env[k])
		}
	}
	// scriptSource 是例外：编辑器必须能显示它
	if s.ScriptSource != "output({used:1})" {
		t.Fatalf("脚本源码不应被打码，实得 %q", s.ScriptSource)
	}
}

func TestMaskSourceKeepsEmptyKeyEmpty(t *testing.T) {
	s := MaskSource(Source{APIKey: ""})
	if s.APIKey != "" {
		t.Fatalf("未配置密钥应保持空，实得 %q", s.APIKey)
	}
}

func TestMaskConfigDoesNotMutateOriginal(t *testing.T) {
	orig := &Config{Sources: []Source{{ID: "s1", APIKey: "sk-abcdefghijklmn"}}}
	masked := MaskConfig(orig)
	if !IsMasked(masked.Sources[0].APIKey) {
		t.Fatal("返回的副本应已脱敏")
	}
	if orig.Sources[0].APIKey != "sk-abcdefghijklmn" {
		t.Fatal("原配置不应被改动")
	}
}

func TestHasSecret(t *testing.T) {
	if HasSecret(Source{APIKey: "  "}) {
		t.Fatal("空白密钥不算已配置")
	}
	if !HasSecret(Source{APIKey: "k"}) {
		t.Fatal("有密钥应返回 true")
	}
}

// ---------------------------------------------------------------------------
// 密钥回填
// ---------------------------------------------------------------------------

func TestResolveSourceSecrets(t *testing.T) {
	saved := &Source{
		APIKey: "sk-real",
		Env:    map[string]string{"SCNET_USER": "alice", "SCNET_PASS": "realpass"},
	}

	t.Run("apiKey 留空时还原", func(t *testing.T) {
		out := ResolveSourceSecrets(Source{}, saved)
		if out.APIKey != "sk-real" {
			t.Fatalf("留空应还原已保存的密钥，实得 %q", out.APIKey)
		}
	})

	t.Run("apiKey 是掩码时还原", func(t *testing.T) {
		out := ResolveSourceSecrets(Source{APIKey: "sk-re****real"}, saved)
		if out.APIKey != "sk-real" {
			t.Fatalf("掩码应还原，实得 %q", out.APIKey)
		}
	})

	t.Run("apiKey 是新值时采用", func(t *testing.T) {
		out := ResolveSourceSecrets(Source{APIKey: "sk-new"}, saved)
		if out.APIKey != "sk-new" {
			t.Fatalf("新值应被采用，实得 %q", out.APIKey)
		}
	})

	t.Run("env 掩码还原、留空保持留空", func(t *testing.T) {
		out := ResolveSourceSecrets(Source{Env: map[string]string{
			"SCNET_USER": "alice",        // 没变
			"SCNET_PASS": "real****pass", // 掩码 -> 还原
		}}, saved)
		if out.Env["SCNET_USER"] != "alice" {
			t.Fatalf("未变的值应保留: %#v", out.Env)
		}
		if out.Env["SCNET_PASS"] != "realpass" {
			t.Fatalf("掩码应还原，实得 %q", out.Env["SCNET_PASS"])
		}

		// 真正留空表示用户就是想清掉
		out2 := ResolveSourceSecrets(Source{Env: map[string]string{"SCNET_PASS": ""}}, saved)
		if out2.Env["SCNET_PASS"] != "" {
			t.Fatalf("留空应保持留空，实得 %q", out2.Env["SCNET_PASS"])
		}
	})

	t.Run("无已保存配置时原样返回", func(t *testing.T) {
		in := Source{APIKey: "k"}
		out := ResolveSourceSecrets(in, nil)
		if out.APIKey != "k" {
			t.Fatalf("无 saved 时应原样，实得 %q", out.APIKey)
		}
	})

	t.Run("saved 的 env 缺该键时还原成空", func(t *testing.T) {
		out := ResolveSourceSecrets(Source{Env: map[string]string{"OTHER": "x****y"}},
			&Source{Env: map[string]string{}})
		if out.Env["OTHER"] != "" {
			t.Fatalf("saved 里没有的键应还原成空，实得 %q", out.Env["OTHER"])
		}
	})
}

// ---------------------------------------------------------------------------
// 校验
// ---------------------------------------------------------------------------

func TestValidateSource(t *testing.T) {
	cases := []struct {
		name    string
		src     Source
		wantErr string
	}{
		{"builtin 正常", Source{Type: TypeBuiltin, Builtin: "deepseek"}, ""},
		{"builtin 未知适配器", Source{Type: TypeBuiltin, Builtin: "nope"}, "未知内置适配器"},
		{"custom 缺 path", Source{Type: TypeBuiltin, Builtin: "custom"}, "请求路径"},
		{"custom 有 path", Source{Type: TypeBuiltin, Builtin: "custom", Path: "/x"}, ""},
		{"登录型缺 env", Source{Type: TypeBuiltin, Builtin: "scnet"}, "SCNET_USER"},
		{"登录型有 env", Source{Type: TypeBuiltin, Builtin: "scnet",
			Env: map[string]string{"SCNET_USER": "u"}}, ""},
		{"http 缺 url", Source{Type: TypeHTTP}, "需要填写 url"},
		{"http 正常", Source{Type: TypeHTTP, URL: "https://x"}, ""},
		{"script 缺内容", Source{Type: TypeScript}, "需要填写脚本内容"},
		{"script 正常", Source{Type: TypeScript, ScriptSource: "output({})"}, ""},
		{"未知类型", Source{Type: "???"}, "未知数据源类型"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateSource(c.src)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("不应报错，实得 %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("应报含 %q 的错误，实得 %v", c.wantErr, err)
			}
		})
	}
}

func TestValidateConfig(t *testing.T) {
	t.Run("负刷新间隔", func(t *testing.T) {
		if err := ValidateConfig(&Config{RefreshInterval: -1}); err == nil {
			t.Fatal("负间隔应报错")
		}
	})

	t.Run("id 重复", func(t *testing.T) {
		cfg := &Config{Sources: []Source{
			{ID: "same", Type: TypeHTTP, URL: "https://a"},
			{ID: "same", Type: TypeHTTP, URL: "https://b"},
		}}
		if err := ValidateConfig(cfg); err == nil || !strings.Contains(err.Error(), "重复") {
			t.Fatalf("应报 id 重复，实得 %v", err)
		}
	})

	t.Run("带出源名称与序号", func(t *testing.T) {
		cfg := &Config{Sources: []Source{{ID: "s", Name: "我的源", Type: TypeHTTP}}}
		err := ValidateConfig(cfg)
		if err == nil || !strings.Contains(err.Error(), "我的源") || !strings.Contains(err.Error(), "第 1 个") {
			t.Fatalf("错误应定位到具体源，实得 %v", err)
		}
	})

	t.Run("全部合法", func(t *testing.T) {
		cfg := &Config{Sources: []Source{{ID: "a", Type: TypeHTTP, URL: "https://x"}}}
		if err := ValidateConfig(cfg); err != nil {
			t.Fatalf("不应报错: %v", err)
		}
	})
}

func TestSourceLabel(t *testing.T) {
	if sourceLabel(Source{Name: "N", ID: "I"}) != "N" {
		t.Fatal("优先用名称")
	}
	if sourceLabel(Source{ID: "I"}) != "I" {
		t.Fatal("无名称时用 id")
	}
	if sourceLabel(Source{}) != "未命名" {
		t.Fatal("都没有时给默认文案")
	}
}

func TestSourceTimeout(t *testing.T) {
	def := 20 * time.Second
	if got := SourceTimeout(Source{}, def); got != def {
		t.Fatalf("未指定应取默认，实得 %v", got)
	}
	if got := SourceTimeout(Source{Timeout: 7}, def); got != 7*time.Second {
		t.Fatalf("应取源自己的超时，实得 %v", got)
	}
}

// ---------------------------------------------------------------------------
// 环境与旧字段兼容
// ---------------------------------------------------------------------------

func TestConfigPathOverride(t *testing.T) {
	t.Setenv("LLMIO_QUOTA_CONFIG", "/tmp/custom.json")
	if got := ConfigPath(); got != "/tmp/custom.json" {
		t.Fatalf("应取环境变量，实得 %q", got)
	}
	t.Setenv("LLMIO_QUOTA_CONFIG", "   ")
	if got := ConfigPath(); got != DefaultConfigPath {
		t.Fatalf("空白应退回默认，实得 %q", got)
	}
}

func TestWriteAllowed(t *testing.T) {
	t.Setenv("LLMIO_QUOTA_ALLOW_WRITE", "")
	if !WriteAllowed() {
		t.Fatal("未设置时应允许写（默认开）")
	}
	t.Setenv("LLMIO_QUOTA_ALLOW_WRITE", "false")
	if WriteAllowed() {
		t.Fatal("显式 false 应为只读")
	}
	t.Setenv("LLMIO_QUOTA_ALLOW_WRITE", "true")
	if !WriteAllowed() {
		t.Fatal("true 应允许写")
	}
}

func TestLoadConfigAcceptsLegacyProvidersField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.json")
	// 旧 dashboard 的配置用 providers 这个字段名，应当能直接读进来
	legacy := `{"refreshInterval":30,"warningAt":70,"providers":[
		{"id":"old-1","name":"老数据源","type":"builtin","builtin":"deepseek"}]}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("旧格式应能读: %v", err)
	}
	if len(cfg.Sources) != 1 || cfg.Sources[0].ID != "old-1" {
		t.Fatalf("旧 providers 字段应被接住: %#v", cfg.Sources)
	}
	if cfg.RefreshInterval != 30 {
		t.Fatalf("全局字段应保留，实得 %d", cfg.RefreshInterval)
	}
}

func TestConfigUnmarshalPrefersSourcesOverProviders(t *testing.T) {
	// 两个字段都在时应以新的 sources 为准
	var c Config
	if err := json.Unmarshal([]byte(`{"sources":[{"id":"new"}],"providers":[{"id":"old"}]}`), &c); err != nil {
		t.Fatal(err)
	}
	if len(c.Sources) != 1 || c.Sources[0].ID != "new" {
		t.Fatalf("应以 sources 为准: %#v", c.Sources)
	}
}

func TestConfigUnmarshalInvalidJSON(t *testing.T) {
	var c Config
	if err := json.Unmarshal([]byte("{oops"), &c); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
}

func TestSaveConfigErrors(t *testing.T) {
	t.Run("写入失败", func(t *testing.T) {
		// 拿一个文件当"目录"，它下面的路径必然写不进去
		dir := t.TempDir()
		blocker := filepath.Join(dir, "blocker")
		if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := SaveConfig(filepath.Join(blocker, "sub", "c.json"), &Config{})
		if err == nil {
			t.Fatal("父路径是文件时应报错")
		}
	})

	t.Run("改名失败", func(t *testing.T) {
		// 目标路径已存在同名目录 -> Rename 失败，且不该留下 .tmp
		dir := t.TempDir()
		target := filepath.Join(dir, "c.json")
		if err := os.Mkdir(target, 0o755); err != nil {
			t.Fatal(err)
		}
		err := SaveConfig(target, &Config{})
		if err == nil {
			t.Fatal("目标是目录时改名应失败")
		}
		if _, statErr := os.Stat(target + ".tmp"); !os.IsNotExist(statErr) {
			t.Fatal("失败后不应残留 .tmp")
		}
	})
}

func TestConfigUnmarshalElementTypeError(t *testing.T) {
	// 数组元素类型不对时 UnmarshalJSON 应把错误抛出来，而不是静默吞掉
	var c Config
	err := json.Unmarshal([]byte(`{"sources":[123]}`), &c)
	if err == nil {
		t.Fatal("元素类型错误应报错")
	}
}

func TestSaveConfigMarshalError(t *testing.T) {
	// Query 是 any，塞进不可 JSON 序列化的值（channel）时应当明确报错，
	// 而不是写出半个文件
	path := filepath.Join(t.TempDir(), "c.json")
	err := SaveConfig(path, &Config{Sources: []Source{
		{ID: "s", Type: TypeHTTP, URL: "https://x", Query: make(chan int)},
	}})
	if err == nil || !strings.Contains(err.Error(), "序列化") {
		t.Fatalf("应报序列化失败，实得 %v", err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal("序列化失败时不应留下文件")
	}
}

func TestMaskSecretShortValues(t *testing.T) {
	// 回归：曾经 len(s)==1 时 s[:2] 会越界 panic，把整个请求打崩。
	// 1~2 位的值一律整体打码（本来也不该露出任何一位）。
	for _, in := range []string{"a", "ab"} {
		if got := MaskSecret(in); got != maskedMarker {
			t.Fatalf("MaskSecret(%q) = %q，期望整体打码 %q", in, got, maskedMarker)
		}
	}
	if got := MaskSecret("abc"); got != "ab****" {
		t.Fatalf("3 位应留前 2 位，实得 %q", got)
	}
}
