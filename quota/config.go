package quota

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// 配额配置：读写、归一、脱敏、密钥回填。
//
// ## 为什么落文件而不是进 configs KV 表
//
// 计划 §3.5 已定：配置落 `./db/quota.config.json`（与 SQLite 同目录、二进制外、
// 易备份、可手工编辑），路径可用 `LLMIO_QUOTA_CONFIG` 覆盖。
// 配额配置与脚本源是**文件形态的运维资产**，可手工编辑是有意设计。
//
// ## 脚本源码为什么内联在配置里
//
// 原实现把在线编辑的脚本落成 node/python 等**可执行文件**，运行时再映射到解释器
// （`scriptStore.js` 的 LANGS / resolveCommand / writeTemp）。合并后 §3.4 已定
// 「goja 沙箱替换 OS 进程 spawn」，于是这一整层都不再需要：
//
//   - 只有 JavaScript 一种语言，没有解释器映射
//   - 源码内联在配置 JSON 里，可手工编辑、随配置一起备份
//   - 试跑把源码**直接**交给沙箱，不需要写临时文件再删（原实现要写 tmp 再 unlink）
//
// 因此 `scriptstore.go` 这个文件不再存在——不是漏了，是被这个决定消掉了。

// DefaultConfigPath 是配额配置的默认落点（与 SQLite 同目录）。
const DefaultConfigPath = "./db/quota.config.json"

// 默认值。与原实现保持一致。
const (
	DefaultRefreshInterval = 120 // 秒；查询类数据源较慢
	DefaultWarningAt       = 80  // 使用率告警阈值（%）
)

// DataSourceType 是数据源类型。
type DataSourceType string

const (
	TypeBuiltin DataSourceType = "builtin"
	TypeHTTP    DataSourceType = "http"
	TypeScript  DataSourceType = "script"
)

// Source 是一个数据源的配置。
//
// 字段是**扁平**的，而不是按类型做联合体：配置是给人手工编辑的 JSON，
// 扁平形状比带标签的联合体更好改（原 JS 版也是扁平 + normalizeProviderShape 分派）。
// 各类型只用到自己那一组字段，校验时按类型检查。
type Source struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Enabled bool           `json:"enabled"`
	Type    DataSourceType `json:"type"`
	Note    string         `json:"note,omitempty"`
	// WarningAt 覆盖全局告警阈值；0 表示沿用全局。
	WarningAt float64 `json:"warningAt,omitempty"`
	// Timeout 单次取数超时（秒）。0 表示按类型取默认。
	Timeout int `json:"timeout,omitempty"`

	// ---- builtin 类型 ----
	Builtin   string         `json:"builtin,omitempty"`
	BaseURL   string         `json:"baseUrl,omitempty"`
	APIKey    string         `json:"apiKey,omitempty"`
	Path      string         `json:"path,omitempty"`
	Method    string         `json:"method,omitempty"`
	Query     any            `json:"query,omitempty"`
	Headers   map[string]any `json:"headers,omitempty"`
	ItemsPath string         `json:"itemsPath,omitempty"`
	Map       map[string]any `json:"map,omitempty"`

	// ---- http 类型 ----
	URL       string         `json:"url,omitempty"`
	Body      any            `json:"body,omitempty"`
	Auth      *HTTPAuth      `json:"auth,omitempty"`
	Constants map[string]any `json:"constants,omitempty"`

	// ---- script 类型（goja 沙箱）----
	ScriptSource string            `json:"scriptSource,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	// AllowFetch 是否给脚本开放 fetch。默认关——沙箱面积越小越安全。
	AllowFetch bool `json:"allowFetch,omitempty"`
}

// Config 是配额配置的顶层结构。
type Config struct {
	// RefreshInterval 整体刷新间隔（秒）。
	RefreshInterval int `json:"refreshInterval"`
	// WarningAt 全局告警阈值（%）。
	WarningAt float64 `json:"warningAt"`
	// Sources 是全部数据源。
	Sources []Source `json:"sources"`
}

// UnmarshalJSON 额外兼容旧字段名 providers。
//
// 原 dashboard 的配置里这个数组叫 providers。手工编辑过的存量配置
// 应当能直接拿来用，而不是"打开面板发现数据源全没了"。
func (c *Config) UnmarshalJSON(data []byte) error {
	var raw struct {
		RefreshInterval int      `json:"refreshInterval"`
		WarningAt       float64  `json:"warningAt"`
		Sources         []Source `json:"sources"`
		Providers       []Source `json:"providers"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.RefreshInterval = raw.RefreshInterval
	c.WarningAt = raw.WarningAt
	c.Sources = raw.Sources
	if len(c.Sources) == 0 {
		c.Sources = raw.Providers
	}
	return nil
}

