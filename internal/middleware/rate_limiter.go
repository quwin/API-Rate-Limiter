package middleware

import (
	"errors"
	"net/http"
	"strconv"

	"quwin/api-gateway/internal/auth"
	"quwin/api-gateway/internal/limiter"
)

type Authenticator interface {
	Authenticate(r *http.Request) (auth.Principal, error)
}

func RateLimitMiddleware(
	authenticator Authenticator,
	rateLimiter limiter.RateLimiter,
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, err := authenticator.Authenticate(r)
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrMissingAPIKey):
				http.Error(w, "missing API key", http.StatusUnauthorized)
			case errors.Is(err, auth.ErrInvalidAPIKey):
				http.Error(w, "invalid API key", http.StatusUnauthorized)
			default:
				http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
			}
			return
		}

		// Rate limit by stable identity, not by the secret itself.
		rateLimitKey := principal.ID

		decision, err := rateLimiter.Allow(r.Context(), rateLimitKey)
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

		// Forward safe identity info, not the raw API key.
		r.Header.Set("X-Authenticated-Principal-ID", principal.ID)
		r.Header.Set("X-Authenticated-Plan", principal.Plan)
		r.Header.Del("X-API-Key")

		next.ServeHTTP(w, r)
	})
}