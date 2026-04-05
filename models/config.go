package models

import "gorm.io/gorm"

type Config struct {
	gorm.Model
	Key   string // 配置类型
	Value string // 配置内容
}

const (
	KeyAnthropicCountTokens = "anthropic_count_tokens"
	KeyLogCleanupPolicy     = "log_cleanup_policy"
)

type AnthropicCountTokens struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Version string `json:"version"`
}

type LogCleanupPolicy struct {
	Enabled       bool `json:"enabled"`
	RetentionDays int  `json:"retention_days"`
}
