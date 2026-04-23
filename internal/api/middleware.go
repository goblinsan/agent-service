package api

import (
	"net/http"
)

// APIKeyMiddleware returns a middleware that enforces API key authentication.
// If apiKey is empty, all requests are allowed through (auth is disabled).
// Clients must supply the key in the X-API-Key request header.
// The /health and /metrics endpoints are exempt from authentication.
func APIKeyMiddleware(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if apiKey == "" {
				next.ServeHTTP(w, r)
				return
			}
			// Health and metrics endpoints are always accessible.
			if r.URL.Path == "/health" || r.URL.Path == "/metrics" {
				next.ServeHTTP(w, r)
				return
			}
			if r.Header.Get("X-API-Key") != apiKey {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
