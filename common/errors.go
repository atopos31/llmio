package common

import (
	"fmt"
)

// 自定义错误类型，便于错误分类和处理

// ModelError 模型相关错误
type ModelError struct {
	ModelName string
	Reason    string
}

func (e *ModelError) Error() string {
	return fmt.Sprintf("model error: %s - %s", e.ModelName, e.Reason)
}

func NewModelError(modelName, reason string) *ModelError {
	return &ModelError{
		ModelName: modelName,
		Reason:    reason,
	}
}

// ProviderError 供应商相关错误
type ProviderError struct {
	ProviderName string
	ProviderType string
	Reason       string
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("provider error: %s (%s) - %s", e.ProviderName, e.ProviderType, e.Reason)
}

func NewProviderError(providerName, providerType, reason string) *ProviderError {
	return &ProviderError{
		ProviderName: providerName,
		ProviderType: providerType,
		Reason:       reason,
	}
}

// AuthError 认证相关错误
type AuthError struct {
	Type   string // token, key, expired, etc.
	Reason string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("auth error: %s - %s", e.Type, e.Reason)
}

func NewAuthError(authType, reason string) *AuthError {
	return &AuthError{
		Type:   authType,
		Reason: reason,
	}
}

// BalancerError 负载均衡器相关错误
type BalancerError struct {
	Strategy string
	Reason   string
}

func (e *BalancerError) Error() string {
	return fmt.Sprintf("balancer error: %s - %s", e.Strategy, e.Reason)
}

func NewBalancerError(strategy, reason string) *BalancerError {
	return &BalancerError{
		Strategy: strategy,
		Reason:   reason,
	}
}

// ConfigError 配置相关错误
type ConfigError struct {
	ConfigType string
	Field      string
	Reason     string
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("config error: %s.%s - %s", e.ConfigType, e.Field, e.Reason)
}

func NewConfigError(configType, field, reason string) *ConfigError {
	return &ConfigError{
		ConfigType: configType,
		Field:      field,
		Reason:     reason,
	}
}
