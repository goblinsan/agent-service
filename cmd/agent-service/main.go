package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"github.com/goblinsan/agent-service/internal/api"
	"github.com/goblinsan/agent-service/internal/config"
	"github.com/goblinsan/agent-service/internal/metrics"
	"github.com/goblinsan/agent-service/internal/model"
	"github.com/goblinsan/agent-service/internal/model/llama"
	"github.com/goblinsan/agent-service/internal/model/registry"
	"github.com/goblinsan/agent-service/internal/push"
	agentrunner "github.com/goblinsan/agent-service/internal/runner"
	"github.com/goblinsan/agent-service/internal/scheduler"
	"github.com/goblinsan/agent-service/internal/service"
	"github.com/goblinsan/agent-service/internal/store"
	"github.com/goblinsan/agent-service/internal/tools"
)

func main() {
	cfg := config.Load()

	level := slog.LevelInfo
	if cfg.LogLevel == "debug" {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	db, err := connectWithRetry(cfg.DatabaseURL, 5, 2*time.Second)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	pg := store.NewPostgres(db)

	var provider model.Provider
	var llmRegistry *registry.Registry
	switch {
	case len(cfg.LLMNodes) > 0:
		// Multi-node pool: build a registry from the node URLs and wrap it in a Pool.
		nodes := make([]registry.NodeConfig, len(cfg.LLMNodes))
		for i, n := range cfg.LLMNodes {
			nodes[i] = registry.NodeConfig{Name: n.Name, URL: n.URL}
		}
		reg := registry.New(nodes)
		if err := refreshNodeRegistry(context.Background(), reg); err != nil {
			slog.Warn("initial llm-service node discovery completed with errors", "error", err)
		}
		startNodeRegistryRefreshLoop(reg)
		provider = registry.NewPool(reg, func(url string) model.Provider {
			return llama.New(url)
		})
		llmRegistry = reg
		slog.Info("llm-service pool configured", "nodes", len(nodes))
	case cfg.LlamaURL != "":
		provider = llama.New(cfg.LlamaURL)
	default:
		provider = &noopProvider{}
	}

	m := &metrics.Metrics{}

	// Build the native tool registry. Memory tools take pg so they can read and
	// write per-user durable facts; the user identity comes from run context, not
	// from model-supplied parameters.
	reg := tools.NewRegistry()
	localTZ := os.Getenv("OPERATOR_TIMEZONE")
	if localTZ == "" {
		localTZ = "America/New_York"
	}
	if err := reg.Register(&tools.TimeNowTool{Location: localTZ}); err != nil {
		slog.Warn("register time_now", "error", err)
	}
	if err := reg.Register(&tools.MemoryRecallTool{Store: pg}); err != nil {
		slog.Warn("register memory_recall", "error", err)
	}
	if err := reg.Register(&tools.MemoryWriteTool{Store: pg}); err != nil {
		slog.Warn("register memory_write", "error", err)
	}
	if err := reg.Register(&tools.PlanListTool{Store: pg}); err != nil {
		slog.Warn("register plan_list", "error", err)
	}
	if err := reg.Register(&tools.PlanUpsertTool{Store: pg}); err != nil {
		slog.Warn("register plan_upsert", "error", err)
	}
	if err := reg.Register(&tools.ScheduleCreateTool{Store: pg}); err != nil {
		slog.Warn("register create_schedule", "error", err)
	}
	if braveKey := os.Getenv("BRAVE_SEARCH_API_KEY"); braveKey != "" {
		if err := reg.Register(&tools.WebSearchTool{APIKey: braveKey}); err != nil {
			slog.Warn("register web_search", "error", err)
		} else {
			slog.Info("web_search tool enabled (brave search)")
		}
	} else {
		slog.Info("web_search tool disabled: BRAVE_SEARCH_API_KEY not set")
	}

	nativeRunner := agentrunner.NewNativeRunner(reg)
	toolDefs := reg.List()
	toolSpecs := make([]model.ToolSpec, len(toolDefs))
	for i, def := range toolDefs {
		toolSpecs[i] = model.ToolSpec{
			Name:        def.Name,
			Description: def.Description,
			Parameters:  tools.JSONSchema(def),
		}
	}
	slog.Info("native tool registry built", "tools", len(toolSpecs))

	chatModel := os.Getenv("CHAT_MODEL")
	automationModel := os.Getenv("AUTOMATION_MODEL")
	var notifier service.NotificationDispatcher
	apnsDispatcher, err := push.NewAPNSDispatcher(pg, push.APNSConfig{
		Enabled: cfg.APNSEnabled,
		TeamID:  cfg.APNSTeamID,
		KeyID:   cfg.APNSKeyID,
		AuthKey: cfg.APNSAuthKey,
		Topic:   cfg.APNSTopic,
		Env:     cfg.APNSEnv,
	})
	if err != nil {
		slog.Warn("APNs dispatcher disabled", "error", err)
	} else if apnsDispatcher != nil {
		notifier = apnsDispatcher
		slog.Info("APNs dispatcher enabled", "env", cfg.APNSEnv, "topic", cfg.APNSTopic)
	} else {
		slog.Info("APNs dispatcher disabled")
	}
	if cfg.ChatNode != "" {
		slog.Info("chat node configured", "node", cfg.ChatNode)
	}
	if cfg.AutomationNode != "" {
		slog.Info("automation node configured", "node", cfg.AutomationNode)
	}
	if chatModel != "" {
		slog.Info("chat model override configured (deprecated; prefer CHAT_NODE)", "model", chatModel)
	}
	if automationModel != "" {
		slog.Info("automation model default configured (deprecated; prefer AUTOMATION_NODE)", "model", automationModel)
	}

	var svc *service.Service
	if cfg.MCPEndpoint != "" {
		// MCP runner takes precedence when configured; native tools are still
		// advertised so the LLM knows about local helpers like time_now even
		// when MCP is supplying remote tools.
		svc = service.NewWithOptions(pg, provider, cfg.AgentMaxSteps, service.ServiceOptions{
			Runner:                 agentrunner.NewMCPRunner(cfg.MCPEndpoint, nil),
			Metrics:                m,
			ToolSpecs:              toolSpecs,
			ChatNode:               cfg.ChatNode,
			AutomationNode:         cfg.AutomationNode,
			ChatModel:              chatModel,
			AutomationModel:        automationModel,
			NotificationDispatcher: notifier,
		})
		slog.Info("MCP runner configured", "endpoint", cfg.MCPEndpoint)
	} else {
		svc = service.NewWithOptions(pg, provider, cfg.AgentMaxSteps, service.ServiceOptions{
			Runner:                 nativeRunner,
			Metrics:                m,
			ToolSpecs:              toolSpecs,
			ChatNode:               cfg.ChatNode,
			AutomationNode:         cfg.AutomationNode,
			ChatModel:              chatModel,
			AutomationModel:        automationModel,
			NotificationDispatcher: notifier,
		})
	}

	// Persisted routing overrides env defaults; env defaults are upserted on
	// first boot so the DB is populated with the static config.
	if llmRegistry != nil {
		rc, err := pg.GetRouting(context.Background())
		switch {
		case err == nil:
			svc.SetChatNode(rc.ChatNode)
			svc.SetAutomationNode(rc.AutomationNode)
			slog.Info("routing loaded from db",
				"chat_node", rc.ChatNode,
				"automation_node", rc.AutomationNode,
			)
		case store.IsNoRows(err):
			seed := store.RoutingConfig{
				ChatNode:       cfg.ChatNode,
				AutomationNode: cfg.AutomationNode,
			}
			if err := pg.UpsertRouting(context.Background(), seed); err != nil {
				slog.Warn("failed to seed routing config", "error", err)
			} else {
				slog.Info("routing seeded from env",
					"chat_node", seed.ChatNode,
					"automation_node", seed.AutomationNode,
				)
			}
		default:
			slog.Warn("failed to load routing config", "error", err)
		}
	}

	router := api.NewRouterWithOptions(svc, api.RouterOptions{
		APIKey:       cfg.APIKey,
		Metrics:      m,
		Registry:     llmRegistry,
		RoutingStore: pg,
	})

	rootCtx, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()
	if cfg.SchedulerEnabled {
		worker := scheduler.New(pg, svc, scheduler.WorkerConfig{PollInterval: cfg.SchedulerPollInterval})
		go worker.Start(rootCtx)
		slog.Info("scheduler enabled", "poll_interval", cfg.SchedulerPollInterval.String())
	} else {
		slog.Info("scheduler disabled")
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%s", cfg.Port),
		Handler: router,
	}

	go func() {
		slog.Info("starting server", "port", cfg.Port)
		if cfg.APIKey != "" {
			slog.Info("API key authentication enabled")
		}
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	cancelRoot()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
	slog.Info("server stopped")
}

type noopProvider struct{}

func (n *noopProvider) Complete(_ context.Context, _ model.Request) (*model.Response, error) {
	return &model.Response{Content: "No model configured.", FinishReason: "stop"}, nil
}

func (n *noopProvider) Stream(_ context.Context, _ model.Request, onChunk func(string) error) error {
	return onChunk("No model configured.")
}

func connectWithRetry(dsn string, attempts int, delay time.Duration) (*sql.DB, error) {
	var db *sql.DB
	var err error
	for i := 0; i < attempts; i++ {
		db, err = sql.Open("postgres", dsn)
		if err == nil {
			if err = db.Ping(); err == nil {
				return db, nil
			}
		}
		slog.Warn("database not ready, retrying", "attempt", i+1, "error", err)
		time.Sleep(delay)
	}
	return nil, fmt.Errorf("could not connect after %d attempts: %w", attempts, err)
}

func refreshNodeRegistry(ctx context.Context, reg *registry.Registry) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Second}
	return reg.RefreshFromNodes(ctx, client)
}

func startNodeRegistryRefreshLoop(reg *registry.Registry) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := refreshNodeRegistry(context.Background(), reg); err != nil {
				slog.Warn("llm-service node refresh completed with errors", "error", err)
			}
		}
	}()
}
