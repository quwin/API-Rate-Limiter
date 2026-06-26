package main

import (
	"context"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"quwin/api-gateway/internal/auth"
	"quwin/api-gateway/internal/gateway"
	"quwin/api-gateway/internal/limiter"
	"quwin/api-gateway/internal/middleware"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

func main() {
	listenAddr := getenvString("GATEWAY_ADDR", ":8080")
	upstreamURL := getenvString("UPSTREAM_URL", "http://localhost:9000")

	parsedUpstreamURL, err := url.Parse(upstreamURL)
	if err != nil {
		log.Fatalf("invalid upstream URL %q: %v", upstreamURL, err)
	}

	reverseProxy := httputil.NewSingleHostReverseProxy(parsedUpstreamURL)

	apiKeyAuthenticator, err := auth.NewAPIKeyAuthenticatorFromHashes(getenvString("API_KEY_HASHES", ""))
	if err != nil {
		log.Fatalf("invalid API key configuration: %v", err)
	}

	redisClient := newRedisClientIfNeeded()
	rateLimiter := getRateLimiter(redisClient)

	apiHandler := middleware.RateLimitMiddleware(apiKeyAuthenticator, rateLimiter, reverseProxy)
	apiHandler = middleware.MetricsMiddleware(apiHandler)

	mux := http.NewServeMux()
	mux.Handle("/healthz", gateway.HealthzHandler())
	mux.Handle("/readyz", gateway.ReadyzHandler(redisClient))
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/", apiHandler)

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("gateway listening on %s", listenAddr)
	log.Printf("proxying allowed requests to %s", upstreamURL)

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("gateway failed: %v", err)
	}
}
func getRateLimiter(redisClient *redis.Client) limiter.RateLimiter {
	limiterType := getenvString("RATE_LIMITER", "fixed-window-memory")
	switch limiterType {
	case "fixed-window-memory":
		return limiter.NewFixedWindowMemoryLimiter(
			getenvInt64("RATE_LIMIT", 5),
			getenvDuration("RATE_LIMIT_WINDOW", time.Minute),
		)
	case "token-bucket-memory":
		return limiter.NewTokenBucketMemoryLimiter(
			getenvInt64("BUCKET_CAPACITY", 5),
			getenvFloat64("RATE_LIMIT", 5)/60.0,
		)
	case "sliding-window-memory":
		return limiter.NewSlidingWindowLogLimiter(
			getenvInt64("RATE_LIMIT", 5),
			getenvDuration("RATE_LIMIT_WINDOW", time.Minute),
		)
	case "fixed-window-redis":
		return limiter.NewFixedWindowRedisLimiter(
			redisClient,
			getenvInt64("RATE_LIMIT", 5),
			getenvDuration("RATE_LIMIT_WINDOW", time.Minute),
		)
	case "token-bucket-redis":
		return limiter.NewTokenBucketRedisLimiter(
			redisClient,
			getenvInt64("BUCKET_CAPACITY", 5),
			getenvFloat64("RATE_LIMIT", 5)/60.0,
		)
	case "sliding-window-redis":
		return limiter.NewSlidingWindowRedisLimiter(
			redisClient,
			getenvInt64("RATE_LIMIT", 5),
			getenvDuration("RATE_LIMIT_WINDOW", time.Minute),
			getenvString("GATEWAY_INSTANCE_ID", uuid.NewString()),
		)
	default:
		log.Fatalf("unknown RATE_LIMITER %q", limiterType)
		return nil
	}
}
func newRedisClientIfNeeded() *redis.Client {
	limiterType := getenvString("RATE_LIMITER", "fixed-window-memory")

	switch limiterType {
	case "fixed-window-redis", "token-bucket-redis", "sliding-window-redis":
		return newRedisClient()
	default:
		return nil
	}
}
func newRedisClient() *redis.Client {
	client := redis.NewClient(&redis.Options{
		Addr: getenvString("REDIS_ADDR", "localhost:6379"),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		log.Fatalf("failed to connect to Redis: %v", err)
	}
	return client
}

func getenvAs[T any](key string, fallback T, parse func(string) (T, error)) T {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := parse(value)
	if err != nil {
		log.Fatalf("invalid %s value %q: %v", key, value, err)
	}
	return parsed
}
func getenvString(key string, fallback string) string {
	return getenvAs(key, fallback, func(value string) (string, error) {
		return value, nil
	})
}
func getenvInt64(key string, fallback int64) int64 {
	return getenvAs(key, fallback, func(value string) (int64, error) {
		return strconv.ParseInt(value, 10, 64)
	})
}
func getenvFloat64(key string, fallback float64) float64 {
	return getenvAs(key, fallback, func(value string) (float64, error) {
		return strconv.ParseFloat(value, 64)
	})
}
func getenvDuration(key string, fallback time.Duration) time.Duration {
	return getenvAs(key, fallback, time.ParseDuration)
}