// ConfigPath 返回配置文件路径，LLMIO_QUOTA_CONFIG 可覆盖。
func ConfigPath() string {
	if p := strings.TrimSpace(os.Getenv("LLMIO_QUOTA_CONFIG")); p != "" {
		return p
	}
	return DefaultConfigPath
}

// WriteAllowed 表示是否允许写操作。
//
// 原设计是「仅回环 + 显式开启」，理由是"写权限 = 可在主机上执行任意命令"。
// 合并后 goja 沙箱替换了 OS 进程 spawn，且整个 /api 已在 TOKEN 之后，
// 这条限制的前提消失（计划 §3.3），降级为一个开关：
// 只有显式设成 false 才进只读模式。
func WriteAllowed() bool {
	return strings.TrimSpace(os.Getenv("LLMIO_QUOTA_ALLOW_WRITE")) != "false"
}

// LoadConfig 读配置。文件不存在时返回带默认值的空配置（不是错误）——
// 首次部署没有这个文件是正常的。
func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			c := &Config{}
			normalizeConfig(c)
			return c, nil
		}
		return nil, fmt.Errorf("读取配额配置失败: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("配额配置不是合法 JSON: %w", err)
	}
	normalizeConfig(&cfg)
	return &cfg, nil
}

// SaveConfig 写配置。目录不存在时创建；权限 0600——
// 配置里可能有明文密钥（R5）。
func SaveConfig(path string, cfg *Config) error {
	out := *cfg
	normalizeConfig(&out)

	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建配置目录失败: %w", err)
		}
	}
	data, err := json.MarshalIndent(&out, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配额配置失败: %w", err)
	}
	// 原子写：先写临时文件再改名，避免写一半被中断留下半个 JSON
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("写入配额配置失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("保存配额配置失败: %w", err)
	}
	return nil
}

// normalizeConfig 填默认值并归一每个数据源。
func normalizeConfig(c *Config) {
	if c.RefreshInterval <= 0 {
		c.RefreshInterval = DefaultRefreshInterval
	}
	if c.WarningAt <= 0 {
		c.WarningAt = DefaultWarningAt
	}
	if c.Sources == nil {
		c.Sources = []Source{}
	}
	for i := range c.Sources {
		normalizeSource(&c.Sources[i])
	}
}

// normalizeSource 归一单个数据源。
func normalizeSource(s *Source) {
	s.ID = strings.TrimSpace(s.ID)
	if s.ID == "" {
		s.ID = newSourceID()
	}
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		s.Name = s.ID
	}
	switch s.Type {
	case TypeBuiltin, TypeHTTP, TypeScript:
		// ok
	default:
		s.Type = TypeBuiltin
	}
	if s.Type == TypeBuiltin && strings.TrimSpace(s.Builtin) == "" {
		s.Builtin = "deepseek"
	}
	if s.Method != "" {
		s.Method = strings.ToUpper(strings.TrimSpace(s.Method))
	}
}

// newSourceID 生成数据源 id。
func newSourceID() string {
	return NewSourceID(randHex(8))
}

// NewSourceID 由给定的后缀拼出一个数据源 id。
//
// 导出是为了让编排层能**与保存路径共用同一条生成规则**地预置 id
// （见 service.QuotaStore.UpsertSource）。不要在别处另写一套拼法。
func NewSourceID(suffix string) string {
	return "src-" + suffix
}

// ---------------------------------------------------------------------------
// 脱敏与密钥回填
// ---------------------------------------------------------------------------

// secretKeyRe 匹配"看起来像密钥"的环境变量名。原实现用同一个正则。
var secretKeyRe = regexp.MustCompile(`(?i)pass|secret|token|key|cookie`)

// maskedMarker 是掩码里必然出现的片段，用于判断"这个值是掩码还是用户新填的"。
const maskedMarker = "****"

// MaskSecret 打码。短值只留前 2 位。
//
// 长度不足 2 的前缀截取会越界 panic，因此 ≤2 位一律整体打码——
// 顺带也更安全：1~2 位的值本来就不该露出任何一位。
func MaskSecret(v string) string {
	s := strings.TrimSpace(v)
	if s == "" {
		return ""
	}
	if len(s) <= 2 {
		return maskedMarker
	}
	if len(s) <= 10 {
		return s[:2] + maskedMarker
	}
	return s[:6] + maskedMarker + s[len(s)-4:]
}

// IsMasked 判断一个值是不是掩码（未改动）而不是用户新填的真实值。
func IsMasked(v string) bool {
	return strings.Contains(v, maskedMarker)
}

