// Package metrics provides lightweight in-process counters that can be
// exposed over HTTP as a JSON document.
package metrics

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
)

// Metrics holds atomic counters for key service events.
type Metrics struct {
	TotalRequests  atomic.Int64 `json:"-"`
	TotalRuns      atomic.Int64 `json:"-"`
	FailedRuns     atomic.Int64 `json:"-"`
	ActiveRequests atomic.Int64 `json:"-"`
}

// Snapshot holds a point-in-time copy of all counters.
type Snapshot struct {
	TotalRequests  int64 `json:"total_requests"`
	TotalRuns      int64 `json:"total_runs"`
	FailedRuns     int64 `json:"failed_runs"`
	ActiveRequests int64 `json:"active_requests"`
}

// Snapshot returns a point-in-time copy of all counters.
func (m *Metrics) Snapshot() Snapshot {
	return Snapshot{
		TotalRequests:  m.TotalRequests.Load(),
		TotalRuns:      m.TotalRuns.Load(),
		FailedRuns:     m.FailedRuns.Load(),
		ActiveRequests: m.ActiveRequests.Load(),
	}
}

// Handler returns an http.Handler that writes the current metrics as JSON.
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(m.Snapshot())
	})
}

// Middleware returns an http.Handler middleware that increments TotalRequests
// and ActiveRequests for every request handled by next.
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.TotalRequests.Add(1)
		m.ActiveRequests.Add(1)
		defer m.ActiveRequests.Add(-1)
		next.ServeHTTP(w, r)
	})
}
