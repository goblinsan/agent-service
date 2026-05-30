package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// LLMNode describes a single llm-service backend node parsed from the
// LLM_NODES environment variable.
type LLMNode struct {
	Name string
	URL  string
}

type Config struct {
	DatabaseURL string
	Port        string
	LogLevel    string
	LlamaURL    string
	// LLMNodes is an optional list of llm-service nodes.  When set it takes
	// precedence over LlamaURL for inference requests.  Entries are parsed
	// from LLM_NODES; each entry may be either a bare URL (auto-named
	// "node-N") or "name=url" (e.g. "papai=http://192.168.0.172:5301").
	LLMNodes      []LLMNode
	AgentMaxSteps int
	// APIKey, when set, enables X-API-Key authentication on all API endpoints
	// except /health and /metrics.
	APIKey string
	// MCPEndpoint, when set, enables the MCP tool runner and routes tool calls
	// to the given Model Context Protocol server URL.
	MCPEndpoint string
	// ChatNode pins all chat runs to the named node from LLMNodes.  Replaces
	// the previous CHAT_MODEL string-matching approach.  Empty means use the
	// model-based registry pick.
	ChatNode string
	// AutomationNode is the default node for automation runs that arrive
	// without an explicit model preference.  Empty means use the model-based
	// registry pick.
	AutomationNode string
	// AgentCatalogPath optionally points at a gateway-control-plane config JSON
	// file. When set, serviceProfiles.gatewayChatPlatform.agents is used as the
	// chat persona catalog.
	AgentCatalogPath string
	// SchedulerEnabled enables the background scheduler loop.
	SchedulerEnabled bool
	// SchedulerPollInterval controls how often due jobs are scanned.
	SchedulerPollInterval time.Duration
	// APNSEnabled enables APNs dispatch for newly created notifications.
	APNSEnabled bool
	// APNS credentials and delivery settings.
	APNSTeamID  string
	APNSKeyID   string
	APNSAuthKey string
	APNSTopic   string
	APNSEnv     string
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
	pollInterval := 15 * time.Second
	if v := strings.TrimSpace(os.Getenv("SCHEDULER_POLL_INTERVAL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			pollInterval = d
		}
	}
	schedulerEnabled := false
	if v := strings.TrimSpace(strings.ToLower(os.Getenv("SCHEDULER_ENABLED"))); v == "1" || v == "true" || v == "yes" {
		schedulerEnabled = true
	}
	apnsEnabled := false
	if v := strings.TrimSpace(strings.ToLower(os.Getenv("APNS_ENABLED"))); v == "1" || v == "true" || v == "yes" {
		apnsEnabled = true
	}
	apnsEnv := strings.ToLower(strings.TrimSpace(os.Getenv("APNS_ENV")))
	if apnsEnv == "" {
		apnsEnv = "production"
	}
	var llmNodes []LLMNode
	if v := os.Getenv("LLM_NODES"); v != "" {
		idx := 1
		for _, raw := range strings.Split(v, ",") {
			entry := strings.TrimSpace(raw)
			if entry == "" {
				continue
			}
			name, url := "", entry
			if eq := strings.Index(entry, "="); eq > 0 {
				name = strings.TrimSpace(entry[:eq])
				url = strings.TrimSpace(entry[eq+1:])
			}
			if url == "" {
				continue
			}
			if name == "" {
				name = fmt.Sprintf("node-%d", idx)
			}
			llmNodes = append(llmNodes, LLMNode{Name: name, URL: url})
			idx++
		}
	}
	return &Config{
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		Port:                  port,
		LogLevel:              os.Getenv("LOG_LEVEL"),
		LlamaURL:              os.Getenv("LLAMA_URL"),
		LLMNodes:              llmNodes,
		AgentMaxSteps:         maxSteps,
		APIKey:                os.Getenv("API_KEY"),
		MCPEndpoint:           os.Getenv("MCP_ENDPOINT"),
		ChatNode:              os.Getenv("CHAT_NODE"),
		AutomationNode:        os.Getenv("AUTOMATION_NODE"),
		AgentCatalogPath:      strings.TrimSpace(os.Getenv("AGENT_CATALOG_PATH")),
		SchedulerEnabled:      schedulerEnabled,
		SchedulerPollInterval: pollInterval,
		APNSEnabled:           apnsEnabled,
		APNSTeamID:            strings.TrimSpace(os.Getenv("APNS_TEAM_ID")),
		APNSKeyID:             strings.TrimSpace(os.Getenv("APNS_KEY_ID")),
		APNSAuthKey:           os.Getenv("APNS_AUTH_KEY"),
		APNSTopic:             strings.TrimSpace(os.Getenv("APNS_TOPIC")),
		APNSEnv:               apnsEnv,
	}
}
