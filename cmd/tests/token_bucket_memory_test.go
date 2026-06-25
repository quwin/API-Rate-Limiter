package main

import (
	"context"
	"quwin/api-gateway/internal/limiter"
	"testing"
	"time"
)

func TestTokenBucketAllowsBurstThenRejects(t *testing.T) {
	currentTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	l := limiter.NewTokenBucketMemoryLimiter(2, 1)
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

	second, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if !second.Allowed {
		t.Fatal("expected second request to be allowed")
	}

	third, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if third.Allowed {
		t.Fatal("expected third request to be rejected")
	}

	currentTime = currentTime.Add(time.Second)

	fourth, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if !fourth.Allowed {
		t.Fatal("expected request to be allowed after refill")
	}
}

func TestTokenBucketPanicsWhenConstructedWrong(t *testing.T) {
	tests := []struct {
		name       string
		capacity   int64
		refillRate float64
	}{
		{
			name:       "zero capacity",
			capacity:   0,
			refillRate: 1,
		},
		{
			name:       "negative capacity",
			capacity:   -1,
			refillRate: 1,
		},
		{
			name:       "zero refill rate",
			capacity:   1,
			refillRate: 0,
		},
		{
			name:       "negative refill rate",
			capacity:   1,
			refillRate: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatal("expected panic, got none")
				}
			}()

			limiter.NewTokenBucketMemoryLimiter(tt.capacity, tt.refillRate)
		})
	}
}

func TestTokenBucketReturnsContextError(t *testing.T) {
	l := limiter.NewTokenBucketMemoryLimiter(2, 1)

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
