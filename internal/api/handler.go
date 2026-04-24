package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/goblinsan/agent-service/internal/metrics"
	"github.com/goblinsan/agent-service/internal/service"
	"github.com/goblinsan/agent-service/internal/sse"
)

// RouterOptions configures optional features of the HTTP router.
type RouterOptions struct {
	// APIKey, when non-empty, enables X-API-Key authentication on all routes
	// except /health and /metrics.
	APIKey string

	// Metrics, when non-nil, exposes counters at GET /metrics and instruments
	// every request with the middleware.
	Metrics *metrics.Metrics
}

func NewRouter(svc *service.Service) http.Handler {
	return NewRouterWithOptions(svc, RouterOptions{})
}

// NewRouterWithOptions builds the HTTP router applying the supplied options.
func NewRouterWithOptions(svc *service.Service, opts RouterOptions) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.SetHeader("Content-Type", "application/json"))

	if opts.Metrics != nil {
		r.Use(opts.Metrics.Middleware)
	}
	if opts.APIKey != "" {
		r.Use(APIKeyMiddleware(opts.APIKey))
	}

	r.Get("/health", healthHandler())

	if opts.Metrics != nil {
		r.Get("/metrics", opts.Metrics.Handler().ServeHTTP)
	}

	r.Post("/sessions", createSessionHandler(svc))
	r.Post("/sessions/{sessionID}/runs", createRunHandler(svc, opts.Metrics))
	r.Get("/sessions/{sessionID}/runs/{runID}/events", runEventsHandler(svc))

	r.Get("/approvals/{id}", getApprovalHandler(svc))
	r.Post("/approvals/{id}/approve", approveHandler(svc))
	r.Post("/approvals/{id}/deny", denyHandler(svc))

	// Internal orchestration endpoints – designed for gateway-chat-platform and
	// automation callers, not for direct browser use.
	r.Post("/internal/chat", internalChatHandler(svc, opts.Metrics))
	r.Post("/internal/automation", internalAutomationHandler(svc, opts.Metrics))

	return r
}

func healthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}
}

func createSessionHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		if req.Name == "" {
			http.Error(w, `{"error":"name is required"}`, http.StatusBadRequest)
			return
		}
		sess, err := svc.CreateSession(r.Context(), req.Name, req.Description)
		if err != nil {
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(sess); err != nil {
			slog.Error("failed to encode session response", "error", err)
		}
	}
}

func createRunHandler(svc *service.Service, m *metrics.Metrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := chi.URLParam(r, "sessionID")
		var req struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		if req.Prompt == "" {
			http.Error(w, `{"error":"prompt is required"}`, http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		if m != nil {
			m.TotalRuns.Add(1)
		}
		if err := svc.StartRun(r.Context(), sessionID, req.Prompt, w); err != nil {
			if m != nil {
				m.FailedRuns.Add(1)
			}
			slog.Error("run failed", "error", err)
			_ = sse.Write(w, sse.Event{Type: "run.failed", Data: map[string]string{"error": "run failed"}})
			return
		}
	}
}

func runEventsHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		// Future: stream stored events for a run
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func getApprovalHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		approval, err := svc.GetApproval(id)
		if err != nil {
			http.Error(w, `{"error":"approval not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(approval); err != nil {
			slog.Error("failed to encode approval response", "error", err)
		}
	}
}

func approveHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := svc.ApproveApproval(id); err != nil {
			http.Error(w, `{"error":"approval not found or already decided"}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func denyHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req struct {
			Reason string `json:"reason"`
		}
		// Reason is optional; ignore decode errors.
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := svc.DenyApproval(id, req.Reason); err != nil {
			http.Error(w, `{"error":"approval not found or already decided"}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// internalChatHandler handles POST /internal/chat.
// It accepts a ChatRunRequest from the gateway-chat-platform and streams
// structured SSE run events back to the caller.
func internalChatHandler(svc *service.Service, m *metrics.Metrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req service.ChatRunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		if len(req.Messages) == 0 {
			http.Error(w, `{"error":"messages is required"}`, http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		if m != nil {
			m.TotalRuns.Add(1)
		}
		if err := svc.StartChatRun(r.Context(), &req, w); err != nil {
			if m != nil {
				m.FailedRuns.Add(1)
			}
			slog.Error("chat run failed", "error", err)
			_ = sse.Write(w, sse.Event{Type: sse.EventRunFailed, Data: map[string]string{"error": "run failed"}})
		}
	}
}

// internalAutomationHandler handles POST /internal/automation.
// It accepts an AutomationRunRequest and either streams SSE events
// (response_mode "stream") or returns a single JSON result (response_mode "sync"
// or when response_mode is omitted).
func internalAutomationHandler(svc *service.Service, m *metrics.Metrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req service.AutomationRunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		if req.Source == "" {
			http.Error(w, `{"error":"source is required"}`, http.StatusBadRequest)
			return
		}
		if req.JobType == "" {
			http.Error(w, `{"error":"job_type is required"}`, http.StatusBadRequest)
			return
		}
		if req.Prompt == "" {
			http.Error(w, `{"error":"prompt is required"}`, http.StatusBadRequest)
			return
		}

		if m != nil {
			m.TotalRuns.Add(1)
		}

		if req.ResponseMode == "stream" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.WriteHeader(http.StatusOK)
		} else {
			w.Header().Set("Content-Type", "application/json")
		}

		if err := svc.StartAutomationRun(r.Context(), &req, w); err != nil {
			if m != nil {
				m.FailedRuns.Add(1)
			}
			slog.Error("automation run failed", "error", err)
			if req.ResponseMode == "stream" {
				_ = sse.Write(w, sse.Event{Type: sse.EventRunFailed, Data: map[string]string{"error": "run failed"}})
			} else {
				http.Error(w, `{"error":"run failed"}`, http.StatusInternalServerError)
			}
		}
	}
}

