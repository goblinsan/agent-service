package metrics_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goblinsan/agent-service/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetrics_Counters(t *testing.T) {
	m := &metrics.Metrics{}
	m.TotalRequests.Add(5)
	m.TotalRuns.Add(3)
	m.FailedRuns.Add(1)
	m.ActiveRequests.Add(2)

	snap := m.Snapshot()
	assert.Equal(t, int64(5), snap.TotalRequests)
	assert.Equal(t, int64(3), snap.TotalRuns)
	assert.Equal(t, int64(1), snap.FailedRuns)
	assert.Equal(t, int64(2), snap.ActiveRequests)
}

func TestMetrics_Handler(t *testing.T) {
	m := &metrics.Metrics{}
	m.TotalRequests.Add(10)
	m.TotalRuns.Add(4)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var body map[string]int64
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, int64(10), body["total_requests"])
	assert.Equal(t, int64(4), body["total_runs"])
}

func TestMetrics_Middleware(t *testing.T) {
	m := &metrics.Metrics{}

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := m.Middleware(inner)

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
	}

	assert.Equal(t, int64(3), m.TotalRequests.Load())
	// ActiveRequests should be back to 0 after all requests complete.
	assert.Equal(t, int64(0), m.ActiveRequests.Load())
}
