package api

import (
	"net/http"
	"strings"
)

// APIKeyMiddleware returns a middleware that enforces API key authentication.
// If apiKey is empty, all requests are allowed through (auth is disabled).
// Clients may supply the key in either the X-API-Key header or a standard
// Authorization: Bearer <key> header.
// The /health and /metrics endpoints are exempt from authentication.
func APIKeyMiddleware(apiKey string) func(http.Handler) http.Handler {
	bearerPrefix := "bearer "
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
			provided := r.Header.Get("X-API-Key")
			if provided == "" {
				auth := r.Header.Get("Authorization")
				if len(auth) > len(bearerPrefix) && strings.EqualFold(auth[:len(bearerPrefix)], bearerPrefix) {
					provided = strings.TrimSpace(auth[len(bearerPrefix):])
				}
			}
			if provided != apiKey {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
