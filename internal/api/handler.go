package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/goblinsan/agent-service/internal/metrics"
	"github.com/goblinsan/agent-service/internal/model/registry"
	"github.com/goblinsan/agent-service/internal/service"
	"github.com/goblinsan/agent-service/internal/sse"
	"github.com/goblinsan/agent-service/internal/store"
	"github.com/goblinsan/agent-service/internal/tools"
)

// RouterOptions configures optional features of the HTTP router.
type RouterOptions struct {
	// APIKey, when non-empty, enables X-API-Key authentication on all routes
	// except /health and /metrics.
	APIKey string

	// Metrics, when non-nil, exposes counters at GET /metrics and instruments
	// every request with the middleware.
	Metrics *metrics.Metrics

	// Registry, when non-nil, enables the /admin/routing endpoints that
	// inspect and mutate which llm-service node handles chat and automation
	// traffic.
	Registry *registry.Registry

	// RoutingStore, when non-nil, is used by PUT /admin/routing to persist
	// routing changes so they survive restarts.  Optional; in-memory only when
	// nil.
	RoutingStore RoutingStore
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

	r.Get("/runs/{runID}", getRunHandler(svc))
	r.Get("/runs/{runID}/steps", listRunStepsHandler(svc))

	r.Get("/approvals/{id}", getApprovalHandler(svc))
	r.Post("/approvals/{id}/approve", approveHandler(svc))
	r.Post("/approvals/{id}/deny", denyHandler(svc))

	// Internal orchestration endpoints – designed for gateway-chat-platform and
	// automation callers, not for direct browser use.
	r.Get("/internal/agents", listAgentsHandler(svc))
	r.Post("/internal/chat", internalChatHandler(svc, opts.Metrics))
	r.Post("/internal/automation", internalAutomationHandler(svc, opts.Metrics))
	r.Post("/run", gatewayRunHandler(svc, opts.Metrics))
	r.Post("/internal/kulrs/palette", kulrsPaletteHandler(svc, opts.Metrics))

	// Per-user thread (session) browsing endpoints used by the chat web UI
	// and the iOS companion app to list, load, rename, and delete prior
	// conversations.  The user is identified by the X-User-ID header so a
	// single API key can serve multiple end users.
	r.Get("/internal/threads", listThreadsHandler(svc))
	r.Get("/internal/threads/{threadID}", getThreadHandler(svc))
	r.Patch("/internal/threads/{threadID}", renameThreadHandler(svc))
	r.Delete("/internal/threads/{threadID}", deleteThreadHandler(svc))

	// Notifications inbox endpoints.
	r.Get("/internal/notifications", listNotificationsHandler(svc))
	r.Post("/internal/notifications", createNotificationHandler(svc))
	r.Post("/internal/notifications/{id}/read", markNotificationReadHandler(svc))
	r.Post("/internal/notifications/read-all", markAllNotificationsReadHandler(svc))
	r.Delete("/internal/notifications/{id}", deleteNotificationHandler(svc))

	// Scheduler endpoints.
	r.Post("/internal/schedules", createScheduleHandler(svc))
	r.Get("/internal/schedules", listSchedulesHandler(svc))
	r.Get("/internal/schedules/history", listScheduleHistoryHandler(svc))
	r.Patch("/internal/schedules/{id}", patchScheduleHandler(svc))
	r.Post("/internal/schedules/{id}/pause", pauseScheduleHandler(svc))
	r.Post("/internal/schedules/{id}/resume", resumeScheduleHandler(svc))
	r.Delete("/internal/schedules/{id}", deleteScheduleHandler(svc))

	// Structured user profile endpoints. Backed by profile.* durable memories.
	r.Get("/internal/profile", getProfileHandler(svc))
	r.Put("/internal/profile", putProfileHandler(svc))

	// Plan CRUD endpoints.
	r.Get("/internal/plans", listPlansHandler(svc))
	r.Get("/internal/plans/workspace", planningWorkspaceHandler(svc))
	r.Get("/internal/plans/{id}", getPlanHandler(svc))
	r.Get("/internal/plans/{id}/export", exportPlanHandler(svc))
	r.Post("/internal/plans", upsertPlanHandler(svc))
	r.Post("/internal/plans/import", importPlanHandler(svc))
	r.Delete("/internal/plans/{id}", deletePlanHandler(svc))

	// Apple Health foreground summary ingest from GatewayApp.
	r.Post("/internal/apple-health/summary", appleHealthSummaryHandler(svc))
	r.Post("/internal/personal-data/batches", personalDataBatchHandler(svc))

	// Push token registration endpoints.
	r.Post("/internal/device-tokens", registerDeviceTokenHandler(svc))
	r.Delete("/internal/device-tokens/{token}", unregisterDeviceTokenHandler(svc))

	if opts.Registry != nil {
		r.Get("/admin/routing", getRoutingHandler(svc, opts.Registry))
		r.Put("/admin/routing", putRoutingHandler(svc, opts.Registry, opts.RoutingStore))
	}

	return r
}

func personalDataBatchHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		var req service.PersonalDataBatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		result, err := svc.IngestPersonalDataBatch(r.Context(), userID, req)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
			return
		}
		if err := json.NewEncoder(w).Encode(result); err != nil {
			slog.Error("failed to encode personal data batch response", "error", err)
		}
	}
}

func appleHealthSummaryHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		var req service.AppleHealthSummaryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		result, err := svc.IngestAppleHealthSummary(r.Context(), userID, req)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
			return
		}
		if err := json.NewEncoder(w).Encode(result); err != nil {
			slog.Error("failed to encode apple health summary response", "error", err)
		}
	}
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

// getRunHandler handles GET /runs/{runID}.
// It returns the full run record including tool calls and approval records so
// that operators can inspect the state of any completed or failed run.
func getRunHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runID := chi.URLParam(r, "runID")
		run, err := svc.GetRun(r.Context(), runID)
		if err != nil {
			http.Error(w, `{"error":"run not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(run); err != nil {
			slog.Error("failed to encode run response", "error", err)
		}
	}
}

// listRunStepsHandler handles GET /runs/{runID}/steps.
// It returns the ordered list of agent steps for the given run, enabling
// replay and post-hoc inspection without relying on streaming-time logs.
func listRunStepsHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runID := chi.URLParam(r, "runID")
		steps, err := svc.ListRunSteps(r.Context(), runID)
		if err != nil {
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		if steps == nil {
			steps = []*store.RunStep{}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(steps); err != nil {
			slog.Error("failed to encode steps response", "error", err)
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

// kulrsPaletteHandler handles POST /internal/kulrs/palette.
// It accepts a KulrsPaletteRequest from the Kulrs automation system and returns
// a single JSON AutomationRunResult once the palette analysis run completes.
func kulrsPaletteHandler(svc *service.Service, m *metrics.Metrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req service.KulrsPaletteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		if req.ProductID == "" {
			http.Error(w, `{"error":"product_id is required"}`, http.StatusBadRequest)
			return
		}
		if len(req.ImageURLs) == 0 {
			http.Error(w, `{"error":"image_urls is required"}`, http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")

		if m != nil {
			m.TotalRuns.Add(1)
		}
		if err := svc.StartKulrsPaletteRun(r.Context(), &req, w); err != nil {
			if m != nil {
				m.FailedRuns.Add(1)
			}
			slog.Error("kulrs palette run failed", "error", err)
			http.Error(w, `{"error":"run failed"}`, http.StatusInternalServerError)
		}
	}
}

// internalChatHandler handles POST /internal/chat.
// It accepts a ChatRunRequest from the gateway-chat-platform and either streams
// structured SSE run events or returns a sync JSON response when the caller asks
// for application/json.
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

		if m != nil {
			m.TotalRuns.Add(1)
		}
		if strings.Contains(r.Header.Get("Accept"), "application/json") {
			w.Header().Set("Content-Type", "application/json")
			if err := svc.StartChatRunSync(r.Context(), &req, w); err != nil {
				if m != nil {
					m.FailedRuns.Add(1)
				}
				slog.Error("chat run failed", "error", err)
				body, _ := json.Marshal(map[string]string{"error": err.Error()})
				http.Error(w, string(body), http.StatusInternalServerError)
			}
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		if err := svc.StartChatRun(r.Context(), &req, w); err != nil {
			if m != nil {
				m.FailedRuns.Add(1)
			}
			slog.Error("chat run failed", "error", err)
			_ = sse.Write(w, sse.Event{Type: sse.EventRunFailed, Data: map[string]string{"error": err.Error()}})
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
		if req.Prompt == "" && len(req.Messages) == 0 {
			http.Error(w, `{"error":"prompt or messages is required"}`, http.StatusBadRequest)
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

// gatewayRunHandler handles POST /run.
// It is a compatibility endpoint used by gateway-chat-platform's current
// internal agent-service client. The request and response shapes mirror the
// gateway's normalized sync contract rather than the richer internal SSE APIs.
func gatewayRunHandler(svc *service.Service, m *metrics.Metrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req service.GatewayRunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		if len(req.Messages) == 0 {
			http.Error(w, `{"error":"messages is required"}`, http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if m != nil {
			m.TotalRuns.Add(1)
		}
		if err := svc.StartGatewayRun(r.Context(), &req, w); err != nil {
			if m != nil {
				m.FailedRuns.Add(1)
			}
			slog.Error("gateway compatibility run failed", "error", err)
			http.Error(w, `{"error":"run failed"}`, http.StatusInternalServerError)
		}
	}
}

// listThreadsHandler returns a paginated summary of chat threads owned by the
// requesting user.  Used by chat clients (web, iOS) to render a thread list.
func listThreadsHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				limit = n
			}
		}
		threads, err := svc.ListThreadsForUser(r.Context(), userID, limit)
		if err != nil {
			slog.Error("list threads failed", "error", err, "user_id", userID)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		if threads == nil {
			threads = []store.ThreadSummary{}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"threads": threads}); err != nil {
			slog.Error("failed to encode threads response", "error", err)
		}
	}
}

// getThreadHandler returns the ordered message history for a thread, scoped to
// the requesting user.
func getThreadHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		threadID := chi.URLParam(r, "threadID")
		messages, err := svc.GetThreadForUser(r.Context(), userID, threadID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, `{"error":"thread not found"}`, http.StatusNotFound)
				return
			}
			slog.Error("get thread failed", "error", err, "user_id", userID, "thread_id", threadID)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		if messages == nil {
			messages = []store.ThreadMessage{}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"thread_id": threadID,
			"messages":  messages,
		}); err != nil {
			slog.Error("failed to encode thread response", "error", err)
		}
	}
}

// renameThreadHandler updates the human-readable title of a thread owned by
// the requesting user.
func renameThreadHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		threadID := chi.URLParam(r, "threadID")
		var req struct {
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Title) == "" {
			http.Error(w, `{"error":"title is required"}`, http.StatusBadRequest)
			return
		}
		if err := svc.RenameThreadForUser(r.Context(), userID, threadID, req.Title); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, `{"error":"thread not found"}`, http.StatusNotFound)
				return
			}
			slog.Error("rename thread failed", "error", err, "user_id", userID, "thread_id", threadID)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// deleteThreadHandler removes a thread owned by the requesting user.
func deleteThreadHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		threadID := chi.URLParam(r, "threadID")
		if err := svc.DeleteThreadForUser(r.Context(), userID, threadID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, `{"error":"thread not found"}`, http.StatusNotFound)
				return
			}
			slog.Error("delete thread failed", "error", err, "user_id", userID, "thread_id", threadID)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func listNotificationsHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				limit = n
			}
		}
		unreadOnly := true
		if raw := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("unread_only"))); raw != "" {
			unreadOnly = raw == "true" || raw == "1" || raw == "yes"
		}
		notifications, err := svc.ListNotifications(r.Context(), userID, unreadOnly, limit)
		if err != nil {
			slog.Error("list notifications failed", "error", err, "user_id", userID)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		if notifications == nil {
			notifications = []store.Notification{}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"notifications": notifications}); err != nil {
			slog.Error("failed to encode notifications response", "error", err)
		}
	}
}

func createNotificationHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		var req struct {
			Kind        string         `json:"kind"`
			Title       string         `json:"title"`
			Body        string         `json:"body"`
			ThreadID    string         `json:"thread_id"`
			SourceRunID string         `json:"source_run_id"`
			Payload     map[string]any `json:"payload"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Title) == "" {
			http.Error(w, `{"error":"title is required"}`, http.StatusBadRequest)
			return
		}
		n := &store.Notification{
			ID:          newID(),
			UserID:      userID,
			Kind:        firstNonEmpty(strings.TrimSpace(req.Kind), "generic"),
			Title:       strings.TrimSpace(req.Title),
			Body:        strings.TrimSpace(req.Body),
			ThreadID:    strings.TrimSpace(req.ThreadID),
			SourceRunID: strings.TrimSpace(req.SourceRunID),
			Payload:     req.Payload,
		}
		if err := svc.CreateNotification(r.Context(), n); err != nil {
			slog.Error("create notification failed", "error", err, "user_id", userID)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(n); err != nil {
			slog.Error("failed to encode notification response", "error", err)
		}
	}
}

func markNotificationReadHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		id := strings.TrimSpace(chi.URLParam(r, "id"))
		if id == "" {
			http.Error(w, `{"error":"notification id is required"}`, http.StatusBadRequest)
			return
		}
		if err := svc.MarkNotificationRead(r.Context(), userID, id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, `{"error":"notification not found"}`, http.StatusNotFound)
				return
			}
			slog.Error("mark notification read failed", "error", err, "user_id", userID, "notification_id", id)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func markAllNotificationsReadHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		if err := svc.MarkAllNotificationsRead(r.Context(), userID); err != nil {
			slog.Error("mark all notifications read failed", "error", err, "user_id", userID)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func deleteNotificationHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		id := strings.TrimSpace(chi.URLParam(r, "id"))
		if id == "" {
			http.Error(w, `{"error":"notification id is required"}`, http.StatusBadRequest)
			return
		}
		if err := svc.DeleteNotification(r.Context(), userID, id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, `{"error":"notification not found"}`, http.StatusNotFound)
				return
			}
			slog.Error("delete notification failed", "error", err, "user_id", userID, "notification_id", id)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func createScheduleHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		var req struct {
			Kind       string         `json:"kind"`
			Prompt     string         `json:"prompt"`
			ThreadID   string         `json:"thread_id"`
			AgentID    string         `json:"agent_id"`
			Payload    map[string]any `json:"payload"`
			RunAt      string         `json:"run_at"`
			Recurrence string         `json:"recurrence"`
			Timezone   string         `json:"timezone"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Prompt) == "" {
			http.Error(w, `{"error":"prompt is required"}`, http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.RunAt) == "" {
			http.Error(w, `{"error":"run_at is required"}`, http.StatusBadRequest)
			return
		}
		runAt, err := time.Parse(time.RFC3339, req.RunAt)
		if err != nil {
			http.Error(w, `{"error":"run_at must be RFC3339"}`, http.StatusBadRequest)
			return
		}
		job := &store.ScheduledJob{
			ID:         newID(),
			UserID:     userID,
			Kind:       firstNonEmpty(strings.TrimSpace(req.Kind), "prompt"),
			Prompt:     strings.TrimSpace(req.Prompt),
			ThreadID:   strings.TrimSpace(req.ThreadID),
			AgentID:    strings.TrimSpace(req.AgentID),
			Payload:    req.Payload,
			RunAt:      runAt.UTC(),
			Recurrence: strings.TrimSpace(req.Recurrence),
			Timezone:   strings.TrimSpace(req.Timezone),
			Status:     "pending",
		}
		if err := svc.CreateScheduledJob(r.Context(), job); err != nil {
			slog.Error("create schedule failed", "error", err, "user_id", userID)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(job); err != nil {
			slog.Error("failed to encode schedule response", "error", err)
		}
	}
}

func listSchedulesHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				limit = n
			}
		}
		jobs, err := svc.ListScheduledJobs(r.Context(), userID, limit)
		if err != nil {
			slog.Error("list schedules failed", "error", err, "user_id", userID)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		if jobs == nil {
			jobs = []store.ScheduledJob{}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"schedules": jobs}); err != nil {
			slog.Error("failed to encode schedules response", "error", err)
		}
	}
}

func listScheduleHistoryHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				limit = n
			}
		}
		jobs, err := svc.ListScheduledJobHistory(r.Context(), userID, limit)
		if err != nil {
			slog.Error("list schedule history failed", "error", err, "user_id", userID)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		if jobs == nil {
			jobs = []store.ScheduledJob{}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"schedules": jobs}); err != nil {
			slog.Error("failed to encode schedule history response", "error", err)
		}
	}
}

func getProfileHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		profile, err := svc.GetUserProfile(r.Context(), userID)
		if err != nil {
			slog.Error("get profile failed", "error", err, "user_id", userID)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"profile": profile}); err != nil {
			slog.Error("failed to encode profile response", "error", err)
		}
	}
}

func putProfileHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		var req struct {
			Profile store.UserProfile `json:"profile"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		profile, err := svc.UpsertUserProfile(r.Context(), userID, &req.Profile)
		if err != nil {
			slog.Error("put profile failed", "error", err, "user_id", userID)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"profile": profile}); err != nil {
			slog.Error("failed to encode profile response", "error", err)
		}
	}
}

func deleteScheduleHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		id := strings.TrimSpace(chi.URLParam(r, "id"))
		if id == "" {
			http.Error(w, `{"error":"schedule id is required"}`, http.StatusBadRequest)
			return
		}
		if err := svc.DeleteScheduledJob(r.Context(), userID, id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, `{"error":"schedule not found"}`, http.StatusNotFound)
				return
			}
			slog.Error("delete schedule failed", "error", err, "user_id", userID, "schedule_id", id)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func patchScheduleHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		id := strings.TrimSpace(chi.URLParam(r, "id"))
		if id == "" {
			http.Error(w, `{"error":"schedule id is required"}`, http.StatusBadRequest)
			return
		}
		var req struct {
			Kind       *string        `json:"kind"`
			Prompt     *string        `json:"prompt"`
			ThreadID   *string        `json:"thread_id"`
			AgentID    *string        `json:"agent_id"`
			Payload    map[string]any `json:"payload"`
			RunAt      *string        `json:"run_at"`
			Recurrence *string        `json:"recurrence"`
			Timezone   *string        `json:"timezone"`
			Status     *string        `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		patch := store.ScheduledJobPatch{
			Kind:       trimStringPtr(req.Kind),
			Prompt:     trimStringPtr(req.Prompt),
			ThreadID:   trimStringPtr(req.ThreadID),
			AgentID:    trimStringPtr(req.AgentID),
			Payload:    req.Payload,
			PayloadSet: req.Payload != nil,
			Recurrence: trimStringPtr(req.Recurrence),
			Timezone:   trimStringPtr(req.Timezone),
			Status:     trimStringPtr(req.Status),
			ClearLock:  req.Status != nil,
		}
		if patch.Prompt != nil && *patch.Prompt == "" {
			http.Error(w, `{"error":"prompt cannot be empty"}`, http.StatusBadRequest)
			return
		}
		if req.RunAt != nil {
			runAt, err := time.Parse(time.RFC3339, strings.TrimSpace(*req.RunAt))
			if err != nil {
				http.Error(w, `{"error":"run_at must be RFC3339"}`, http.StatusBadRequest)
				return
			}
			runAt = runAt.UTC()
			patch.RunAt = &runAt
		}
		if err := svc.PatchScheduledJob(r.Context(), userID, id, patch); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, `{"error":"schedule not found"}`, http.StatusNotFound)
				return
			}
			slog.Error("patch schedule failed", "error", err, "user_id", userID, "schedule_id", id)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func pauseScheduleHandler(svc *service.Service) http.HandlerFunc {
	return scheduleStatusHandler(svc, "paused")
}

func resumeScheduleHandler(svc *service.Service) http.HandlerFunc {
	return scheduleStatusHandler(svc, "pending")
}

func scheduleStatusHandler(svc *service.Service, status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		id := strings.TrimSpace(chi.URLParam(r, "id"))
		if id == "" {
			http.Error(w, `{"error":"schedule id is required"}`, http.StatusBadRequest)
			return
		}
		statusValue := status
		if err := svc.PatchScheduledJob(r.Context(), userID, id, store.ScheduledJobPatch{Status: &statusValue, ClearLock: true}); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, `{"error":"schedule not found"}`, http.StatusNotFound)
				return
			}
			slog.Error("schedule status update failed", "error", err, "user_id", userID, "schedule_id", id, "status", status)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func trimStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

func listPlansHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		plans, err := svc.ListActivePlans(r.Context(), userID)
		if err != nil {
			slog.Error("list plans failed", "error", err, "user_id", userID)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		if plans == nil {
			plans = []store.UserPlan{}
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"plans": plans}); err != nil {
			slog.Error("failed to encode plans response", "error", err)
		}
	}
}

type planningWorkspaceResponse struct {
	Workspace planningWorkspaceView `json:"workspace"`
}

type planningWorkspaceView struct {
	GeneratedAt time.Time                 `json:"generated_at"`
	Summary     planningWorkspaceSummary  `json:"summary"`
	Plans       []store.UserPlan          `json:"plans"`
	Timeline    planningWorkspaceTimeline `json:"timeline"`
}

type planningWorkspaceSummary struct {
	PlanCount              int     `json:"plan_count"`
	MilestoneCount         int     `json:"milestone_count"`
	CompletedMilestones    int     `json:"completed_milestones"`
	TaskCount              int     `json:"task_count"`
	CompletedTasks         int     `json:"completed_tasks"`
	PercentComplete        float64 `json:"percent_complete"`
	DatedItemCount         int     `json:"dated_item_count"`
	OverdueTaskCount       int     `json:"overdue_task_count"`
	UpcomingTaskCount      int     `json:"upcoming_task_count"`
	UpcomingMilestoneCount int     `json:"upcoming_milestone_count"`
}

type planningWorkspaceTimeline struct {
	AuthoritativeDateFields []string                `json:"authoritative_date_fields"`
	OrderingSemantics       string                  `json:"ordering_semantics"`
	HasHeuristicDates       bool                    `json:"has_heuristic_dates"`
	Items                   []planningWorkspaceItem `json:"items"`
}

type planningWorkspaceItem struct {
	Kind           string     `json:"kind"`
	PlanID         string     `json:"plan_id"`
	PlanTitle      string     `json:"plan_title"`
	MilestoneID    string     `json:"milestone_id,omitempty"`
	MilestoneTitle string     `json:"milestone_title,omitempty"`
	TaskID         string     `json:"task_id,omitempty"`
	Title          string     `json:"title"`
	Status         string     `json:"status"`
	Date           time.Time  `json:"date"`
	DateKind       string     `json:"date_kind"`
	DateConfidence string     `json:"date_confidence"`
	DependsOn      []string   `json:"depends_on,omitempty"`
	Sequence       int        `json:"sequence,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	IsCompleted    bool       `json:"is_completed"`
	IsOverdue      bool       `json:"is_overdue"`
	PlanOrder      int        `json:"plan_order"`
	MilestoneOrder int        `json:"milestone_order,omitempty"`
	TaskOrder      int        `json:"task_order,omitempty"`
}

func planningWorkspaceHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		plans, err := svc.ListActivePlans(r.Context(), userID)
		if err != nil {
			slog.Error("list planning workspace failed", "error", err, "user_id", userID)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		if plans == nil {
			plans = []store.UserPlan{}
		}
		if err := json.NewEncoder(w).Encode(planningWorkspaceResponse{
			Workspace: buildPlanningWorkspaceView(plans, time.Now().UTC()),
		}); err != nil {
			slog.Error("failed to encode planning workspace response", "error", err)
		}
	}
}

func buildPlanningWorkspaceView(plans []store.UserPlan, now time.Time) planningWorkspaceView {
	view := planningWorkspaceView{
		GeneratedAt: now,
		Plans:       plans,
		Timeline: planningWorkspaceTimeline{
			AuthoritativeDateFields: []string{
				"milestones[].scheduled_date",
				"milestones[].start_date",
				"milestones[].target_date",
				"milestones[].end_date",
				"milestones[].tasks[].scheduled_at",
				"milestones[].tasks[].start_at",
				"milestones[].tasks[].target_at",
				"milestones[].tasks[].due_at",
				"milestones[].tasks[].end_at",
				"milestones[].tasks[].completed_at",
			},
			OrderingSemantics: "items are sorted by authoritative date; ties preserve canonical plan, milestone, and task order from the durable plan model",
			HasHeuristicDates: false,
			Items:             []planningWorkspaceItem{},
		},
	}
	summary := planningWorkspaceSummary{PlanCount: len(plans)}
	for planOrder, plan := range plans {
		summary.MilestoneCount += plan.Progress.MilestoneCount
		summary.CompletedMilestones += plan.Progress.CompletedMilestones
		summary.TaskCount += plan.Progress.TaskCount
		summary.CompletedTasks += plan.Progress.CompletedTasks
		for milestoneOrder, milestone := range plan.Milestones {
			milestoneDone := planningMilestoneDone(milestone)
			appendMilestoneDate := func(date *time.Time, dateKind string, isDeadline bool) {
				if date == nil {
					return
				}
				isOverdue := isDeadline && !milestoneDone && date.Before(now)
				view.Timeline.Items = append(view.Timeline.Items, planningWorkspaceItem{
					Kind:           "milestone",
					PlanID:         plan.ID,
					PlanTitle:      plan.Title,
					MilestoneID:    milestone.ID,
					MilestoneTitle: milestone.Title,
					Title:          milestone.Title,
					Status:         milestone.Status,
					Date:           date.UTC(),
					DateKind:       dateKind,
					DateConfidence: "authoritative",
					DependsOn:      milestone.DependsOn,
					Sequence:       milestone.Sequence,
					IsCompleted:    milestoneDone,
					IsOverdue:      isOverdue,
					PlanOrder:      planOrder,
					MilestoneOrder: milestoneOrder,
				})
				summary.DatedItemCount++
				if isDeadline && !milestoneDone && !date.Before(now) {
					summary.UpcomingMilestoneCount++
				}
			}
			appendMilestoneDate(milestone.ScheduledDate, "scheduled_date", false)
			appendMilestoneDate(milestone.StartDate, "start_date", false)
			appendMilestoneDate(milestone.TargetDate, "target_date", true)
			appendMilestoneDate(milestone.EndDate, "end_date", true)
			for taskOrder, task := range milestone.Tasks {
				isCompleted := planningTaskDone(task)
				appendTaskDate := func(date *time.Time, dateKind string, isDeadline bool) {
					if date == nil {
						return
					}
					isOverdue := isDeadline && !isCompleted && date.Before(now)
					view.Timeline.Items = append(view.Timeline.Items, planningWorkspaceItem{
						Kind:           "task",
						PlanID:         plan.ID,
						PlanTitle:      plan.Title,
						MilestoneID:    milestone.ID,
						MilestoneTitle: milestone.Title,
						TaskID:         task.ID,
						Title:          task.Title,
						Status:         task.Status,
						Date:           date.UTC(),
						DateKind:       dateKind,
						DateConfidence: "authoritative",
						DependsOn:      task.DependsOn,
						Sequence:       task.Sequence,
						CompletedAt:    task.CompletedAt,
						IsCompleted:    isCompleted,
						IsOverdue:      isOverdue,
						PlanOrder:      planOrder,
						MilestoneOrder: milestoneOrder,
						TaskOrder:      taskOrder,
					})
					summary.DatedItemCount++
					if isDeadline {
						if isOverdue {
							summary.OverdueTaskCount++
						} else if !isCompleted {
							summary.UpcomingTaskCount++
						}
					}
				}
				appendTaskDate(task.ScheduledAt, "scheduled_at", false)
				appendTaskDate(task.StartAt, "start_at", false)
				appendTaskDate(task.TargetAt, "target_at", true)
				appendTaskDate(task.DueAt, "due_at", true)
				appendTaskDate(task.EndAt, "end_at", true)
			}
		}
	}
	switch {
	case summary.TaskCount > 0:
		summary.PercentComplete = float64(summary.CompletedTasks) / float64(summary.TaskCount) * 100
	case summary.MilestoneCount > 0:
		summary.PercentComplete = float64(summary.CompletedMilestones) / float64(summary.MilestoneCount) * 100
	}
	sort.SliceStable(view.Timeline.Items, func(i, j int) bool {
		left := view.Timeline.Items[i]
		right := view.Timeline.Items[j]
		switch {
		case left.Date.Before(right.Date):
			return true
		case right.Date.Before(left.Date):
			return false
		case left.PlanOrder != right.PlanOrder:
			return left.PlanOrder < right.PlanOrder
		case left.MilestoneOrder != right.MilestoneOrder:
			return left.MilestoneOrder < right.MilestoneOrder
		default:
			return left.TaskOrder < right.TaskOrder
		}
	})
	view.Summary = summary
	return view
}

