package config

import (
	"log"
	"os"
	"strconv"
	"strings"
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
	Stuck       StuckConfig
}

// StuckConfig holds tunables for the agentic-execution stuck-task recovery
// probe (CW-20260512-0063, sprint α.5). The probe state machine lives in
// internal/runtime/stuck; this struct just exposes the operator knobs.
//
// Trigger wireup status: no upstream "stuck signal" exists today — the
// probe is callable but unwired. When that wireup lands (separate ticket),
// this is where its tuning lives.
type StuckConfig struct {
	// WaitSeconds is the WAIT-phase timeout. The probe sends the
	// "are you stuck?" user-turn input and then waits up to this many
	// seconds for a status_update envelope tagged with the task's id.
	// On timeout the probe falls through to checkpoint+resume.
	//
	// Default 90 (the midpoint of the sprint-α α.5 60–120s range). Set
	// TORQUE_STUCK_WAIT_SECONDS to override. The stuck package also
	// exports a DefaultWaitTimeout that callers passing a zero
	// ProbeInput.WaitTimeout inherit directly; this config field is for
	// the production trigger wireup once that lands.
	WaitSeconds int
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
	Workers                  int
	IntervalSeconds          int
	RetryBudget              int
	CostCeiling              float64
	HeartbeatSeconds         int
	HeartbeatProgressSeconds int
	StaleSeconds             int
	Enabled                  bool
	MaxPerProject            int
	DefaultMerge             string
	WorktreeCleanup          string

	// Per-run worktree dispatch — opt-in. When enabled, each run gets its
	// own ephemeral git worktree branched from origin/main so concurrent
	// runs can never collide and human git ops on the main tree can't
	// silently hijack agent state.
	WorktreePerRun   bool
	WorktreeRoot     string // empty means "${repoRoot}-worktrees"
	WorktreeKeepDays int

	// DEPRECATED: remove when CW-20260417-0129 (workspace support) ships.
	// ProjectAllowlist is a stopgap for the shared-DB cross-project
	// contamination observed 2026-04-17. When non-empty, the scheduler
	// picker only considers tasks whose project_id is in this list; empty
	// means no filter (current all-projects behavior). Populated at
	// startup from TORQUE_PROJECT_IDS (comma-separated, wins if set) or
	// TORQUE_PROJECT_ID (single id). Read once at Load() — there is no
	// reload mechanism; changing the env var requires a serve restart.
	ProjectAllowlist []string
}

func Load() (*Config, error) {
	cfg := &Config{
		DBPath:      envOr("TORQUE_DB_PATH", "torque.db"),
		PostgresDSN: os.Getenv("TORQUE_POSTGRES_DSN"),
		HTTPPort:    envInt("TORQUE_HTTP_PORT", 8990),
		RepoRoot:    os.Getenv("TORQUE_REPO"),
		DataDir:     envOr("TORQUE_DATA_DIR", ".torque"),
		Scheduler: SchedulerConfig{
			Workers:                  envInt("TORQUE_SCHED_WORKERS", 3),
			IntervalSeconds:          envInt("TORQUE_SCHED_INTERVAL", 10),
			RetryBudget:              envInt("TORQUE_SCHED_RETRY_BUDGET", 3),
			CostCeiling:              envFloat("TORQUE_SCHED_COST_CEILING", 0),
			HeartbeatSeconds:         envInt("TORQUE_SCHED_HEARTBEAT", 15),
			HeartbeatProgressSeconds: envInt("TORQUE_PROGRESS_HEARTBEAT_SECONDS", 30),
			// StaleSeconds is the staleness threshold for worker heartbeats.
			// A row in worker_heartbeats whose last_heartbeat is older than
			// this gets logged, published on the bus as worker.stale, and
			// deleted by the next scheduler tick (CW-20260418-0003 cleanup
			// and CW-20260418-0018 gauge/config). Default 300s (5 min) —
			// long enough to tolerate a slow CLI executor pause, short
			// enough that a crashed worker doesn't linger in the table
			// across a whole session. Tune down for faster feedback in
			// dev/test, not recommended below ~30s in production.
			StaleSeconds:     envInt("TORQUE_SCHED_STALE", 300),
			Enabled:          envBool("TORQUE_SCHED_ENABLED", true),
			MaxPerProject:    envInt("TORQUE_SCHED_MAX_PER_PROJECT", 2),
			DefaultMerge:     envOr("TORQUE_SCHED_DEFAULT_MERGE", "none"),
			WorktreeCleanup:  envOr("TORQUE_SCHED_WORKTREE_CLEANUP", "on_merge"),
			WorktreePerRun:   envBool("TORQUE_WORKTREE_PER_RUN", false),
			WorktreeRoot:     os.Getenv("TORQUE_WORKTREE_ROOT"),
			WorktreeKeepDays: envInt("TORQUE_WORKTREE_KEEP_DAYS", 7),
			// DEPRECATED: remove when CW-20260417-0129 (workspace support) ships.
			ProjectAllowlist: envProjectAllowlist(),
		},
		Concurrency: ConcurrencyConfig{
			MaxReadConns:     envInt("TORQUE_MAX_READ_CONNS", 4),
			BusyTimeoutMs:    envInt("TORQUE_BUSY_TIMEOUT_MS", 5000),
			WriteChannelSize: envInt("TORQUE_WRITE_CHANNEL_SIZE", 256),
			DrainBatchSize:   envInt("TORQUE_DRAIN_BATCH_SIZE", 50),
			DrainIntervalMs:  envInt("TORQUE_DRAIN_INTERVAL_MS", 1000),
			QueueDBPath:      envOr("TORQUE_QUEUE_DB_PATH", "queue.db"),
		},
		Merge: MergeConfig{
			ResolutionExecutor:    envOr("TORQUE_MERGE_EXECUTOR", "cli"),
			ResolutionAgent:       envOr("TORQUE_MERGE_AGENT", "default"),
			ConfidenceThreshold:   envFloat("TORQUE_MERGE_CONFIDENCE", 0.8),
			MaxResolutionAttempts: envInt("TORQUE_MERGE_MAX_ATTEMPTS", 1),
			NotifyOnConflict:      envBool("TORQUE_MERGE_NOTIFY", true),
		},
		Stuck: StuckConfig{
			// 90s = midpoint of the sprint-α α.5 60–120s range. Tuning
			// notes on StuckConfig.WaitSeconds.
			WaitSeconds: envInt("TORQUE_STUCK_WAIT_SECONDS", 90),
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

// envProjectAllowlist parses the project-scope filter env vars for the
// scheduler. TORQUE_PROJECT_IDS (comma-separated) wins if set; otherwise
// TORQUE_PROJECT_ID becomes a single-element list; otherwise nil (no
// filter). Whitespace around tokens is trimmed and empty tokens are
// discarded, so "PRJ-A, PRJ-B" and "PRJ-A,PRJ-B" are equivalent. A non-empty
// allowlist is logged at startup so operators can spot a stale env var
// carried over from a prior shell session.
//
// DEPRECATED: remove when CW-20260417-0129 (workspace support) ships.
func envProjectAllowlist() []string {
	raw := os.Getenv("TORQUE_PROJECT_IDS")
	if raw == "" {
		if single := os.Getenv("TORQUE_PROJECT_ID"); single != "" {
			raw = single
		}
	}
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	log.Printf("[config] scheduler scoped to projects: %v (STOPGAP — CW-20260417-0129 will replace this with workspaces)", out)
	return out
}
