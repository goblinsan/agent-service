package service

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"
)

type AgentConfig struct {
	ID              string                `json:"id"`
	Name            string                `json:"name"`
	Icon            string                `json:"icon"`
	Color           string                `json:"color"`
	ProviderName    string                `json:"providerName"`
	Model           string                `json:"model"`
	CostClass       string                `json:"costClass"`
	SystemPrompt    string                `json:"systemPrompt,omitempty"`
	Temperature     *float64              `json:"temperature,omitempty"`
	MaxTokens       int                   `json:"maxTokens,omitempty"`
	EnableReasoning bool                  `json:"enableReasoning,omitempty"`
	FeatureFlags    map[string]bool       `json:"featureFlags,omitempty"`
	EndpointConfig  EndpointConfig        `json:"endpointConfig,omitempty"`
	PersonalContext PersonalContextConfig `json:"personalContext,omitempty"`
	TTSVoiceID      string                `json:"ttsVoiceId,omitempty"`
	ExecutionMode   string                `json:"executionMode,omitempty"`
	Source          string                `json:"source,omitempty"`
	Enabled         *bool                 `json:"enabled,omitempty"`
}

type PersonalContextConfig struct {
	Enabled      *bool `json:"enabled,omitempty"`
	Profile      *bool `json:"profile,omitempty"`
	Memories     *bool `json:"memories,omitempty"`
	Goals        *bool `json:"goals,omitempty"`
	Events       *bool `json:"events,omitempty"`
	PersonalData *bool `json:"personalData,omitempty"`
}

type EndpointConfig struct {
	BaseURL     string         `json:"baseUrl,omitempty"`
	APIKey      string         `json:"apiKey,omitempty"`
	ModelParams map[string]any `json:"modelParams,omitempty"`
}

type agentCatalog struct {
	path string
}

func newAgentCatalog(path string) *agentCatalog {
	return &agentCatalog{path: strings.TrimSpace(path)}
}

func (c *agentCatalog) list(defaultModel string) []AgentConfig {
	agents, err := c.load()
	if err != nil {
		if c.path != "" {
			slog.Warn("agent catalog unavailable", "path", c.path, "error", err)
		}
		return defaultAgentCatalog(defaultModel)
	}

	result := make([]AgentConfig, 0, len(agents))
	for _, agent := range agents {
		agent = normalizeAgentConfig(agent)
		if !agentEnabled(agent) {
			continue
		}
		agent.SystemPrompt = ""
		agent.EndpointConfig = EndpointConfig{}
		agent.Source = firstNonEmpty(agent.Source, "remote")
		result = append(result, agent)
	}
	if len(result) == 0 {
		return defaultAgentCatalog(defaultModel)
	}
	return result
}

func (c *agentCatalog) get(id string) (AgentConfig, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return AgentConfig{}, false
	}
	agents, err := c.load()
	if err != nil {
		if c.path != "" {
			slog.Warn("agent catalog unavailable", "path", c.path, "error", err)
		}
		return AgentConfig{}, false
	}
	for _, agent := range agents {
		agent = normalizeAgentConfig(agent)
		if agent.ID == id && agentEnabled(agent) {
			return agent, true
		}
	}
	return AgentConfig{}, false
}

func (c *agentCatalog) load() ([]AgentConfig, error) {
	if c == nil || c.path == "" {
		return nil, os.ErrNotExist
	}
	raw, err := os.ReadFile(c.path)
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		ServiceProfiles struct {
			GatewayChatPlatform struct {
				Agents []AgentConfig `json:"agents"`
			} `json:"gatewayChatPlatform"`
		} `json:"serviceProfiles"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, err
	}
	return wrapper.ServiceProfiles.GatewayChatPlatform.Agents, nil
}

func normalizeAgentConfig(agent AgentConfig) AgentConfig {
	agent.ID = strings.TrimSpace(agent.ID)
	agent.Name = strings.TrimSpace(agent.Name)
	agent.Icon = strings.TrimSpace(agent.Icon)
	agent.Color = strings.TrimSpace(agent.Color)
	agent.ProviderName = strings.TrimSpace(agent.ProviderName)
	agent.Model = strings.TrimSpace(agent.Model)
	agent.CostClass = strings.TrimSpace(agent.CostClass)
	agent.TTSVoiceID = strings.TrimSpace(agent.TTSVoiceID)
	agent.ExecutionMode = strings.TrimSpace(agent.ExecutionMode)
	if agent.TTSVoiceID == "" && agent.EndpointConfig.ModelParams != nil {
		if voiceID, ok := agent.EndpointConfig.ModelParams["ttsVoiceId"].(string); ok {
			agent.TTSVoiceID = strings.TrimSpace(voiceID)
		}
	}
	if agent.Name == "" {
		agent.Name = agent.ID
	}
	if agent.Icon == "" {
		agent.Icon = strings.ToUpper(firstNonEmpty(firstLetters(agent.Name), "AI"))
	}
	if agent.Color == "" {
		agent.Color = "#2563eb"
	}
	if agent.ProviderName == "" {
		agent.ProviderName = "agent-service"
	}
	if agent.CostClass == "" {
		agent.CostClass = "free"
	}
	if agent.ExecutionMode == "" {
		agent.ExecutionMode = "orchestrated"
	}
	return agent
}

func agentEnabled(agent AgentConfig) bool {
	return agent.Enabled == nil || *agent.Enabled
}

func defaultAgentCatalog(model string) []AgentConfig {
	enabled := true
	return []AgentConfig{
		{
			ID:            "project-manager",
			Name:          "Project Manager",
			Icon:          "PM",
			Color:         "#2563eb",
			ProviderName:  "agent-service",
			Model:         firstNonEmpty(model, "agent-service"),
			CostClass:     "free",
			ExecutionMode: "orchestrated",
			Source:        "agent-service",
			Enabled:       &enabled,
		},
	}
}

func firstLetters(value string) string {
	words := strings.Fields(value)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	for _, word := range words {
		if word == "" {
			continue
		}
		b.WriteString(word[:1])
		if b.Len() >= 2 {
			break
		}
	}
	return b.String()
}
