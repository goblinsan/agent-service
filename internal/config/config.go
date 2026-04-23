package config

import (
	"os"
	"strconv"
)

type Config struct {
	DatabaseURL   string
	Port          string
	LogLevel      string
	LlamaURL      string
	AgentMaxSteps int
	// APIKey, when set, enables X-API-Key authentication on all API endpoints
	// except /health and /metrics.
	APIKey string
	// MCPEndpoint, when set, enables the MCP tool runner and routes tool calls
	// to the given Model Context Protocol server URL.
	MCPEndpoint string
}

func Load() *Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	maxSteps := 10
	if v := os.Getenv("AGENT_MAX_STEPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxSteps = n
		}
	}
	return &Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		Port:          port,
		LogLevel:      os.Getenv("LOG_LEVEL"),
		LlamaURL:      os.Getenv("LLAMA_URL"),
		AgentMaxSteps: maxSteps,
		APIKey:        os.Getenv("API_KEY"),
		MCPEndpoint:   os.Getenv("MCP_ENDPOINT"),
	}
}
