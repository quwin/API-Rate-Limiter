package limiter

import (
	"context"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var errInvalidTokenBucketRedisResult = errors.New("invalid token bucket redis script result")

type TokenBucketRedisLimiter struct {
	client     *redis.Client
	capacity   float64
	refillRate float64
	Now        func() time.Time
}

// NewTokenBucketRedisLimiter creates a Redis-backed token bucket limiter.
//
// capacity: maximum burst size.
// refillRate: tokens added per second.
//
// Example:
//   capacity = 10
//   refillRate = 2
//
// This allows bursts up to 10 requests and refills at 2 requests/sec.
func NewTokenBucketRedisLimiter(
	client *redis.Client,
	capacity int64,
	refillRate float64,
) *TokenBucketRedisLimiter {
	if client == nil {
		panic("redis client must not be nil")
	}

	if capacity <= 0 {
		panic("capacity must be greater than 0")
	}

	if refillRate <= 0 {
		panic("refillRate must be greater than 0")
	}

	return &TokenBucketRedisLimiter{
		client:     client,
		capacity:   float64(capacity),
		refillRate: refillRate,
		Now:        time.Now,
	}
}

var tokenBucketRedisScript = redis.NewScript(`
	local tokens_key = KEYS[1]
	local timestamp_key = KEYS[2]

	local capacity = tonumber(ARGV[1])
	local refill_rate = tonumber(ARGV[2])
	local Now = tonumber(ARGV[3])
	local requested = tonumber(ARGV[4])
	local ttl_seconds = tonumber(ARGV[5])

	local current_tokens = tonumber(redis.call("GET", tokens_key))
	if current_tokens == nil then
		current_tokens = capacity
	end

	local last_refill = tonumber(redis.call("GET", timestamp_key))
	if last_refill == nil then
		last_refill = Now
	end

	local elapsed = Now - last_refill
	if elapsed < 0 then
		elapsed = 0
	end

	local tokens_to_add = elapsed * refill_rate
	current_tokens = math.min(capacity, current_tokens + tokens_to_add)
	last_refill = Now

	local allowed = 0
	local retry_after = 0

	if current_tokens >= requested then
		allowed = 1
		current_tokens = current_tokens - requested
	else
		local tokens_needed = requested - current_tokens
		retry_after = math.ceil(tokens_needed / refill_rate)
	end

	redis.call("SET", tokens_key, current_tokens, "EX", ttl_seconds)
	redis.call("SET", timestamp_key, last_refill, "EX", ttl_seconds)

	return {
		allowed,
		current_tokens,
		retry_after
	}
`)

func (l *TokenBucketRedisLimiter) Allow(ctx context.Context, key string) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}

	Now := l.Now().UnixNano()
	NowSeconds := float64(Now) / float64(time.Second)

	redisTokensKey := "ratelimit:token_bucket:" + key + ":tokens"
	redisTimestampKey := "ratelimit:token_bucket:" + key + ":timestamp"

	// Keep idle users in Redis for roughly enough time to refill from empty to full,
	// plus a small buffer. This prevents unbounded key growth.
	ttlSeconds := max(int64(math.Ceil(l.capacity/l.refillRate)) + 60, 60)

	result, err := tokenBucketRedisScript.Run(
		ctx,
		l.client,
		[]string{redisTokensKey, redisTimestampKey},
		strconv.FormatFloat(l.capacity, 'f', -1, 64),
		strconv.FormatFloat(l.refillRate, 'f', -1, 64),
		strconv.FormatFloat(NowSeconds, 'f', -1, 64),
		"1",
		strconv.FormatInt(ttlSeconds, 10),
	).Result()
	if err != nil {
		return Decision{}, err
	}

	values, ok := result.([]any)
	if !ok || len(values) != 3 {
		return Decision{}, errInvalidTokenBucketRedisResult
	}

	allowedInt, ok := values[0].(int64)
	if !ok {
		return Decision{}, errInvalidTokenBucketRedisResult
	}

	remainingFloat, err := redisNumberToFloat64(values[1])
	if err != nil {
		return Decision{}, err
	}

	retryAfterSeconds, ok := values[2].(int64)
	if !ok {
		return Decision{}, errInvalidTokenBucketRedisResult
	}

	remaining := max(int64(math.Floor(remainingFloat)), 0)

	retryAfter := time.Duration(0)
	if allowedInt == 0 {
		retryAfter = max(time.Duration(retryAfterSeconds) * time.Second, time.Second)
	}

	return Decision{
		Allowed:    allowedInt == 1,
		RetryAfter: retryAfter,
		Remaining:  remaining,
		Limit:      int64(l.capacity),
	}, nil
}

func redisNumberToFloat64(value any) (float64, error) {
	switch typed := value.(type) {
	case int64:
		return float64(typed), nil
	case string:
		return strconv.ParseFloat(typed, 64)
	case []byte:
		return strconv.ParseFloat(string(typed), 64)
	default:
		return 0, errInvalidTokenBucketRedisResult
	}
}