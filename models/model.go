package models

import (
	"time"

	"gorm.io/gorm"
)

type Provider struct {
	gorm.Model
	Name   string `gorm:"unique"`
	Type   string
	Config string
}

type OpenAIConfig struct {
	BaseUrl string `json:"base_url"`
	ApiKey  string `json:"api_key"`
}

type Model struct {
	gorm.Model
	Name   string `gorm:"unique"`
	Remark string
}

type ModelWithProvider struct {
	gorm.Model
	ModelID       uint
	ProviderModel string
	ProviderID    uint
	Weight        int
}

type ChatLog struct {
	gorm.Model
	Name          string
	ProviderModel string
	ProviderName  string
	Status        string // error or success

	Error          string // if status is error, this field will be set
	FirstChunkTime time.Duration
	ChunkTime      time.Duration
	Tps            float64
	Usage
}

type Usage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}
