package models

import "gorm.io/gorm"

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
	ModelID      uint
	ProviderName string
	ProviderID   uint
	Weight       int
}
