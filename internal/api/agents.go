package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/goblinsan/agent-service/internal/service"
)

func listAgentsHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		agents := svc.ListAgents()

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"agents": agents}); err != nil {
			slog.Error("failed to encode agent catalog", "error", err)
		}
	}
}
