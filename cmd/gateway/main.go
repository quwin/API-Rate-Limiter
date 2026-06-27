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
	"quwin/api-gateway/internal/policy"
	"strconv"
	"time"
	"google.golang.org/api/idtoken"
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

	upstreamAudience := getenvString("UPSTREAM_AUDIENCE", upstreamURL)
	tokenSource, err := idtoken.NewTokenSource(context.Background(), upstreamAudience)
	if err != nil {
		log.Printf("failed to create upstream ID token source: %v", err)
		tokenSource = nil
	}
	reverseProxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(parsedUpstreamURL)
			r.Out.Host = parsedUpstreamURL.Host
			r.SetXForwarded()

			token, err := tokenSource.Token()
			if err == nil {
				token.SetAuthHeader(r.Out)
			}
			if err != nil {
				log.Printf("failed to get upstream ID token: %v", err)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf(
				"proxy error: method=%s path=%s upstream=%s err=%v",
				r.Method,
				r.URL.Path,
				parsedUpstreamURL.String(),
				err,
			)
			http.Error(w, "bad gateway", http.StatusBadGateway)
		},
	}

	apiKeyAuthenticator, err := auth.NewAPIKeyAuthenticatorFromHashes(getenvString("API_KEY_HASHES", ""))
	if err != nil {
		log.Fatalf("invalid API key configuration: %v", err)
	}

	redisClient := newRedisClientIfNeeded()
	rateLimiter := getRateLimiter(redisClient)

	defaultPolicy := limiter.Policy{
		Limit:      getenvInt64("RATE_LIMIT", 5),
		Window:     getenvDuration("RATE_LIMIT_WINDOW", time.Minute),
		Capacity:   getenvInt64("BUCKET_CAPACITY", 5),
		RefillRate: getenvFloat64("RATE_LIMIT", 5) / getenvDuration("RATE_LIMIT_WINDOW", time.Minute).Seconds(),
	}
	planPolicies, err := policy.ParsePlanPolicies(getenvString("RATE_LIMIT_POLICIES", ""))
	if err != nil {
		log.Fatalf("invalid RATE_LIMIT_POLICIES: %v", err)
	}
	policyStore := policy.NewStore(defaultPolicy, planPolicies)

	apiHandler := middleware.RateLimitMiddleware(
		apiKeyAuthenticator,
		policyStore,
		rateLimiter,
		reverseProxy,
	)
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
		return limiter.NewFixedWindowMemoryLimiter()
	case "token-bucket-memory":
		return limiter.NewTokenBucketMemoryLimiter()
	case "sliding-window-memory":
		return limiter.NewSlidingWindowLogLimiter()
	case "fixed-window-redis":
		return limiter.NewFixedWindowRedisLimiter(redisClient)
	case "token-bucket-redis":
		return limiter.NewTokenBucketRedisLimiter(redisClient)
	case "sliding-window-redis":
		return limiter.NewSlidingWindowRedisLimiter(
			redisClient,
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
	options := &redis.Options{
		Addr:     getenvString("REDIS_ADDR", "localhost:6379"),
		Username: getenvString("REDIS_USERNAME", "default"),
		Password: getenvString("REDIS_PASSWORD", ""),
	}
	client := redis.NewClient(options)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
