package limiter

import (
	"context"
	"sync"
	"time"
)

type fixedWindowCounter struct {
	count   int64
	resetAt time.Time
}

type FixedWindowMemoryLimiter struct {
	mu       sync.Mutex
	limit    int64
	window   time.Duration
	counters map[string]fixedWindowCounter
	now      func() time.Time
}

func NewFixedWindowMemoryLimiter(limit int64, window time.Duration) *FixedWindowMemoryLimiter {
	if limit <= 0 {
		panic("limit must be greater than 0")
	}
	if window <= 0 {
		panic("window must be greater than 0")
	}
	return &FixedWindowMemoryLimiter{
		limit:    limit,
		window:   window,
		counters: make(map[string]fixedWindowCounter),
		now:      time.Now,
	}
}

func (limiter *FixedWindowMemoryLimiter) Allow(ctx context.Context, key string) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	now := limiter.now()

	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	counter, exists := limiter.counters[key]

	if !exists || now.After(counter.resetAt) {
		counter = fixedWindowCounter{
			count:   0,
			resetAt: now.Add(limiter.window),
		}
	}

	if counter.count >= limiter.limit {
		limiter.counters[key] = counter

		return Decision{
			Allowed:    false,
			RetryAfter: time.Until(counter.resetAt),
			Remaining:  0,
			Limit:      limiter.limit,
		}, nil
	}

	counter.count++
	limiter.counters[key] = counter

	return Decision{
		Allowed:    true,
		RetryAfter: 0,
		Remaining:  limiter.limit - counter.count,
		Limit:      limiter.limit,
	}, nil
}
