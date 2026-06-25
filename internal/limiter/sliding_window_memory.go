package limiter

import (
	"context"
	"sync"
	"time"
)

type SlidingWindowLogLimiter struct {
	mu       sync.Mutex
	limit    int64
	window   time.Duration
	Requests map[string][]time.Time
	Now      func() time.Time
}

func NewSlidingWindowLogLimiter(limit int64, window time.Duration) *SlidingWindowLogLimiter {
	if limit <= 0 {
		panic("limit must be greater than 0")
	}

	if window <= 0 {
		panic("window must be greater than 0")
	}

	return &SlidingWindowLogLimiter{
		limit:    limit,
		window:   window,
		Requests: make(map[string][]time.Time),
		Now:      time.Now,
	}
}

func (l *SlidingWindowLogLimiter) Allow(ctx context.Context, key string) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}

	Now := l.Now()
	windowStart := Now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	timestamps := l.Requests[key]

	// Drop Requests that are outside the current sliding window.
	firstValidIndex := 0
	for firstValidIndex < len(timestamps) && !timestamps[firstValidIndex].After(windowStart) {
		firstValidIndex++
	}

	timestamps = timestamps[firstValidIndex:]

	if int64(len(timestamps)) >= l.limit {
		oldestRequest := timestamps[0]
		retryAfter := max(oldestRequest.Add(l.window).Sub(Now), 0)

		l.Requests[key] = timestamps

		return Decision{
			Allowed:    false,
			RetryAfter: retryAfter,
			Remaining:  0,
			Limit:      l.limit,
		}, nil
	}

	timestamps = append(timestamps, Now)
	l.Requests[key] = timestamps

	return Decision{
		Allowed:    true,
		RetryAfter: 0,
		Remaining:  l.limit - int64(len(timestamps)),
		Limit:      l.limit,
	}, nil
}
