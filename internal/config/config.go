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
	}
}
