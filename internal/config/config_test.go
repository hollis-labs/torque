package config_test

import (
	"os"
	"testing"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, "torque.db", cfg.DBPath)
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
	assert.Equal(t, "", cfg.Scheduler.WorktreeRoot, "empty root means a true sibling of the repo")
	assert.Equal(t, 7, cfg.Scheduler.WorktreeKeepDays)
	assert.Equal(t, "", cfg.Scheduler.WorktreePrecheck, "unset lets the scheduler default apply")
}

func TestPerRunWorktreeFromEnv(t *testing.T) {
	os.Setenv("TORQUE_WORKTREE_PER_RUN", "true")
	os.Setenv("TORQUE_WORKTREE_ROOT", "/var/torque/worktrees")
	os.Setenv("TORQUE_WORKTREE_KEEP_DAYS", "14")
	os.Setenv("TORQUE_WORKTREE_PRECHECK", "warn")
	defer func() {
		os.Unsetenv("TORQUE_WORKTREE_PER_RUN")
		os.Unsetenv("TORQUE_WORKTREE_ROOT")
		os.Unsetenv("TORQUE_WORKTREE_KEEP_DAYS")
		os.Unsetenv("TORQUE_WORKTREE_PRECHECK")
	}()

	cfg, err := config.Load()
	require.NoError(t, err)

	assert.True(t, cfg.Scheduler.WorktreePerRun)
	assert.Equal(t, "/var/torque/worktrees", cfg.Scheduler.WorktreeRoot)
	assert.Equal(t, 14, cfg.Scheduler.WorktreeKeepDays)
	assert.Equal(t, "warn", cfg.Scheduler.WorktreePrecheck)
}

// DEPRECATED: remove when CW-20260417-0129 (workspace support) ships.
// Covers the CW-20260417-0130 stopgap project-scope env-var parsing.
func TestProjectAllowlistFromEnv(t *testing.T) {
	t.Run("unset = nil", func(t *testing.T) {
		os.Unsetenv("TORQUE_PROJECT_ID")
		os.Unsetenv("TORQUE_PROJECT_IDS")
		cfg, err := config.Load()
		require.NoError(t, err)
		assert.Nil(t, cfg.Scheduler.ProjectAllowlist)
	})

	t.Run("single TORQUE_PROJECT_ID", func(t *testing.T) {
		os.Unsetenv("TORQUE_PROJECT_IDS")
		os.Setenv("TORQUE_PROJECT_ID", "PRJ-A")
		defer os.Unsetenv("TORQUE_PROJECT_ID")

		cfg, err := config.Load()
		require.NoError(t, err)
		assert.Equal(t, []string{"PRJ-A"}, cfg.Scheduler.ProjectAllowlist)
	})

	t.Run("comma-separated TORQUE_PROJECT_IDS with whitespace", func(t *testing.T) {
		os.Unsetenv("TORQUE_PROJECT_ID")
		os.Setenv("TORQUE_PROJECT_IDS", "PRJ-A, PRJ-B ,PRJ-C")
		defer os.Unsetenv("TORQUE_PROJECT_IDS")

		cfg, err := config.Load()
		require.NoError(t, err)
		assert.Equal(t, []string{"PRJ-A", "PRJ-B", "PRJ-C"}, cfg.Scheduler.ProjectAllowlist)
	})

	t.Run("TORQUE_PROJECT_IDS wins over TORQUE_PROJECT_ID", func(t *testing.T) {
		os.Setenv("TORQUE_PROJECT_ID", "PRJ-X")
		os.Setenv("TORQUE_PROJECT_IDS", "PRJ-A,PRJ-B")
		defer func() {
			os.Unsetenv("TORQUE_PROJECT_ID")
			os.Unsetenv("TORQUE_PROJECT_IDS")
		}()

		cfg, err := config.Load()
		require.NoError(t, err)
		assert.Equal(t, []string{"PRJ-A", "PRJ-B"}, cfg.Scheduler.ProjectAllowlist)
	})

	t.Run("only-whitespace and commas collapse to nil", func(t *testing.T) {
		os.Unsetenv("TORQUE_PROJECT_ID")
		os.Setenv("TORQUE_PROJECT_IDS", " , ,  ")
		defer os.Unsetenv("TORQUE_PROJECT_IDS")

		cfg, err := config.Load()
		require.NoError(t, err)
		assert.Nil(t, cfg.Scheduler.ProjectAllowlist)
	})
}

func TestConfigFromEnv(t *testing.T) {
	os.Setenv("TORQUE_DB_PATH", "/tmp/test.db")
	os.Setenv("TORQUE_HTTP_PORT", "9999")
	os.Setenv("TORQUE_POSTGRES_DSN", "postgres://localhost/torque")
	defer func() {
		os.Unsetenv("TORQUE_DB_PATH")
		os.Unsetenv("TORQUE_HTTP_PORT")
		os.Unsetenv("TORQUE_POSTGRES_DSN")
	}()

	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, "/tmp/test.db", cfg.DBPath)
	assert.Equal(t, 9999, cfg.HTTPPort)
	assert.Equal(t, "postgres://localhost/torque", cfg.PostgresDSN)
}
