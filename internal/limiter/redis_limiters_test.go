package limiter_test

import (
	"context"
	"testing"
	"time"

	"quwin/api-gateway/internal/limiter"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestRedisClient(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{
		Addr: s.Addr(),
	})
	t.Cleanup(func() {
		_ = client.Close()
		s.Close()
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("ping miniredis: %v", err)
	}
	return s, client
}

func assertDecision(
	t *testing.T,
	got limiter.Decision,
	allowed bool,
	remaining int64,
	limit int64,
	retryAfter time.Duration,
) {
	t.Helper()

	if got.Allowed != allowed {
		t.Fatalf("Allowed: expected %v, got %v; limiter.Decision=%+v", allowed, got.Allowed, got)
	}
	if got.Remaining != remaining {
		t.Fatalf("Remaining: expected %d, got %d; limiter.Decision=%+v", remaining, got.Remaining, got)
	}
	if got.Limit != limit {
		t.Fatalf("Limit: expected %d, got %d; limiter.Decision=%+v", limit, got.Limit, got)
	}
	if got.RetryAfter != retryAfter {
		t.Fatalf("RetryAfter: expected %s, got %s; limiter.Decision=%+v", retryAfter, got.RetryAfter, got)
	}
}

func TestFixedWindowRedisAllowsUntilLimitThenRejects(t *testing.T) {
	_, client := newTestRedisClient(t)

	l := limiter.NewFixedWindowRedisLimiter(client, 2, time.Minute)

	first, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	assertDecision(t, first, true, 1, 2, 0)

	second, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	assertDecision(t, second, true, 0, 2, 0)

	third, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	assertDecision(t, third, false, 0, 2, time.Minute)
}

func TestFixedWindowRedisAllowsAfterWindowExpires(t *testing.T) {
	s, client := newTestRedisClient(t)

	l := limiter.NewFixedWindowRedisLimiter(client, 1, time.Minute)

	first, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	assertDecision(t, first, true, 0, 1, 0)

	rejected, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Allowed {
		t.Fatalf("expected second request to be rejected, got %+v", rejected)
	}

	s.FastForward(time.Minute)

	allowed, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	assertDecision(t, allowed, true, 0, 1, 0)
}

func TestFixedWindowRedisTracksKeysIndependently(t *testing.T) {
	_, client := newTestRedisClient(t)

	l := limiter.NewFixedWindowRedisLimiter(client, 1, time.Minute)

	userOneFirst, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	assertDecision(t, userOneFirst, true, 0, 1, 0)

	userOneSecond, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if userOneSecond.Allowed {
		t.Fatalf("expected user-1 second request to be rejected, got %+v", userOneSecond)
	}

	userTwoFirst, err := l.Allow(context.Background(), "user-2")
	if err != nil {
		t.Fatal(err)
	}
	assertDecision(t, userTwoFirst, true, 0, 1, 0)
}

func TestFixedWindowRedisReturnsContextError(t *testing.T) {
	_, client := newTestRedisClient(t)

	l := limiter.NewFixedWindowRedisLimiter(client, 2, time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	decision, err := l.Allow(ctx, "user-1")
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if decision != (limiter.Decision{}) {
		t.Fatalf("expected zero-value limiter.Decision, got %+v", decision)
	}
}

func TestFixedWindowRedisPanicsWhenConstructedWrong(t *testing.T) {
	_, client := newTestRedisClient(t)

	tests := []struct {
		name   string
		limit  int64
		window time.Duration
	}{
		{name: "zero limit", limit: 0, window: time.Minute},
		{name: "negative limit", limit: -1, window: time.Minute},
		{name: "zero window", limit: 1, window: 0},
		{name: "negative window", limit: 1, window: -time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatal("expected panic, got none")
				}
			}()

			limiter.NewFixedWindowRedisLimiter(client, tt.limit, tt.window)
		})
	}
}

