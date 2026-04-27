package cliexec

import (
	"os"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

// Valid range for task-level timeout_seconds_override. Mirrors the legacy
// executor-cli boundaries.
const (
	timeoutOverrideMinSeconds = 60
	timeoutOverrideMaxSeconds = 7200
)

// resolveTimeout picks the effective run timeout, in priority order:
//  1. job.Metadata["timeout_seconds_override"] when within range
//  2. profile.TimeoutSeconds when > 0
//  3. job.Limits.EffectiveTimeout() fallback
func resolveTimeout(profile config.AgentProfile, job *executor.ExecutionJob) time.Duration {
	if job != nil {
		if secs, ok := taskTimeoutOverride(job.Metadata); ok {
			return time.Duration(secs) * time.Second
		}
	}
	if profile.TimeoutSeconds > 0 {
		return time.Duration(profile.TimeoutSeconds) * time.Second
	}
	if job == nil {
		return executor.ExecutionLimits{}.EffectiveTimeout()
	}
	return job.Limits.EffectiveTimeout()
}

// taskTimeoutOverride extracts a valid timeout_seconds_override from task
// metadata. JSON-unmarshalled integers arrive as float64; integral floats
// are accepted, non-integral floats and other types are rejected.
func taskTimeoutOverride(md map[string]any) (int, bool) {
	raw, ok := md["timeout_seconds_override"]
	if !ok {
		return 0, false
	}
	var secs int
	switch v := raw.(type) {
	case int:
		secs = v
	case int64:
		secs = int(v)
	case float64:
		if v != float64(int(v)) {
			return 0, false
		}
		secs = int(v)
	default:
		return 0, false
	}
	if secs < timeoutOverrideMinSeconds || secs > timeoutOverrideMaxSeconds {
		return 0, false
	}
	return secs, true
}

// cancelGraceFromEnv parses CLOCKWORK_SCHED_CANCEL_GRACE for SIGTERM→SIGKILL
// child-process grace; falls back to 5s. Same env var the scheduler reads, so
// one knob controls both layers.
func cancelGraceFromEnv() time.Duration {
	const def = 5 * time.Second
	v := os.Getenv("CLOCKWORK_SCHED_CANCEL_GRACE")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}
	return d
}
