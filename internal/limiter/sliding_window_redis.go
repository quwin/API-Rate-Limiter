package limiter

import (
	"context"
	"errors"
	"math"
	"strconv"
	"time"
	"sync/atomic"
	"github.com/redis/go-redis/v9"
)

var errInvalidSlidingWindowRedisResult = errors.New("invalid sliding window redis script result")

type SlidingWindowRedisLimiter struct {
	client     *redis.Client
	limit      int64
	window     time.Duration
	Now        func() time.Time
	instanceID string
	sequence   atomic.Uint64
}

func NewSlidingWindowRedisLimiter(
	client *redis.Client,
	limit int64,
	window time.Duration,
	instanceID string,
) *SlidingWindowRedisLimiter {
	if client == nil {
		panic("redis client must not be nil")
	}
	if limit <= 0 {
		panic("limit must be greater than 0")
	}
	if window <= 0 {
		panic("window must be greater than 0")
	}
	if instanceID == "" {
		instanceID = "gateway"
	}
	return &SlidingWindowRedisLimiter{
		client:     client,
		limit:      limit,
		window:     window,
		Now:        time.Now,
		instanceID: instanceID,
	}
}

var slidingWindowRedisScript = redis.NewScript(`
	local key = KEYS[1]

	local limit = tonumber(ARGV[1])
	local window_seconds = tonumber(ARGV[2])
	local Now = tonumber(ARGV[3])
	local member = ARGV[4]
	local ttl_seconds = tonumber(ARGV[5])

	local window_start = Now - window_seconds

	redis.call("ZREMRANGEBYSCORE", key, "-inf", window_start)

	local current_count = redis.call("ZCARD", key)

	if current_count >= limit then
		local oldest = redis.call("ZRANGE", key, 0, 0, "WITHSCORES")
		local retry_after = 1

		if oldest[2] ~= nil then
			retry_after = math.ceil((tonumber(oldest[2]) + window_seconds) - Now)

			if retry_after < 1 then
				retry_after = 1
			end
		end

		redis.call("EXPIRE", key, ttl_seconds)

		return {
			0,
			0,
			retry_after,
			current_count
		}
	end

	redis.call("ZADD", key, Now, member)
	redis.call("EXPIRE", key, ttl_seconds)

	local remaining = limit - current_count - 1

	return {
		1,
		remaining,
		0,
		current_count + 1
	}
`)

func (l *SlidingWindowRedisLimiter) Allow(ctx context.Context, key string) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	Now := l.Now()
	NowSeconds := float64(Now.UnixNano()) / float64(time.Second)
	redisKey := "ratelimit:sliding_window:" + key
	// Adds an additional iterator in case two 
	member := strconv.FormatInt(Now.UnixNano(), 10) + ":" + strconv.FormatUint(l.sequence.Add(1), 10)
	windowSeconds := l.window.Seconds()

	ttlSeconds := max(60, int64(math.Ceil(windowSeconds))+60)

	result, err := slidingWindowRedisScript.Run(
		ctx,
		l.client,
		[]string{redisKey},
		strconv.FormatInt(l.limit, 10),
		strconv.FormatFloat(windowSeconds, 'f', -1, 64),
		strconv.FormatFloat(NowSeconds, 'f', -1, 64),
		member,
		strconv.FormatInt(ttlSeconds, 10),
	).Result()
	if err != nil {
		return Decision{}, err
	}

	values, ok := result.([]any)
	if !ok || len(values) != 4 {
		return Decision{}, errInvalidSlidingWindowRedisResult
	}

	allowedInt, ok := values[0].(int64)
	if !ok {
		return Decision{}, errInvalidSlidingWindowRedisResult
	}

	remaining, ok := values[1].(int64)
	if !ok {
		return Decision{}, errInvalidSlidingWindowRedisResult
	}

	retryAfterSeconds, ok := values[2].(int64)
	if !ok {
		return Decision{}, errInvalidSlidingWindowRedisResult
	}

	remaining = max(0, remaining)

	retryAfter := time.Duration(0)
	if allowedInt == 0 {
		retryAfter = max(time.Duration(retryAfterSeconds)*time.Second, time.Second)
	}

	return Decision{
		Allowed:    allowedInt == 1,
		RetryAfter: retryAfter,
		Remaining:  remaining,
		Limit:      l.limit,
	}, nil
}
