package limiter_test

import (
	"context"
	"quwin/api-gateway/internal/limiter"
	"testing"
	"time"
)

func TestSlidingWindowAllowsRequestsUnderLimit(t *testing.T) {
	currentTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	l := limiter.NewSlidingWindowLogLimiter(2, time.Minute)
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
	if first.Limit != 2 {
		t.Fatalf("expected limit 2, got %d", first.Limit)
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
	if second.Limit != 2 {
		t.Fatalf("expected limit 2, got %d", second.Limit)
	}
}

func TestSlidingWindowRejectsWhenLimitReached(t *testing.T) {
	currentTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	l := limiter.NewSlidingWindowLogLimiter(2, time.Minute)
	l.Now = func() time.Time {
		return currentTime
	}

	_, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}

	currentTime = currentTime.Add(10 * time.Second)

	_, err = l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}

	currentTime = currentTime.Add(10 * time.Second)

	decision, err := l.Allow(context.Background(), "user-1")
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

	// First request was at 12:00:00.
	// Current request is at 12:00:20.
	// Oldest request leaves the 1-minute window at 12:01:00.
	// RetryAfter should be 40 seconds.
	if decision.RetryAfter != 40*time.Second {
		t.Fatalf("expected RetryAfter 40s, got %s", decision.RetryAfter)
	}
}

func TestSlidingWindowAllowsAfterOldestRequestExpires(t *testing.T) {
	currentTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	l := limiter.NewSlidingWindowLogLimiter(2, time.Minute)
	l.Now = func() time.Time {
		return currentTime
	}

	_, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}

	currentTime = currentTime.Add(10 * time.Second)

	_, err = l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}

	currentTime = currentTime.Add(10 * time.Second)

	rejected, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Allowed {
		t.Fatal("expected request to be rejected before oldest request expires")
	}

	// Move just beyond the first request's 1-minute window.
	// The first request at 12:00:00 should no longer count.
	currentTime = time.Date(2026, 1, 1, 12, 1, 0, 1, time.UTC)

	allowed, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}

	if !allowed.Allowed {
		t.Fatal("expected request to be allowed after oldest request expires")
	}

	if allowed.Remaining != 0 {
		t.Fatalf("expected 0 remaining requests, got %d", allowed.Remaining)
	}

	if allowed.Limit != 2 {
		t.Fatalf("expected limit 2, got %d", allowed.Limit)
	}
}

func TestSlidingWindowDoesNotResetAllRequestsAtFixedBoundary(t *testing.T) {
	currentTime := time.Date(2026, 1, 1, 12, 0, 30, 0, time.UTC)

	l := limiter.NewSlidingWindowLogLimiter(2, time.Minute)
	l.Now = func() time.Time {
		return currentTime
	}

	_, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}

	currentTime = time.Date(2026, 1, 1, 12, 0, 50, 0, time.UTC)

	_, err = l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}

	// Even though the wall clock crosses into 12:01, both requests are still
	// within the last 60 seconds. Sliding window should reject.
	currentTime = time.Date(2026, 1, 1, 12, 1, 5, 0, time.UTC)

	decision, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}

	if decision.Allowed {
		t.Fatal("expected request to be rejected because prior requests are still inside sliding window")
	}

	// Oldest request was 12:00:30, so it expires at 12:01:30.
	// Current time is 12:01:05, so RetryAfter should be 25s.
	if decision.RetryAfter != 25*time.Second {
		t.Fatalf("expected RetryAfter 25s, got %s", decision.RetryAfter)
	}
}

func TestSlidingWindowPanicsWhenConstructedWrong(t *testing.T) {
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

			limiter.NewSlidingWindowLogLimiter(tt.limit, tt.window)
		})
	}
}

func TestSlidingWindowReturnsContextError(t *testing.T) {
	l := limiter.NewSlidingWindowLogLimiter(2, time.Minute)

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

func TestSlidingWindowTracksKeysIndependently(t *testing.T) {
	currentTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	l := limiter.NewSlidingWindowLogLimiter(1, time.Minute)
	l.Now = func() time.Time {
		return currentTime
	}

	userOneFirst, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if !userOneFirst.Allowed {
		t.Fatal("expected user-1 first request to be allowed")
	}

	userOneSecond, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if userOneSecond.Allowed {
		t.Fatal("expected user-1 second request to be rejected")
	}

	userTwoFirst, err := l.Allow(context.Background(), "user-2")
	if err != nil {
		t.Fatal(err)
	}
	if !userTwoFirst.Allowed {
		t.Fatal("expected user-2 first request to be allowed")
	}
}

func TestSlidingWindowPrunesExpiredRequests(t *testing.T) {
	currentTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	l := limiter.NewSlidingWindowLogLimiter(3, time.Minute)
	l.Now = func() time.Time {
		return currentTime
	}

	_, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}

	currentTime = currentTime.Add(10 * time.Second)

	_, err = l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}

	currentTime = currentTime.Add(10 * time.Second)

	_, err = l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}

	if got := len(l.Requests["user-1"]); got != 3 {
		t.Fatalf("expected 3 stored timestamps, got %d", got)
	}

	// Move far enough that all previous requests are expired.
	currentTime = currentTime.Add(2 * time.Minute)

	decision, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}

	if !decision.Allowed {
		t.Fatal("expected request to be allowed after old requests expired")
	}

	if got := len(l.Requests["user-1"]); got != 1 {
		t.Fatalf("expected only 1 stored timestamp after pruning, got %d", got)
	}
}
