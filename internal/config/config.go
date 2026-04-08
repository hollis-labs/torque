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
