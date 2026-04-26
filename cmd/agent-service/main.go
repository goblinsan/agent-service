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
	agentrunner "github.com/goblinsan/agent-service/internal/runner"
	"github.com/goblinsan/agent-service/internal/service"
	"github.com/goblinsan/agent-service/internal/store"
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
	switch {
	case len(cfg.LLMNodes) > 0:
		// Multi-node pool: build a registry from the node URLs and wrap it in a Pool.
		nodes := make([]registry.NodeConfig, len(cfg.LLMNodes))
		for i, u := range cfg.LLMNodes {
			nodes[i] = registry.NodeConfig{Name: fmt.Sprintf("node-%d", i+1), URL: u}
		}
		reg := registry.New(nodes)
		if err := refreshNodeRegistry(context.Background(), reg); err != nil {
			slog.Warn("initial llm-service node discovery completed with errors", "error", err)
		}
		startNodeRegistryRefreshLoop(reg)
		provider = registry.NewPool(reg, func(url string) model.Provider {
			return llama.New(url)
		})
		slog.Info("llm-service pool configured", "nodes", len(nodes))
	case cfg.LlamaURL != "":
		provider = llama.New(cfg.LlamaURL)
	default:
		provider = &noopProvider{}
	}

	m := &metrics.Metrics{}

	var svc *service.Service
	if cfg.MCPEndpoint != "" {
		svc = service.NewWithOptions(pg, provider, cfg.AgentMaxSteps, service.ServiceOptions{
			Runner:  agentrunner.NewMCPRunner(cfg.MCPEndpoint, nil),
			Metrics: m,
		})
		slog.Info("MCP runner configured", "endpoint", cfg.MCPEndpoint)
	} else {
		svc = service.NewWithOptions(pg, provider, cfg.AgentMaxSteps, service.ServiceOptions{
			Metrics: m,
		})
	}

	router := api.NewRouterWithOptions(svc, api.RouterOptions{
		APIKey:  cfg.APIKey,
		Metrics: m,
	})

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
