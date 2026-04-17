package config

import (
	"os"
	"strconv"
)

type Config struct {
	DBPath      string
	PostgresDSN string
	HTTPPort    int
	RepoRoot    string
	DataDir     string
	Scheduler   SchedulerConfig
	Concurrency ConcurrencyConfig
	Merge       MergeConfig
}

type ConcurrencyConfig struct {
	MaxReadConns     int
	BusyTimeoutMs    int
	WriteChannelSize int
	DrainBatchSize   int
	DrainIntervalMs  int
	QueueDBPath      string
}

// MergeConfig holds configuration for merge conflict resolution.
type MergeConfig struct {
	// ResolutionExecutor is the executor to use for resolution tasks (e.g., "cli").
	ResolutionExecutor string

	// ResolutionAgent is the agent profile to use for resolution tasks.
	ResolutionAgent string

	// ConfidenceThreshold is the minimum confidence score to auto-accept a resolution.
	ConfidenceThreshold float64

	// MaxResolutionAttempts is the max number of times a resolution task can be retried.
	MaxResolutionAttempts int

	// NotifyOnConflict sends a notification when a merge conflict is detected.
	NotifyOnConflict bool
}

type SchedulerConfig struct {
	Workers          int
	IntervalSeconds  int
	RetryBudget      int
	CostCeiling      float64
	HeartbeatSeconds int
	StaleSeconds     int
	Enabled          bool
	MaxPerProject    int
	DefaultMerge     string
	WorktreeCleanup  string

	// Per-run worktree dispatch — opt-in. When enabled, each run gets its
	// own ephemeral git worktree branched from origin/main so concurrent
	// runs can never collide and human git ops on the main tree can't
	// silently hijack agent state.
	WorktreePerRun   bool
	WorktreeRoot     string // empty means "${repoRoot}-worktrees"
	WorktreeKeepDays int
}

func Load() (*Config, error) {
	cfg := &Config{
		DBPath:      envOr("CLOCKWORK_DB_PATH", "clockwork.db"),
		PostgresDSN: os.Getenv("CLOCKWORK_POSTGRES_DSN"),
		HTTPPort:    envInt("CLOCKWORK_HTTP_PORT", 8990),
		RepoRoot:    os.Getenv("CLOCKWORK_REPO"),
		DataDir:     envOr("CLOCKWORK_DATA_DIR", ".clockwork"),
		Scheduler: SchedulerConfig{
			Workers:          envInt("CLOCKWORK_SCHED_WORKERS", 3),
			IntervalSeconds:  envInt("CLOCKWORK_SCHED_INTERVAL", 10),
			RetryBudget:      envInt("CLOCKWORK_SCHED_RETRY_BUDGET", 3),
			CostCeiling:      envFloat("CLOCKWORK_SCHED_COST_CEILING", 0),
			HeartbeatSeconds: envInt("CLOCKWORK_SCHED_HEARTBEAT", 15),
			StaleSeconds:     envInt("CLOCKWORK_SCHED_STALE", 300),
			Enabled:          envBool("CLOCKWORK_SCHED_ENABLED", true),
			MaxPerProject:    envInt("CLOCKWORK_SCHED_MAX_PER_PROJECT", 2),
			DefaultMerge:     envOr("CLOCKWORK_SCHED_DEFAULT_MERGE", "none"),
			WorktreeCleanup:  envOr("CLOCKWORK_SCHED_WORKTREE_CLEANUP", "on_merge"),
			WorktreePerRun:   envBool("CLOCKWORK_WORKTREE_PER_RUN", false),
			WorktreeRoot:     os.Getenv("CLOCKWORK_WORKTREE_ROOT"),
			WorktreeKeepDays: envInt("CLOCKWORK_WORKTREE_KEEP_DAYS", 7),
		},
		Concurrency: ConcurrencyConfig{
			MaxReadConns:     envInt("CLOCKWORK_MAX_READ_CONNS", 4),
			BusyTimeoutMs:    envInt("CLOCKWORK_BUSY_TIMEOUT_MS", 5000),
			WriteChannelSize: envInt("CLOCKWORK_WRITE_CHANNEL_SIZE", 256),
			DrainBatchSize:   envInt("CLOCKWORK_DRAIN_BATCH_SIZE", 50),
			DrainIntervalMs:  envInt("CLOCKWORK_DRAIN_INTERVAL_MS", 1000),
			QueueDBPath:      envOr("CLOCKWORK_QUEUE_DB_PATH", "queue.db"),
		},
		Merge: MergeConfig{
			ResolutionExecutor:    envOr("CLOCKWORK_MERGE_EXECUTOR", "cli"),
			ResolutionAgent:       envOr("CLOCKWORK_MERGE_AGENT", "default"),
			ConfidenceThreshold:   envFloat("CLOCKWORK_MERGE_CONFIDENCE", 0.8),
			MaxResolutionAttempts: envInt("CLOCKWORK_MERGE_MAX_ATTEMPTS", 1),
			NotifyOnConflict:      envBool("CLOCKWORK_MERGE_NOTIFY", true),
		},
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}
