package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/goblinsan/agent-service/internal/model/registry"
	"github.com/goblinsan/agent-service/internal/service"
)

// routingNodeView is the JSON projection of a registry node returned by
// GET /admin/routing.
type routingNodeView struct {
	Name                  string   `json:"name"`
	URL                   string   `json:"url"`
	Healthy               bool     `json:"healthy"`
	Models                []string `json:"models,omitempty"`
	MaxConcurrentRequests int      `json:"max_concurrent_requests,omitempty"`
	ActiveRequests        int      `json:"active_requests"`
	MaxTokens             int      `json:"max_tokens,omitempty"`
	CtxSize               int      `json:"ctx_size,omitempty"`
}

type routingView struct {
	ChatNode        string            `json:"chat_node"`
	AutomationNode  string            `json:"automation_node"`
	ChatModel       string            `json:"chat_model,omitempty"`
	AutomationModel string            `json:"automation_model,omitempty"`
	Nodes           []routingNodeView `json:"nodes"`
}

func snapshotRouting(svc *service.Service, reg *registry.Registry) routingView {
	statuses := reg.Snapshot()
	nodes := make([]routingNodeView, len(statuses))
	for i, s := range statuses {
		nodes[i] = routingNodeView{
			Name:                  s.Config.Name,
			URL:                   s.Config.URL,
			Healthy:               s.Healthy,
			Models:                s.Config.Models,
			MaxConcurrentRequests: s.Config.MaxConcurrentRequests,
			ActiveRequests:        s.Config.ActiveRequests,
			MaxTokens:             s.Config.MaxTokens,
			CtxSize:               s.Config.CtxSize,
		}
	}
	return routingView{
		ChatNode:        svc.ChatNode(),
		AutomationNode:  svc.AutomationNode(),
		ChatModel:       svc.ChatModel(),
		AutomationModel: svc.AutomationModel(),
		Nodes:           nodes,
	}
}

func getRoutingHandler(svc *service.Service, reg *registry.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(snapshotRouting(svc, reg)); err != nil {
			slog.Error("failed to encode routing snapshot", "error", err)
		}
	}
}

func putRoutingHandler(svc *service.Service, reg *registry.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ChatNode       *string `json:"chat_node"`
			AutomationNode *string `json:"automation_node"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		if req.ChatNode != nil {
			name := *req.ChatNode
			if name != "" && !reg.HasNode(name) {
				http.Error(w, `{"error":"unknown chat_node"}`, http.StatusBadRequest)
				return
			}
			svc.SetChatNode(name)
		}
		if req.AutomationNode != nil {
			name := *req.AutomationNode
			if name != "" && !reg.HasNode(name) {
				http.Error(w, `{"error":"unknown automation_node"}`, http.StatusBadRequest)
				return
			}
			svc.SetAutomationNode(name)
		}
		slog.Info("routing updated",
			"chat_node", svc.ChatNode(),
			"automation_node", svc.AutomationNode(),
		)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(snapshotRouting(svc, reg)); err != nil {
			slog.Error("failed to encode routing snapshot", "error", err)
		}
	}
}