func TestTokenBucketRedisAllowsBurstThenRejects(t *testing.T) {
	_, client := newTestRedisClient(t)

	currentTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	l := limiter.NewTokenBucketRedisLimiter(client, 2, 1)
	l.Now = func() time.Time {
		return currentTime
	}

	first, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	assertDecision(t, first, true, 1, 2, 0)

	second, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	assertDecision(t, second, true, 0, 2, 0)

	third, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	assertDecision(t, third, false, 0, 2, time.Second)
}

func TestTokenBucketRedisRefillsOverTime(t *testing.T) {
	_, client := newTestRedisClient(t)

	currentTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	l := limiter.NewTokenBucketRedisLimiter(client, 2, 1)
	l.Now = func() time.Time {
		return currentTime
	}

	_, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}

	_, err = l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}

	currentTime = currentTime.Add(time.Second)

	allowed, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	assertDecision(t, allowed, true, 0, 2, 0)
}

func TestTokenBucketRedisClampsRefillToCapacity(t *testing.T) {
	_, client := newTestRedisClient(t)

	currentTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	l := limiter.NewTokenBucketRedisLimiter(client, 2, 1)
	l.Now = func() time.Time {
		return currentTime
	}

	_, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}

	currentTime = currentTime.Add(10 * time.Second)

	decision, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}

	// Bucket should refill only back to capacity 2, then spend 1 token.
	assertDecision(t, decision, true, 1, 2, 0)
}

func TestTokenBucketRedisTracksKeysIndependently(t *testing.T) {
	_, client := newTestRedisClient(t)

	currentTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	l := limiter.NewTokenBucketRedisLimiter(client, 1, 1)
	l.Now = func() time.Time {
		return currentTime
	}

	userOneFirst, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	assertDecision(t, userOneFirst, true, 0, 1, 0)

	userOneSecond, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if userOneSecond.Allowed {
		t.Fatalf("expected user-1 second request to be rejected, got %+v", userOneSecond)
	}

	userTwoFirst, err := l.Allow(context.Background(), "user-2")
	if err != nil {
		t.Fatal(err)
	}
	assertDecision(t, userTwoFirst, true, 0, 1, 0)
}

func TestTokenBucketRedisReturnsContextError(t *testing.T) {
	_, client := newTestRedisClient(t)

	l := limiter.NewTokenBucketRedisLimiter(client, 2, 1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	decision, err := l.Allow(ctx, "user-1")
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if decision != (limiter.Decision{}) {
		t.Fatalf("expected zero-value limiter.Decision, got %+v", decision)
	}
}

func TestTokenBucketRedisPanicsWhenConstructedWrong(t *testing.T) {
	_, client := newTestRedisClient(t)

	tests := []struct {
		name       string
		client     *redis.Client
		capacity   int64
		refillRate float64
	}{
		{name: "nil client", client: nil, capacity: 1, refillRate: 1},
		{name: "zero capacity", client: client, capacity: 0, refillRate: 1},
		{name: "negative capacity", client: client, capacity: -1, refillRate: 1},
		{name: "zero refill rate", client: client, capacity: 1, refillRate: 0},
		{name: "negative refill rate", client: client, capacity: 1, refillRate: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatal("expected panic, got none")
				}
			}()

			limiter.NewTokenBucketRedisLimiter(tt.client, tt.capacity, tt.refillRate)
		})
	}
}

func TestSlidingWindowRedisAllowsRequestsUnderLimit(t *testing.T) {
	_, client := newTestRedisClient(t)

	currentTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	l := limiter.NewSlidingWindowRedisLimiter(client, 2, time.Minute, "test-instance")
	l.Now = func() time.Time {
		return currentTime
	}

	first, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	assertDecision(t, first, true, 1, 2, 0)

	second, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	assertDecision(t, second, true, 0, 2, 0)
}

func TestSlidingWindowRedisRejectsWhenLimitReached(t *testing.T) {
	_, client := newTestRedisClient(t)

	currentTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	l := limiter.NewSlidingWindowRedisLimiter(client, 2, time.Minute, "test-instance")
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

	// Oldest request was at 12:00:00. Current request is at 12:00:20.
	// Oldest request exits the 60-second sliding window at 12:01:00.
	assertDecision(t, decision, false, 0, 2, 40*time.Second)
}

