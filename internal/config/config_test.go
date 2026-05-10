package config_test

import (
	"testing"

	"github.com/carreira-cloud/ai-microservice/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("GATEWAY_SECRET", "test-secret")
	t.Setenv("GITHUB_COPILOT_OAUTH_TOKEN", "")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "8080", cfg.Port)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, "sqlite", cfg.DBDriver)
	assert.Equal(t, 60, cfg.RateLimitRPM)
	assert.Equal(t, 3600, cfg.CacheTTLSeconds)
	assert.Equal(t, "dev", cfg.Version)
}

func TestLoad_EnvOverride(t *testing.T) {
	t.Setenv("GATEWAY_SECRET", "secret")
	t.Setenv("PORT", "9090")
	t.Setenv("RATE_LIMIT_RPM", "120")
	t.Setenv("DB_DRIVER", "postgres")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "9090", cfg.Port)
	assert.Equal(t, 120, cfg.RateLimitRPM)
	assert.Equal(t, "postgres", cfg.DBDriver)
}

func TestLoad_MissingGatewaySecret(t *testing.T) {
	t.Setenv("GATEWAY_SECRET", "")
	_, err := config.Load()
	assert.ErrorContains(t, err, "GATEWAY_SECRET")
}