func planningMilestoneDone(milestone store.UserPlanMilestone) bool {
	status := strings.ToLower(strings.TrimSpace(milestone.Status))
	if status == "done" || status == "complete" {
		return true
	}
	if len(milestone.Tasks) == 0 {
		return false
	}
	for _, task := range milestone.Tasks {
		if !planningTaskDone(task) {
			return false
		}
	}
	return true
}

func planningTaskDone(task store.UserPlanTask) bool {
	if task.CompletedAt != nil {
		return true
	}
	status := strings.ToLower(strings.TrimSpace(task.Status))
	return status == "done" || status == "complete"
}

func getPlanHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		planID := strings.TrimSpace(chi.URLParam(r, "id"))
		if planID == "" {
			http.Error(w, `{"error":"plan id is required"}`, http.StatusBadRequest)
			return
		}
		plan, err := svc.GetUserPlan(r.Context(), userID, planID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, `{"error":"plan not found"}`, http.StatusNotFound)
				return
			}
			slog.Error("get plan failed", "error", err, "user_id", userID, "plan_id", planID)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"plan": plan}); err != nil {
			slog.Error("failed to encode plan response", "error", err)
		}
	}
}

func upsertPlanHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		var req struct {
			ID                 string                        `json:"id"`
			Title              string                        `json:"title"`
			Status             string                        `json:"status"`
			Vision             string                        `json:"vision"`
			Target             string                        `json:"target"`
			Category           string                        `json:"category"`
			Objectives         []string                      `json:"objectives"`
			Principles         []string                      `json:"principles"`
			Tags               []string                      `json:"tags"`
			DataSources        []string                      `json:"data_sources"`
			Connectors         []store.PlanConnector         `json:"connectors"`
			ReviewCadence      string                        `json:"review_cadence"`
			Summary            string                        `json:"summary"`
			Metrics            map[string]any                `json:"metrics"`
			TrackedMetrics     []store.PlanTrackedMetric     `json:"tracked_metrics"`
			BaselineFacts      []store.PlanFact              `json:"baseline_facts"`
			SuccessCriteria    []string                      `json:"success_criteria"`
			Cadence            []store.PlanCadenceEntry      `json:"cadence"`
			SupportingSections []store.PlanSupportingSection `json:"supporting_sections"`
			Milestones         []store.UserPlanMilestone     `json:"milestones"`
			Steps              []map[string]any              `json:"steps"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Title) == "" {
			http.Error(w, `{"error":"title is required"}`, http.StatusBadRequest)
			return
		}
		resolvedID, created, err := tools.ResolvePlanIdentityForWrite(r.Context(), svc, userID, req.ID, req.Title)
		if err != nil {
			slog.Error("resolve plan identity failed", "error", err, "user_id", userID, "title", req.Title)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		req.ID = strings.TrimSpace(resolvedID)
		if req.ID == "" {
			req.ID = newID()
			created = true
		}
		plan := &store.UserPlan{
			ID:                 strings.TrimSpace(req.ID),
			UserID:             userID,
			Title:              strings.TrimSpace(req.Title),
			Status:             strings.TrimSpace(req.Status),
			Vision:             strings.TrimSpace(req.Vision),
			Target:             strings.TrimSpace(req.Target),
			Category:           strings.TrimSpace(req.Category),
			Objectives:         req.Objectives,
			Principles:         req.Principles,
			Tags:               req.Tags,
			DataSources:        req.DataSources,
			Connectors:         req.Connectors,
			ReviewCadence:      strings.TrimSpace(req.ReviewCadence),
			Summary:            strings.TrimSpace(req.Summary),
			Metrics:            req.Metrics,
			TrackedMetrics:     req.TrackedMetrics,
			BaselineFacts:      req.BaselineFacts,
			SuccessCriteria:    req.SuccessCriteria,
			Cadence:            req.Cadence,
			SupportingSections: req.SupportingSections,
			Milestones:         req.Milestones,
			Steps:              req.Steps,
		}
		if err := svc.UpsertUserPlan(r.Context(), plan); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, `{"error":"plan not found"}`, http.StatusNotFound)
				return
			}
			if errors.Is(err, store.ErrForbidden) {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			slog.Error("upsert plan failed", "error", err, "user_id", userID, "plan_id", plan.ID)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		statusCode := http.StatusOK
		if created {
			statusCode = http.StatusCreated
		}
		w.WriteHeader(statusCode)
		if err := json.NewEncoder(w).Encode(map[string]any{"created": created, "plan": plan}); err != nil {
			slog.Error("failed to encode plan upsert response", "error", err)
		}
	}
}

func importPlanHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		var req struct {
			ID            string                `json:"id"`
			Title         string                `json:"title"`
			Text          string                `json:"text"`
			Status        string                `json:"status"`
			Category      string                `json:"category"`
			Tags          []string              `json:"tags"`
			DataSources   []string              `json:"data_sources"`
			Connectors    []store.PlanConnector `json:"connectors"`
			ReviewCadence string                `json:"review_cadence"`
			Metrics       map[string]any        `json:"metrics"`
			Source        string                `json:"source"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		plan, created, source, err := tools.BuildUserPlanFromDocument(userID, map[string]any{
			"id":             req.ID,
			"title":          req.Title,
			"text":           req.Text,
			"status":         req.Status,
			"category":       req.Category,
			"tags":           req.Tags,
			"data_sources":   req.DataSources,
			"connectors":     req.Connectors,
			"review_cadence": req.ReviewCadence,
			"metrics":        req.Metrics,
			"source":         req.Source,
		})
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
			return
		}
		requestedID := strings.TrimSpace(req.ID)
		if requestedID == "" {
			requestedID = tools.StructuredPlanDocumentID(req.Text)
		}
		resolvedID, resolvedCreated, resolveErr := tools.ResolvePlanIdentityForWrite(r.Context(), svc, userID, requestedID, plan.Title)
		if resolveErr != nil {
			slog.Error("resolve imported plan identity failed", "error", resolveErr, "user_id", userID, "title", plan.Title)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		if strings.TrimSpace(resolvedID) != "" {
			plan.ID = strings.TrimSpace(resolvedID)
		}
		created = resolvedCreated
		if err := svc.UpsertUserPlan(r.Context(), plan); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, `{"error":"plan not found"}`, http.StatusNotFound)
				return
			}
			if errors.Is(err, store.ErrForbidden) {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			slog.Error("import plan failed", "error", err, "user_id", userID, "plan_id", plan.ID)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		statusCode := http.StatusOK
		if created {
			statusCode = http.StatusCreated
		}
		w.WriteHeader(statusCode)
		if err := json.NewEncoder(w).Encode(map[string]any{
			"created": created,
			"source":  source,
			"plan":    plan,
		}); err != nil {
			slog.Error("failed to encode plan import response", "error", err)
		}
	}
}

func exportPlanHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		planID := strings.TrimSpace(chi.URLParam(r, "id"))
		if planID == "" {
			http.Error(w, `{"error":"plan id is required"}`, http.StatusBadRequest)
			return
		}
		plan, err := svc.GetUserPlan(r.Context(), userID, planID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, `{"error":"plan not found"}`, http.StatusNotFound)
				return
			}
			if errors.Is(err, store.ErrForbidden) {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			slog.Error("get plan for export failed", "error", err, "user_id", userID, "plan_id", planID)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		format := r.URL.Query().Get("format")
		raw, contentType, err := tools.RenderUserPlanDocument(*plan, format)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
			return
		}
		filename := tools.PlanDocumentFilename(*plan, format)
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(raw); err != nil {
			slog.Error("failed to write exported plan document", "error", err, "user_id", userID, "plan_id", planID)
		}
	}
}

func deletePlanHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		planID := strings.TrimSpace(chi.URLParam(r, "id"))
		if planID == "" {
			http.Error(w, `{"error":"plan id is required"}`, http.StatusBadRequest)
			return
		}
		if err := svc.DeleteUserPlan(r.Context(), userID, planID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, `{"error":"plan not found"}`, http.StatusNotFound)
				return
			}
			slog.Error("delete plan failed", "error", err, "user_id", userID, "plan_id", planID)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func registerDeviceTokenHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		var req struct {
			Platform   string `json:"platform"`
			Token      string `json:"token"`
			AppVersion string `json:"app_version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Token) == "" {
			http.Error(w, `{"error":"token is required"}`, http.StatusBadRequest)
			return
		}
		d := &store.DeviceToken{
			ID:         newID(),
			UserID:     userID,
			Platform:   firstNonEmpty(strings.TrimSpace(req.Platform), "ios"),
			Token:      strings.TrimSpace(req.Token),
			AppVersion: strings.TrimSpace(req.AppVersion),
			LastSeenAt: time.Now().UTC(),
			CreatedAt:  time.Now().UTC(),
		}
		if err := svc.UpsertDeviceToken(r.Context(), d); err != nil {
			slog.Error("register device token failed", "error", err, "user_id", userID)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func unregisterDeviceTokenHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			http.Error(w, `{"error":"X-User-ID header is required"}`, http.StatusBadRequest)
			return
		}
		token := strings.TrimSpace(chi.URLParam(r, "token"))
		if token == "" {
			http.Error(w, `{"error":"token is required"}`, http.StatusBadRequest)
			return
		}
		if err := svc.DeleteDeviceToken(r.Context(), userID, token); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, `{"error":"device token not found"}`, http.StatusNotFound)
				return
			}
			slog.Error("delete device token failed", "error", err, "user_id", userID)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func newID() string {
	return strconv.FormatInt(time.Now().UTC().UnixNano(), 36)
}
