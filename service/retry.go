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

type permanentRetryError struct {
	err error
}

func (e permanentRetryError) Error() string {
	return e.err.Error()
}

func (e permanentRetryError) Unwrap() error {
	return e.err
}

func MarkPermanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentRetryError{err: err}
}

func DoRetry[T any](ctx context.Context, cfg RetryConfig, fn func() (T, error)) (T, error) {
	var zero T
	if cfg.MaxRetry < 0 {
		return zero, errors.New("max retry must be non-negative")
	}

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
		if err == nil {
			return result, nil
		}

		shouldRetry := IsRetryable(err)
		if cfg.ShouldRetry != nil {
			shouldRetry = cfg.ShouldRetry(err)
		}
		if !shouldRetry {
			return zero, err
		}
		if attempt >= cfg.MaxRetry {
			return zero, err
		}
	}

	return zero, errors.New("retry exhausted")
}

func IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	var permanentErr permanentRetryError
	if errors.As(err, &permanentErr) {
		return false
	}
	return true
}
