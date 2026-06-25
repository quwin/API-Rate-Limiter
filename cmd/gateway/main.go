package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"quwin/api-gateway/internal/gateway"
	"quwin/api-gateway/internal/limiter"
	"time"
)

func main() {
	listenAddr := getenv("GATEWAY_ADDR", ":8080")
	upstreamURL := getenv("UPSTREAM_URL", "http://localhost:9000")

	parsedUpstreamURL, err := url.Parse(upstreamURL)
	if err != nil {
		log.Fatalf("invalid upstream URL %q: %v", upstreamURL, err)
	}

	reverseProxy := httputil.NewSingleHostReverseProxy(parsedUpstreamURL)

	rateLimiter := limiter.NewFixedWindowMemoryLimiter(
		5,
		time.Minute,
	)

	handler := gateway.RateLimitMiddleware(rateLimiter, reverseProxy)

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("gateway listening on %s", listenAddr)
	log.Printf("proxying allowed requests to %s", upstreamURL)

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("gateway failed: %v", err)
	}
}

func getenv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	return value
}
