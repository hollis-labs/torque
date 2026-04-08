package config_test

import (
	"os"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, "clockwork.db", cfg.DBPath)
	assert.Equal(t, "", cfg.PostgresDSN)
	assert.Equal(t, 8990, cfg.HTTPPort)
	assert.Equal(t, 3, cfg.Scheduler.Workers)
	assert.Equal(t, 10, cfg.Scheduler.IntervalSeconds)
	assert.Equal(t, 3, cfg.Scheduler.RetryBudget)
	assert.True(t, cfg.Scheduler.Enabled)
}

func TestConfigFromEnv(t *testing.T) {
	os.Setenv("CLOCKWORK_DB_PATH", "/tmp/test.db")
	os.Setenv("CLOCKWORK_HTTP_PORT", "9999")
	os.Setenv("CLOCKWORK_POSTGRES_DSN", "postgres://localhost/clockwork")
	defer func() {
		os.Unsetenv("CLOCKWORK_DB_PATH")
		os.Unsetenv("CLOCKWORK_HTTP_PORT")
		os.Unsetenv("CLOCKWORK_POSTGRES_DSN")
	}()

	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, "/tmp/test.db", cfg.DBPath)
	assert.Equal(t, 9999, cfg.HTTPPort)
	assert.Equal(t, "postgres://localhost/clockwork", cfg.PostgresDSN)
}
