package model

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
