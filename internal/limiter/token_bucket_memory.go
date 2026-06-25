package limiter

import (
	"context"
	"math"
	"sync"
	"time"
)

type tokenBucket struct {
	tokens       float64
	lastRefillAt time.Time
}

type TokenBucketMemoryLimiter struct {
	mu         sync.Mutex
	capacity   float64
	refillRate float64
	buckets    map[string]tokenBucket
	Now        func() time.Time
}

// NewTokenBucketMemoryLimiter creates an in-memory token bucket limiter.
//
// capacity: maximum burst size.
// refillRate: tokens added per second.
//
// Example:
//   capacity = 10
//   refillRate = 2
//
// This allows bursts of up to 10 requests and refills at 2 requests/sec.
func NewTokenBucketMemoryLimiter(capacity int64, refillRate float64) *TokenBucketMemoryLimiter {
	if capacity <= 0 {
		panic("capacity must be greater than 0")
	}

	if refillRate <= 0 {
		panic("refillRate must be greater than 0")
	}

	return &TokenBucketMemoryLimiter{
		capacity:   float64(capacity),
		refillRate: refillRate,
		buckets:    make(map[string]tokenBucket),
		Now:        time.Now,
	}
}

func (l *TokenBucketMemoryLimiter) Allow(ctx context.Context, key string) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}

	Now := l.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	bucket, exists := l.buckets[key]
	if !exists {
		bucket = tokenBucket{
			tokens:       l.capacity,
			lastRefillAt: Now,
		}
	}

	elapsed := Now.Sub(bucket.lastRefillAt).Seconds()
	tokensToAdd := elapsed * l.refillRate

	bucket.tokens = math.Min(l.capacity, bucket.tokens+tokensToAdd)
	bucket.lastRefillAt = Now

	if bucket.tokens < 1 {
		tokensNeeded := 1 - bucket.tokens
		secondsUntilNextToken := tokensNeeded / l.refillRate
		retryAfter := time.Duration(math.Ceil(secondsUntilNextToken)) * time.Second

		l.buckets[key] = bucket

		return Decision{
			Allowed:    false,
			RetryAfter: retryAfter,
			Remaining:  int64(math.Floor(bucket.tokens)),
			Limit:      int64(l.capacity),
		}, nil
	}

	bucket.tokens -= 1
	l.buckets[key] = bucket

	return Decision{
		Allowed:    true,
		RetryAfter: 0,
		Remaining:  int64(math.Floor(bucket.tokens)),
		Limit:      int64(l.capacity),
	}, nil
}