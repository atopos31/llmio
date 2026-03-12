package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDoRetrySuccessAfterRetry(t *testing.T) {
	attempts := 0
	value, err := DoRetry(context.Background(), RetryConfig{
		MaxRetry: 2,
		Timeout:  time.Second,
	}, func() (int, error) {
		attempts++
		if attempts < 2 {
			return 0, errors.New("temporary error")
		}
		return 42, nil
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if value != 42 {
		t.Fatalf("expected value 42, got %d", value)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}

func TestDoRetryStopsOnPermanentError(t *testing.T) {
	attempts := 0
	expectedErr := errors.New("permanent error")

	_, err := DoRetry(context.Background(), RetryConfig{
		MaxRetry: 3,
		Timeout:  time.Second,
	}, func() (int, error) {
		attempts++
		return 0, MarkPermanent(expectedErr)
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected permanent error, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", attempts)
	}
}

func TestDoRetryTimeout(t *testing.T) {
	_, err := DoRetry(context.Background(), RetryConfig{
		MaxRetry: 1,
		Timeout:  10 * time.Millisecond,
	}, func() (int, error) {
		time.Sleep(20 * time.Millisecond)
		return 0, errors.New("temporary error")
	})
	if err == nil || err.Error() != "retry timeout" {
		t.Fatalf("expected retry timeout, got %v", err)
	}
}
