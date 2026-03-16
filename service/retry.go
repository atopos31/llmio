package service

import (
	"context"
	"errors"
	"time"
)

type RetryConfig struct {
	MaxRetry    int
	Timeout     time.Duration
	OnRetry     func(int)
	ShouldRetry func(error) bool
}

func DoRetry[T any](ctx context.Context, cfg RetryConfig, fn func() (T, error)) (T, error) {
	var zero T

	var timeoutCh <-chan time.Time
	if cfg.Timeout > 0 {
		timer := time.NewTimer(cfg.Timeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}

	for attempt := 0; attempt <= cfg.MaxRetry; attempt++ {
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-timeoutCh:
			return zero, errors.New("retry timeout")
		default:
		}

		if attempt > 0 && cfg.OnRetry != nil {
			cfg.OnRetry(attempt)
		}

		result, err := fn()
		if err != nil {
			continue
		}
		return result, nil
	}

	return zero, errors.New("retry exhausted")
}
