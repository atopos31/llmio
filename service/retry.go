package service

import (
	"context"
	"errors"
	"time"
)

// RetryConfig 重试配置
type RetryConfig struct {
	MaxRetry           int           // 最大重试次数
	Timeout            time.Duration // 总超时时间
	OnRetry            func(int)      // 每次重试前的回调
	ShouldRetry        func(error) bool // 判断错误是否可重试
}

// DoRetry 通用的重试执行函数
// fn 是需要执行的函数，返回 (结果, 错误)
// 如果返回的错误 IsRetryable=true，则会重试
func DoRetry[T any](ctx context.Context, cfg RetryConfig, fn func() (T, error)) (T, error) {
	var zero T

	// 创建超时定时器
	timer := time.NewTimer(cfg.Timeout)
	defer timer.Stop()

	for attempt := 0; attempt <= cfg.MaxRetry; attempt++ {
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-timer.C:
			return zero, errors.New("retry timeout")
		default:
		}

		// 如果不是第一次尝试，且有 OnRetry 回调，则调用
		if attempt > 0 && cfg.OnRetry != nil {
			cfg.OnRetry(attempt)
		}

		result, err := fn()
		if err == nil {
			return result, nil
		}

		// 检查是否应该重试
		if cfg.ShouldRetry != nil && !cfg.ShouldRetry(err) {
			return zero, err
		}

		// 如果是最后一次尝试，返回错误
		if attempt >= cfg.MaxRetry {
			return zero, err
		}
	}

	return zero, errors.New("retry exhausted")
}

// IsRetryable 判断错误是否可重试
// 可以扩展更多错误类型判断
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	// 这里可以添加更多可重试错误的判断
	// 比如网络超时、5xx 错误等
	return true
}