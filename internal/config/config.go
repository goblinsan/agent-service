package config

import "os"

type Config struct {
	DatabaseURL string
	Port        string
	LogLevel    string
}

func Load() *Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	return &Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		Port:        port,
		LogLevel:    os.Getenv("LOG_LEVEL"),
	}
}
