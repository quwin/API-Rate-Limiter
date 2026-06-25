package main

import (
	"context"
	"quwin/api-gateway/internal/limiter"
	"testing"
	"time"
)

func TestFixedWindowAllowsRequestsUnderLimit(t *testing.T) {
	currentTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	l := limiter.NewFixedWindowMemoryLimiter(2, time.Minute)
	l.Now = func() time.Time {
		return currentTime
	}

	first, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if !first.Allowed {
		t.Fatal("expected first request to be allowed")
	}
	if first.Remaining != 1 {
		t.Fatalf("expected 1 remaining request, got %d", first.Remaining)
	}

	second, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if !second.Allowed {
		t.Fatal("expected second request to be allowed")
	}
	if second.Remaining != 0 {
		t.Fatalf("expected 0 remaining requests, got %d", second.Remaining)
	}
}

func TestFixedWindowPanicsWhenConstructedWrong(t *testing.T) {
	tests := []struct {
		name   string
		limit  int64
		window time.Duration
	}{
		{
			name:   "zero limit",
			limit:  0,
			window: time.Minute,
		},
		{
			name:   "negative limit",
			limit:  -1,
			window: time.Minute,
		},
		{
			name:   "zero window",
			limit:  1,
			window: 0,
		},
		{
			name:   "negative window",
			limit:  1,
			window: -time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatal("expected panic, got none")
				}
			}()

			limiter.NewFixedWindowMemoryLimiter(tt.limit, tt.window)
		})
	}
}

func TestFixedWindowReturnsContextError(t *testing.T) {
	currentTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	l := limiter.NewFixedWindowMemoryLimiter(2, time.Minute)
	l.Now = func() time.Time {
		return currentTime
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	decision, err := l.Allow(ctx, "user-1")
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if decision != (limiter.Decision{}) {
		t.Fatalf("expected zero-value decision, got %+v", decision)
	}
}

func TestFixedWindowRejectsWhenCounterReachesLimit(t *testing.T) {
	limiter := limiter.NewFixedWindowMemoryLimiter(2, time.Minute)

	_, err := limiter.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}

	_, err = limiter.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}

	decision, err := limiter.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}

	if decision.Allowed {
		t.Fatal("expected request to be rejected")
	}

	if decision.Remaining != 0 {
		t.Fatalf("expected 0 remaining requests, got %d", decision.Remaining)
	}

	if decision.Limit != 2 {
		t.Fatalf("expected limit 2, got %d", decision.Limit)
	}

	if decision.RetryAfter <= 0 {
		t.Fatalf("expected positive RetryAfter, got %s", decision.RetryAfter)
	}

	if decision.RetryAfter > time.Minute {
		t.Fatalf("expected RetryAfter to be <= 1 minute, got %s", decision.RetryAfter)
	}
}