// MaskSource 返回脱敏后的副本，可直接下发给前端。
//
// **响应永不回传明文**：apiKey 打码；env 里键名像密钥的打码。
// 例外是 scriptSource——编辑器必须显示它才能编辑，它本身不含凭据
// （凭据应从 env 读）。
func MaskSource(s Source) Source {
	out := s
	if out.APIKey != "" {
		out.APIKey = MaskSecret(out.APIKey)
	}
	if len(out.Env) > 0 {
		masked := make(map[string]string, len(out.Env))
		for k, v := range out.Env {
			if secretKeyRe.MatchString(k) {
				masked[k] = MaskSecret(v)
			} else {
				masked[k] = v
			}
		}
		out.Env = masked
	}
	return out
}

// MaskConfig 返回脱敏后的整份配置。
func MaskConfig(c *Config) *Config {
	out := *c
	out.Sources = make([]Source, 0, len(c.Sources))
	for _, s := range c.Sources {
		out.Sources = append(out.Sources, MaskSource(s))
	}
	return &out
}

// HasSecret 判断某个源是否已配置密钥（供前端显示"已配置/未配置"）。
func HasSecret(s Source) bool {
	return strings.TrimSpace(s.APIKey) != ""
}

// ResolveSourceSecrets 把"前端回传的掩码/空值"还原成已保存的真实凭据。
//
// 两条规则（与原实现一致，差别是刻意的）：
//
//   - apiKey：**空值也还原**。编辑弹窗不回显明文密钥，用户不动它就是空的，
//     不能因此把已保存的密钥抹掉。
//   - env：只有掩码值才还原，真正留空的保持留空——用户可能就是想把某个变量清掉。
func ResolveSourceSecrets(incoming Source, saved *Source) Source {
	out := incoming
	if saved == nil {
		return out
	}
	if out.APIKey == "" || IsMasked(out.APIKey) {
		out.APIKey = saved.APIKey
	}
	if len(out.Env) > 0 {
		resolved := make(map[string]string, len(out.Env))
		for k, v := range out.Env {
			if IsMasked(v) {
				resolved[k] = saved.Env[k]
				continue
			}
			resolved[k] = v
		}
		out.Env = resolved
	}
	return out
}

// ---------------------------------------------------------------------------
// 校验
// ---------------------------------------------------------------------------

// ValidateSource 校验一个数据源是否可以保存。
//
// 只拦"存下去也跑不起来"的配置，不做过度校验——配额源本来就是长尾形态。
func ValidateSource(s Source) error {
	switch s.Type {
	case TypeBuiltin:
		if _, ok := findBuiltin(s.Builtin); !ok {
			return fmt.Errorf("未知内置适配器：%s", s.Builtin)
		}
		spec, _ := findBuiltin(s.Builtin)
		if spec.info.Generic && strings.TrimSpace(s.Path) == "" {
			return fmt.Errorf("「自定义端点」类型需要填写请求路径(path)")
		}
		if spec.info.NeedsLogin && len(s.Env) == 0 {
			return fmt.Errorf("「%s」需要账号会话，请在 env 里配置 %s",
				spec.info.Label, strings.Join(spec.info.EnvKeys, " / "))
		}
	case TypeHTTP:
		if strings.TrimSpace(s.URL) == "" {
			return fmt.Errorf("HTTP 类型需要填写 url")
		}
	case TypeScript:
		if strings.TrimSpace(s.ScriptSource) == "" {
			return fmt.Errorf("脚本类型需要填写脚本内容")
		}
	default:
		return fmt.Errorf("未知数据源类型：%s", s.Type)
	}
	return nil
}

// ValidateConfig 校验整份配置。
func ValidateConfig(c *Config) error {
	if c.RefreshInterval < 0 {
		return fmt.Errorf("刷新间隔不能为负")
	}
	seen := map[string]bool{}
	for i, s := range c.Sources {
		if err := ValidateSource(s); err != nil {
			return fmt.Errorf("第 %d 个数据源「%s」：%w", i+1, sourceLabel(s), err)
		}
		if seen[s.ID] {
			return fmt.Errorf("数据源 id 重复：%s", s.ID)
		}
		seen[s.ID] = true
	}
	return nil
}

func sourceLabel(s Source) string {
	if strings.TrimSpace(s.Name) != "" {
		return s.Name
	}
	if strings.TrimSpace(s.ID) != "" {
		return s.ID
	}
	return "未命名"
}

// SourceTimeout 返回该源的实际超时。
func SourceTimeout(s Source, fallback time.Duration) time.Duration {
	if s.Timeout > 0 {
		return time.Duration(s.Timeout) * time.Second
	}
	return fallback
}
