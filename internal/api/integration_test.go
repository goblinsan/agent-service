package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goblinsan/agent-service/internal/api"
	"github.com/goblinsan/agent-service/internal/metrics"
	"github.com/goblinsan/agent-service/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jsonBody wraps a JSON string as an io.Reader for use with httptest.NewRequest.
func jsonBody(s string) *bytes.Buffer { return bytes.NewBufferString(s) }

// ---------------------------------------------------------------------------
// /health endpoint
// ---------------------------------------------------------------------------

func TestHealthEndpoint(t *testing.T) {
	router := api.NewRouter(service.New(newMockStore(), &mockProvider{}, 10))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var body map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, "ok", body["status"])
}

// ---------------------------------------------------------------------------
// /metrics endpoint
// ---------------------------------------------------------------------------

func TestMetricsEndpoint(t *testing.T) {
	svc := service.New(newMockStore(), &mockProvider{}, 10)
	m := &metrics.Metrics{}
	m.TotalRequests.Add(7)
	m.TotalRuns.Add(3)

	router := api.NewRouterWithOptions(svc, api.RouterOptions{Metrics: m})

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var body map[string]int64
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	// The middleware itself adds 1 to TotalRequests for the /metrics request,
	// so the reported value will be 8 (7 pre-seeded + 1 for this request).
	assert.Equal(t, int64(8), body["total_requests"])
	assert.Equal(t, int64(3), body["total_runs"])
}

func TestMetricsEndpoint_NotRegisteredWithoutOption(t *testing.T) {
	// NewRouter (without opts) does not register /metrics.
	router := api.NewRouter(service.New(newMockStore(), &mockProvider{}, 10))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// ---------------------------------------------------------------------------
// API key authentication middleware
// ---------------------------------------------------------------------------

func TestAPIKeyMiddleware_AllowsWithCorrectKey(t *testing.T) {
	svc := service.New(newMockStore(), &mockProvider{}, 10)
	router := api.NewRouterWithOptions(svc, api.RouterOptions{APIKey: "secret"})

	req := httptest.NewRequest(http.MethodPost, "/sessions", jsonBody(`{"name":"test","description":""}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "secret")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusCreated, rr.Code)
}

func TestAPIKeyMiddleware_RejectsWithWrongKey(t *testing.T) {
	svc := service.New(newMockStore(), &mockProvider{}, 10)
	router := api.NewRouterWithOptions(svc, api.RouterOptions{APIKey: "secret"})

	req := httptest.NewRequest(http.MethodPost, "/sessions", jsonBody(`{"name":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "wrong")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAPIKeyMiddleware_RejectsMissingKey(t *testing.T) {
	svc := service.New(newMockStore(), &mockProvider{}, 10)
	router := api.NewRouterWithOptions(svc, api.RouterOptions{APIKey: "secret"})

	req := httptest.NewRequest(http.MethodPost, "/sessions", jsonBody(`{"name":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAPIKeyMiddleware_AllowsHealthWithoutKey(t *testing.T) {
	svc := service.New(newMockStore(), &mockProvider{}, 10)
	router := api.NewRouterWithOptions(svc, api.RouterOptions{APIKey: "secret"})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	// /health is always accessible even when auth is enabled.
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestAPIKeyMiddleware_DisabledWhenKeyEmpty(t *testing.T) {
	svc := service.New(newMockStore(), &mockProvider{}, 10)
	router := api.NewRouterWithOptions(svc, api.RouterOptions{APIKey: ""})

	req := httptest.NewRequest(http.MethodPost, "/sessions", jsonBody(`{"name":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	// No X-API-Key header – auth is disabled so this must succeed.
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusCreated, rr.Code)
}

func TestMetricsEndpoint_AllowedWithoutKeyWhenAuthEnabled(t *testing.T) {
	svc := service.New(newMockStore(), &mockProvider{}, 10)
	m := &metrics.Metrics{}
	router := api.NewRouterWithOptions(svc, api.RouterOptions{APIKey: "secret", Metrics: m})

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	// No API key – /metrics should still be reachable.
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}
