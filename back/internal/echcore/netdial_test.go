package echcore

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestRetry_SucceedsOnFirstAttempt tests that Retry returns the result
// immediately when the function succeeds on the first try.
func TestRetry_SucceedsOnFirstAttempt(t *testing.T) {
	ctx := context.Background()
	result, err := Retry(ctx, 3, 10*time.Millisecond, func() (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Fatalf("expected 'ok', got %q", result)
	}
}

// TestRetry_SucceedsAfterRetries tests that Retry succeeds after transient
// failures.
func TestRetry_SucceedsAfterRetries(t *testing.T) {
	ctx := context.Background()
	attempts := 0
	result, err := Retry(ctx, 5, 1*time.Millisecond, func() (int, error) {
		attempts++
		if attempts < 3 {
			return 0, errors.New("transient")
		}
		return 42, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 42 {
		t.Fatalf("expected 42, got %d", result)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

// TestRetry_ExhaustsAttempts tests that Retry returns the last error when
// all attempts fail.
func TestRetry_ExhaustsAttempts(t *testing.T) {
	ctx := context.Background()
	attempts := 0
	_, err := Retry(ctx, 3, 1*time.Millisecond, func() (string, error) {
		attempts++
		return "", errors.New("always fails")
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

// TestRetry_ContextCancelled tests that Retry returns ctx.Err() when the
// context is cancelled.
func TestRetry_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := Retry(ctx, 3, 1*time.Millisecond, func() (string, error) {
		return "should not reach", nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

// TestRetry_GenericType tests that Retry works with generic types.
func TestRetry_GenericType(t *testing.T) {
	ctx := context.Background()
	result, err := Retry(ctx, 3, 1*time.Millisecond, func() (bool, error) {
		return true, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Fatal("expected true")
	}
}

// TestSleepCtx_ReturnsTrueWhenComplete tests that sleepCtx returns true
// when the timer fires normally.
func TestSleepCtx_ReturnsTrueWhenComplete(t *testing.T) {
	ctx := context.Background()
	if !sleepCtx(ctx, 1*time.Millisecond) {
		t.Fatal("expected true")
	}
}

// TestSleepCtx_ReturnsFalseWhenCancelled tests that sleepCtx returns false
// when the context is cancelled before the timer fires.
func TestSleepCtx_ReturnsFalseWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(1 * time.Millisecond)
		cancel()
	}()
	if sleepCtx(ctx, 5*time.Second) {
		t.Fatal("expected false when cancelled")
	}
}

// TestDialer_NotNil tests that Dialer returns a non-nil dialer.
func TestDialer_NotNil(t *testing.T) {
	d := Dialer()
	if d == nil {
		t.Fatal("expected non-nil dialer")
	}
	if d.Timeout != OpTimeout {
		t.Fatalf("expected timeout %v, got %v", OpTimeout, d.Timeout)
	}
}

// TestTransport_NotNil tests that Transport returns a non-nil transport.
func TestTransport_NotNil(t *testing.T) {
	tr := Transport()
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
	if tr.MaxIdleConns != 100 {
		t.Fatalf("expected MaxIdleConns 100, got %d", tr.MaxIdleConns)
	}
}

// TestHTTPClient_NotNil tests that HTTPClient returns a non-nil http client.
func TestHTTPClient_NotNil(t *testing.T) {
	c := HTTPClient(5 * time.Second)
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.Timeout != 5*time.Second {
		t.Fatalf("expected timeout 5s, got %v", c.Timeout)
	}
}
