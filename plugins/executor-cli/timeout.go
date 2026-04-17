package executorcli

import (
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

// Valid range for task-level timeout_seconds_override metadata. Values
// outside this range are ignored and the profile default applies. The
// minimum guards against runaway-short overrides; the maximum (2h) caps
// worst-case blast radius for a single run.
const (
	timeoutOverrideMinSeconds = 60
	timeoutOverrideMaxSeconds = 7200
)

// resolveTimeout picks the effective run timeout in priority order:
//  1. job.Metadata["timeout_seconds_override"] when present and within
//     [timeoutOverrideMinSeconds, timeoutOverrideMaxSeconds]
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
// metadata. Returns (seconds, true) only when the value is an integer
// within [timeoutOverrideMinSeconds, timeoutOverrideMaxSeconds].
// JSON-unmarshalled integers arrive as float64 — integral floats are
// accepted, non-integral floats and other types are rejected.
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
