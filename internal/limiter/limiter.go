package limiter

import (
	"context"
	"time"
)

type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
	Remaining  int64
	Limit      int64
}

type RateLimiter interface {
	Allow(ctx context.Context, key string) (Decision, error)
}
