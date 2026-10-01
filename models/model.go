package models

import (
	"net/http"
	"time"

	"github.com/atopos31/llmio/consts"
	"gorm.io/gorm"
)

type Provider struct {
	gorm.Model
	Name         string
	Type         string
	Config       string
	Console      string // 控制台地址
	Proxy        string // HTTP 代理地址
	ErrorMatcher string // 响应体错误识别规则，多行或分号分隔 sample
}

type AnthropicConfig struct {
	BaseUrl string `json:"base_url"`
	ApiKey  string `json:"api_key"`
	Version string `json:"version"`
}

type Model struct {
	gorm.Model
	Name         string
	Remark       string
	MaxRetry     int    // 重试次数限制
	TimeOut      int    // 超时时间 单位秒
	Strategy     string // 负载均衡策略 默认 lottery
	Breaker      *bool  // 是否开启熔断
	DisplayOrder int    // 模型展示顺序，值越大越靠前
	// PreferDirect 优先匹配相同协议：候选池里先挑说客户端协议的提供商，一个都没有
	// 才退回可转换的那些。关掉则整池按权重摇（见 service.poolFor）。
	// nil 表示没配过（迁移上来的老行），按**开**处理——转换功能上线前候选池只有
	// 本协议的，这样老模型的行为不会因为多出一列而改变。
	PreferDirect *bool
}

type ModelWithProvider struct {
	gorm.Model
	ModelID          uint
	ProviderModel    string
	ProviderID       uint
	ToolCall         *bool             // 能否接受带有工具调用的请求
	StructuredOutput *bool             // 能否接受带有结构化输出的请求
	Image            *bool             // 能否接受带有图片的请求(视觉)
	WithHeader       *bool             // 是否透传header
	Status           *bool             // 是否启用
	CustomerHeaders  map[string]string `gorm:"serializer:json"` // 自定义headers
	ExtraBody        map[string]any    `gorm:"serializer:json"` // 额外请求体参数
	Weight           int
	InputPrice       *float64
	CacheReadPrice   *float64
	OutputPrice      *float64
	Currency         string
	// Peak 这一条关联的峰谷计费条款，nil 表示没配（按基础价计费）。
	//
	// 与三档单价同处一行是刻意的：乘数就是乘在它们上面的，
	// 拆到别处就会出现"价格改了、条款还指着旧的那套"的可能。
	Peak *PeakTerms `gorm:"serializer:json"`
}

// ChatLog 请求日志，也是分析聚合的事实表。
//
// 此处刻意不内嵌 gorm.Model：分析聚合全部按 created_at 过滤与分桶，
// 需要 created_at 参与复合索引，而内嵌结构的字段无法单独打标签。
// 字段集合与 gorm.Model 等价（ID / CreatedAt / UpdatedAt / DeletedAt），
// 因此对外仍以 ID、CreatedAt、DeletedAt 访问，JSON 形态不变。
//
// 索引按聚合的实际查询形态设计：
//   - created_at 单列：范围过滤 + ORDER BY
//   - status, created_at：趋势与成功率
//   - auth_key_id, created_at：按 Key 下钻
//   - name, created_at：按模型下钻
type ChatLog struct {
	ID        uint      `gorm:"primarykey"`
	CreatedAt time.Time `gorm:"index;index:idx_chat_logs_status_created,priority:2;index:idx_chat_logs_key_created,priority:2;index:idx_chat_logs_name_created,priority:2"`
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`

	Name          string `gorm:"index;index:idx_chat_logs_name_created,priority:1"`
	TraceID       string `gorm:"index"`
	ProviderModel string `gorm:"index"`
	ProviderName  string `gorm:"index"`
	Status        string `gorm:"index;index:idx_chat_logs_status_created,priority:1"` // error or success
	Style         string // 类型（客户端协议）
	// UpstreamStyle 实际使用的上游协议。直连时与 Style 相同；不同即说明这次转发经过了
	// 一层协议翻译（见 service/bridge.go）。
	UpstreamStyle string `gorm:"index" json:"upstream_style,omitempty"`
	// BridgeNotes 这次协议翻译"改了什么"，逗号分隔的短码（见 bridge.Note）。
	// 只作排查用：丢了哪个参数、补了什么默认值，客户端从响应里看不出来，这里留个线索。
	BridgeNotes string `json:"bridge_notes,omitempty"`
	UserAgent   string `gorm:"index"` // 用户代理
	RemoteIP    string // 访问ip
	AuthKeyID   uint   `gorm:"index;index:idx_chat_logs_key_created,priority:1"` // 使用的AuthKey ID
	SessionID   string `gorm:"index"`                                            // 请求体中的session_id
	ChatIO      bool   // 是否开启IO记录

	Error          string        // if status is error, this field will be set
	Retry          int           // 重试次数
	ProxyTime      time.Duration // 代理耗时
	FirstChunkTime time.Duration // 首个chunk耗时
	ChunkTime      time.Duration // chunk耗时
	Tps            float64
	Size           int // 响应大小 字节
	Usage
	InputPrice     float64 `json:"input_price"`
	CacheReadPrice float64 `json:"cache_read_price"`
	OutputPrice    float64 `json:"output_price"`
	Currency       string  `json:"currency"`
	// PeakPeriod 记录本条请求命中的计费时段名，空表示按基础价计费。
	// 只作展示与排查用——价格本身已按实际生效值快照到上面三个字段，
	// 因此成本计算不依赖本字段，改了时段配置也不会影响历史成本。
	PeakPeriod string `json:"peak_period,omitempty"`
}

func (l ChatLog) WithError(err error) ChatLog {
	l.Error = err.Error()
	l.Status = consts.StatusError
	return l
}

type Usage struct {
	PromptTokens        int64               `json:"prompt_tokens"`
	CompletionTokens    int64               `json:"completion_tokens"`
	TotalTokens         int64               `json:"total_tokens"`
	PromptTokensDetails PromptTokensDetails `json:"prompt_tokens_details" gorm:"serializer:json"`
}

type PromptTokensDetails struct {
	CachedTokens int64 `json:"cached_tokens"`
	AudioTokens  int64 `json:"audio_tokens"`
}

type ChatIO struct {
	gorm.Model
	LogId uint
	Input string
	OutputUnion
}

type OutputUnion struct {
	OfString      string
	OfStringArray []string `gorm:"serializer:json"`
}

type ReqMeta struct {
	UserAgent string // 用户代理
	RemoteIP  string // 访问ip
	Header    http.Header
}

type AuthKey struct {
	gorm.Model
	Name       string // 项目名称
	Key        string
	Status     *bool      // 是否启用
	IOLog      *bool      // 是否记录IO
	AllowAll   *bool      // 是否允许所有模型
	Models     []string   `gorm:"serializer:json"` // 允许的模型列表
	ExpiresAt  *time.Time // nil=永不过期，有值=具体过期时间
	UsageCount int64      // 使用次数统计
	LastUsedAt *time.Time // 最后使用时间
}
