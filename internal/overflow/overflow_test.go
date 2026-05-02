package overflow

import (
	"fmt"
	"testing"
)

func TestRetrySucceedsFirstAttempt(t *testing.T) {
	calls := 0
	data, err := Retry(RetryConfig{Attempts: 3, Backoff: nil}, func() ([]byte, int, error) {
		calls++
		return []byte("ok"), 200, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ok" {
		t.Fatalf("data = %q", data)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestRetryRetriesOn500(t *testing.T) {
	calls := 0
	data, err := Retry(RetryConfig{Attempts: 3, Backoff: nil}, func() ([]byte, int, error) {
		calls++
		if calls < 3 {
			return nil, 500, fmt.Errorf("server error")
		}
		return []byte("ok"), 200, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ok" {
		t.Fatalf("data = %q", data)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestRetryRetriesOn429(t *testing.T) {
	calls := 0
	_, err := Retry(RetryConfig{Attempts: 2, Backoff: nil}, func() ([]byte, int, error) {
		calls++
		return nil, 429, fmt.Errorf("rate limited")
	})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestRetryDoesNotRetryOn400(t *testing.T) {
	calls := 0
	_, err := Retry(RetryConfig{Attempts: 3, Backoff: nil}, func() ([]byte, int, error) {
		calls++
		return nil, 400, fmt.Errorf("bad request")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (should not retry 400)", calls)
	}
}

func TestIsRetryable(t *testing.T) {
	if !IsRetryable(429) {
		t.Fatal("429 should be retryable")
	}
	if !IsRetryable(500) {
		t.Fatal("500 should be retryable")
	}
	if !IsRetryable(503) {
		t.Fatal("503 should be retryable")
	}
	if IsRetryable(400) {
		t.Fatal("400 should not be retryable")
	}
	if IsRetryable(200) {
		t.Fatal("200 should not be retryable")
	}
}