func TestSlidingWindowRedisAllowsAfterOldestRequestExpires(t *testing.T) {
	_, client := newTestRedisClient(t)

	currentTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	l := limiter.NewSlidingWindowRedisLimiter(client, 2, time.Minute, "test-instance")
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
		t.Fatalf("expected request to be rejected before oldest request expires, got %+v", rejected)
	}

	currentTime = time.Date(2026, 1, 1, 12, 1, 0, 1, time.UTC)

	allowed, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}

	assertDecision(t, allowed, true, 0, 2, 0)
}

func TestSlidingWindowRedisDoesNotResetAtFixedBoundary(t *testing.T) {
	_, client := newTestRedisClient(t)

	currentTime := time.Date(2026, 1, 1, 12, 0, 30, 0, time.UTC)

	l := limiter.NewSlidingWindowRedisLimiter(client, 2, time.Minute, "test-instance")
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

	currentTime = time.Date(2026, 1, 1, 12, 1, 5, 0, time.UTC)

	d, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}

	// Both prior requests are still inside the last 60 seconds.
	// Oldest request expires at 12:01:30, so retry-after is 25s.
	assertDecision(t, d, false, 0, 2, 25*time.Second)
}

func TestSlidingWindowRedisTracksKeysIndependently(t *testing.T) {
	_, client := newTestRedisClient(t)

	currentTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	l := limiter.NewSlidingWindowRedisLimiter(client, 1, time.Minute, "test-instance")
	l.Now = func() time.Time {
		return currentTime
	}

	userOneFirst, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	assertDecision(t, userOneFirst, true, 0, 1, 0)

	userOneSecond, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if userOneSecond.Allowed {
		t.Fatalf("expected user-1 second request to be rejected, got %+v", userOneSecond)
	}

	userTwoFirst, err := l.Allow(context.Background(), "user-2")
	if err != nil {
		t.Fatal(err)
	}
	assertDecision(t, userTwoFirst, true, 0, 1, 0)
}

func TestSlidingWindowRedisStoresDistinctRequestsAtSameTimestamp(t *testing.T) {
	_, client := newTestRedisClient(t)

	currentTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	l := limiter.NewSlidingWindowRedisLimiter(client, 3, time.Minute, "test-instance")
	l.Now = func() time.Time {
		return currentTime
	}

	for i := 0; i < 3; i++ {
		d, err := l.Allow(context.Background(), "user-1")
		if err != nil {
			t.Fatal(err)
		}
		if !d.Allowed {
			t.Fatalf("request %d: expected allowed, got %+v", i+1, d)
		}
	}

	rejected, err := l.Allow(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Allowed {
		t.Fatalf("expected fourth request at same timestamp to be rejected, got %+v", rejected)
	}
}

func TestSlidingWindowRedisReturnsContextError(t *testing.T) {
	_, client := newTestRedisClient(t)

	l := limiter.NewSlidingWindowRedisLimiter(client, 2, time.Minute, "test-instance")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	d, err := l.Allow(ctx, "user-1")
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if d != (limiter.Decision{}) {
		t.Fatalf("expected zero-value d, got %+v", d)
	}
}

func TestSlidingWindowRedisPanicsWhenConstructedWrong(t *testing.T) {
	_, client := newTestRedisClient(t)

	tests := []struct {
		name   string
		client *redis.Client
		limit  int64
		window time.Duration
	}{
		{name: "nil client", client: nil, limit: 1, window: time.Minute},
		{name: "zero limit", client: client, limit: 0, window: time.Minute},
		{name: "negative limit", client: client, limit: -1, window: time.Minute},
		{name: "zero window", client: client, limit: 1, window: 0},
		{name: "negative window", client: client, limit: 1, window: -time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatal("expected panic, got none")
				}
			}()

			limiter.NewSlidingWindowRedisLimiter(tt.client, tt.limit, tt.window, "test-instance")
		})
	}
}
