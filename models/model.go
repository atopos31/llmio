package models

import (
	"time"

	"gorm.io/gorm"
)

type Provider struct {
	gorm.Model
	Name    string
	Type    string
	Config  string
	Console string // 控制台地址
}

type AnthropicConfig struct {
	BaseUrl string `json:"base_url"`
	ApiKey  string `json:"api_key"`
	Version string `json:"version"`
}

type Model struct {
	gorm.Model
	Name     string
	Remark   string
	MaxRetry int   // 重试次数限制
	TimeOut  int   // 超时时间 单位秒
	IOLog    *bool // 是否记录IO
}

type ModelWithProvider struct {
	gorm.Model
	ModelID          uint
	ProviderModel    string
	ProviderID       uint
	ToolCall         *bool // 能否接受带有工具调用的请求
	StructuredOutput *bool // 能否接受带有结构化输出的请求
	Image            *bool // 能否接受带有图片的请求(视觉)
	WithHeader       *bool // 是否携带header
	Status           *bool // 是否启用
	Weight           int
}

type ChatLog struct {
	gorm.Model
	Name          string
	ProviderModel string
	ProviderName  string
	Status        string // error or success
	Style         string // 类型
	UserAgent     string // 用户代理
	RemoteIP      string // 访问ip
	ChatIO        bool   // 是否开启IO记录

	Error          string        // if status is error, this field will be set
	Retry          int           // 重试次数
	ProxyTime      time.Duration // 代理耗时
	FirstChunkTime time.Duration // 首个chunk耗时
	ChunkTime      time.Duration // chunk耗时
	Tps            float64
	Usage
}

func (l ChatLog) WithError(err error) ChatLog {
	l.Error = err.Error()
	l.Status = "error"
	return l
}

type Usage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
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
