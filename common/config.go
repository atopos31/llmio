package common

import (
	"encoding/json"
	"log/slog"
	"os"
)

// AppConfig 应用配置结构
type AppConfig struct {
	GinMode  string `json:"gin_mode"`  // release 或 debug
	Token    string `json:"token"`     // API 访问令牌
	Port     string `json:"port"`      // 服务端口
	Timezone string `json:"timezone"`  // 时区设置，如 "Asia/Shanghai"
	DBPath   string `json:"db_path"`   // 数据库路径
}

// LoadConfig 从 llmio.json 加载配置，如果文件不存在则使用环境变量
func LoadConfig() *AppConfig {
	config := &AppConfig{
		GinMode:  "debug",
		Token:    "",
		Port:     "7070",
		Timezone: "Local",
		DBPath:   "./db/llmio.db",
	}

	// 尝试从 llmio.json 读取配置
	configFile := "llmio.json"
	if data, err := os.ReadFile(configFile); err == nil {
		if err := json.Unmarshal(data, config); err != nil {
			slog.Warn("Failed to parse llmio.json, using default/env config", "error", err)
		} else {
			slog.Info("Loaded configuration from llmio.json")
		}
	} else {
		slog.Info("llmio.json not found, using environment variables")
	}

	// 环境变量优先级更高，可以覆盖配置文件
	if envToken := os.Getenv("TOKEN"); envToken != "" {
		config.Token = envToken
	}
	if envGinMode := os.Getenv("GIN_MODE"); envGinMode != "" {
		config.GinMode = envGinMode
	}
	if envPort := os.Getenv("LLMIO_SERVER_PORT"); envPort != "" {
		config.Port = envPort
	}
	if envTZ := os.Getenv("TZ"); envTZ != "" {
		config.Timezone = envTZ
	}
	if envDBPath := os.Getenv("DB_PATH"); envDBPath != "" {
		config.DBPath = envDBPath
	}

	return config
}

// ApplyConfig 应用配置到环境
func (c *AppConfig) ApplyConfig() {
	// 设置 GIN_MODE
	if c.GinMode != "" {
		os.Setenv("GIN_MODE", c.GinMode)
		slog.Info("Applied GIN_MODE", "mode", c.GinMode)
	}

	// 设置时区
	if c.Timezone != "" && c.Timezone != "Local" {
		os.Setenv("TZ", c.Timezone)
		slog.Info("Timezone set", "tz", c.Timezone)
	}
}

// Validate 验证配置有效性
func (c *AppConfig) Validate() error {
	if c.Token == "" {
		slog.Warn("TOKEN is not set! Please set it in llmio.json or environment variable")
	}
	return nil
}