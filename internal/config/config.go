package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all runtime configuration for the ai-microservice.
type Config struct {
	Port               string
	LogLevel           string
	DBDriver           string // "sqlite" | "postgres"
	DBURL              string
	RedisURL           string
	GithubCopilotToken string
	GatewaySecret      string
	RateLimitRPM       int
	CacheTTLSeconds    int
	Version            string
}

// Load reads configuration from environment variables with safe defaults.
// Returns an error if required variables (GATEWAY_SECRET) are missing.
func Load() (*Config, error) {
	cfg := &Config{
		Port:               getEnv("PORT", "8080"),
		LogLevel:           getEnv("LOG_LEVEL", "info"),
		DBDriver:           getEnv("DB_DRIVER", "sqlite"),
		DBURL:              getEnv("DB_URL", "ai.db"),
		RedisURL:           getEnv("REDIS_URL", ""),
		GithubCopilotToken: os.Getenv("GITHUB_COPILOT_OAUTH_TOKEN"),
		GatewaySecret:      os.Getenv("GATEWAY_SECRET"),
		RateLimitRPM:       getEnvInt("RATE_LIMIT_RPM", 60),
		CacheTTLSeconds:    getEnvInt("CACHE_TTL_SECONDS", 3600),
		Version:            getEnv("VERSION", "dev"),
	}
	if cfg.GatewaySecret == "" {
		return nil, fmt.Errorf("config: GATEWAY_SECRET is required")
	}
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
