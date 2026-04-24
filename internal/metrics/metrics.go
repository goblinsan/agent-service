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

	// ToolCallsTotal counts every tool invocation executed during agent runs.
	ToolCallsTotal atomic.Int64 `json:"-"`
	// ApprovalRequestsTotal counts every human-approval gate reached during agent runs.
	ApprovalRequestsTotal atomic.Int64 `json:"-"`
	// BackendSelectionsTotal counts every time a model backend was explicitly selected.
	BackendSelectionsTotal atomic.Int64 `json:"-"`
	// RunsCompleted is the number of runs that finished (successfully or otherwise).
	// Together with RunLatencyTotalMs it enables average-latency computation.
	RunsCompleted     atomic.Int64 `json:"-"`
	RunLatencyTotalMs atomic.Int64 `json:"-"`
}

// Snapshot holds a point-in-time copy of all counters.
type Snapshot struct {
	TotalRequests          int64 `json:"total_requests"`
	TotalRuns              int64 `json:"total_runs"`
	FailedRuns             int64 `json:"failed_runs"`
	ActiveRequests         int64 `json:"active_requests"`
	ToolCallsTotal         int64 `json:"tool_calls_total"`
	ApprovalRequestsTotal  int64 `json:"approval_requests_total"`
	BackendSelectionsTotal int64 `json:"backend_selections_total"`
	RunsCompleted          int64 `json:"runs_completed"`
	RunLatencyTotalMs      int64 `json:"run_latency_total_ms"`
}

// Snapshot returns a point-in-time copy of all counters.
func (m *Metrics) Snapshot() Snapshot {
	return Snapshot{
		TotalRequests:          m.TotalRequests.Load(),
		TotalRuns:              m.TotalRuns.Load(),
		FailedRuns:             m.FailedRuns.Load(),
		ActiveRequests:         m.ActiveRequests.Load(),
		ToolCallsTotal:         m.ToolCallsTotal.Load(),
		ApprovalRequestsTotal:  m.ApprovalRequestsTotal.Load(),
		BackendSelectionsTotal: m.BackendSelectionsTotal.Load(),
		RunsCompleted:          m.RunsCompleted.Load(),
		RunLatencyTotalMs:      m.RunLatencyTotalMs.Load(),
	}
}

// RecordRunCompleted increments RunsCompleted and adds latencyMs to RunLatencyTotalMs.
func (m *Metrics) RecordRunCompleted(latencyMs int64) {
	m.RunsCompleted.Add(1)
	m.RunLatencyTotalMs.Add(latencyMs)
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
