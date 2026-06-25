package gateway

import (
	"net/http"
	"strconv"
	"quwin/api-gateway/internal/limiter"
)

func RateLimitMiddleware(limiter limiter.RateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-API-Key")
		if apiKey == "" {
			http.Error(w, "missing API key", http.StatusUnauthorized)
			return
		}
		decision, err := limiter.Allow(r.Context(), apiKey)
		if err != nil {
			http.Error(w, "rate limiter unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("X-RateLimit-Limit", strconv.FormatInt(decision.Limit, 10))
		w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(decision.Remaining, 10))
		if !decision.Allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(decision.RetryAfter.Seconds())))
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
