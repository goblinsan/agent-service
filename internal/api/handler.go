package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/goblinsan/agent-service/internal/service"
	"github.com/goblinsan/agent-service/internal/sse"
)

func NewRouter(svc *service.Service) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.SetHeader("Content-Type", "application/json"))

	r.Post("/sessions", createSessionHandler(svc))
	r.Post("/sessions/{sessionID}/runs", createRunHandler(svc))
	r.Get("/sessions/{sessionID}/runs/{runID}/events", runEventsHandler(svc))

	r.Get("/approvals/{id}", getApprovalHandler(svc))
	r.Post("/approvals/{id}/approve", approveHandler(svc))
	r.Post("/approvals/{id}/deny", denyHandler(svc))

	return r
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

func createRunHandler(svc *service.Service) http.HandlerFunc {
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

		if err := svc.StartRun(r.Context(), sessionID, req.Prompt, w); err != nil {
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
