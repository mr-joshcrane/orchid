package overflow

import (
	"fmt"
	"math"
	"net/http"
	"time"
)

type OverflowPolicy int

const (
	OverflowFail OverflowPolicy = iota
	OverflowTruncateOldestToolResults
)

type BackoffFunc func(attempt int) time.Duration

func ExponentialBackoff(attempt int) time.Duration {
	return time.Duration(math.Pow(2, float64(attempt))) * 100 * time.Millisecond
}

func IsRetryable(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests || statusCode >= 500
}

type RetryConfig struct {
	Attempts int
	Backoff  BackoffFunc
}

func Retry(cfg RetryConfig, fn func() ([]byte, int, error)) ([]byte, error) {
	var lastErr error
	for attempt := range cfg.Attempts {
		data, status, err := fn()
		if err == nil {
			return data, nil
		}
		lastErr = err
		if status > 0 && !IsRetryable(status) {
			return nil, err
		}
		if attempt < cfg.Attempts-1 && cfg.Backoff != nil {
			time.Sleep(cfg.Backoff(attempt))
		}
	}
	return nil, fmt.Errorf("exhausted %d retries: %w", cfg.Attempts, lastErr)
}
