package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DatabaseURL   string
	Port          string
	LogLevel      string
	LlamaURL      string
	// LLMNodes is an optional comma-separated list of llm-service node URLs,
	// e.g. "http://node1:8080,http://node2:8080".  When set it takes
	// precedence over LlamaURL for inference requests.
	LLMNodes      []string
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
	var llmNodes []string
	if v := os.Getenv("LLM_NODES"); v != "" {
		for _, raw := range strings.Split(v, ",") {
			if u := strings.TrimSpace(raw); u != "" {
				llmNodes = append(llmNodes, u)
			}
		}
	}
	return &Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		Port:          port,
		LogLevel:      os.Getenv("LOG_LEVEL"),
		LlamaURL:      os.Getenv("LLAMA_URL"),
		LLMNodes:      llmNodes,
		AgentMaxSteps: maxSteps,
		APIKey:        os.Getenv("API_KEY"),
		MCPEndpoint:   os.Getenv("MCP_ENDPOINT"),
	}
}
