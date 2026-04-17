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

func TestConcurrencyConfigDefaults(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, 4, cfg.Concurrency.MaxReadConns)
	assert.Equal(t, 5000, cfg.Concurrency.BusyTimeoutMs)
	assert.Equal(t, 256, cfg.Concurrency.WriteChannelSize)
	assert.Equal(t, 50, cfg.Concurrency.DrainBatchSize)
	assert.Equal(t, 1000, cfg.Concurrency.DrainIntervalMs)
	assert.Equal(t, "queue.db", cfg.Concurrency.QueueDBPath)
}

func TestMergeConfigDefaults(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, "cli", cfg.Merge.ResolutionExecutor)
	assert.Equal(t, "default", cfg.Merge.ResolutionAgent)
	assert.Equal(t, 0.8, cfg.Merge.ConfidenceThreshold)
	assert.Equal(t, 1, cfg.Merge.MaxResolutionAttempts)
	assert.True(t, cfg.Merge.NotifyOnConflict)
}

func TestSchedulerWorktreeCleanupDefault(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, "on_merge", cfg.Scheduler.WorktreeCleanup)
}

func TestPerRunWorktreeDefaults(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)

	assert.False(t, cfg.Scheduler.WorktreePerRun, "per-run worktrees default off")
	assert.Equal(t, "", cfg.Scheduler.WorktreeRoot, "empty root means '${repo}-worktrees'")
	assert.Equal(t, 7, cfg.Scheduler.WorktreeKeepDays)
}

func TestPerRunWorktreeFromEnv(t *testing.T) {
	os.Setenv("CLOCKWORK_WORKTREE_PER_RUN", "true")
	os.Setenv("CLOCKWORK_WORKTREE_ROOT", "/var/clockwork/worktrees")
	os.Setenv("CLOCKWORK_WORKTREE_KEEP_DAYS", "14")
	defer func() {
		os.Unsetenv("CLOCKWORK_WORKTREE_PER_RUN")
		os.Unsetenv("CLOCKWORK_WORKTREE_ROOT")
		os.Unsetenv("CLOCKWORK_WORKTREE_KEEP_DAYS")
	}()

	cfg, err := config.Load()
	require.NoError(t, err)

	assert.True(t, cfg.Scheduler.WorktreePerRun)
	assert.Equal(t, "/var/clockwork/worktrees", cfg.Scheduler.WorktreeRoot)
	assert.Equal(t, 14, cfg.Scheduler.WorktreeKeepDays)
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
