package agent

import (
	"os"
	"time"

	"github.com/hollis-labs/torque/internal/config"
)

// Valid range for task-level timeout_seconds_override. Mirrors the legacy
// cliexec boundaries.
const (
	timeoutOverrideMinSeconds = 60
	timeoutOverrideMaxSeconds = 7200

	// defaultExecutionTimeout matches executor.ExecutionLimits{}.EffectiveTimeout
	// (5 min) so ModeOneShot dispatches without an explicit Limits block fall
	// back to the same window cliexec used.
	defaultExecutionTimeout = 5 * time.Minute

	// loopbackShutdownGrace bounds the per-session loopback teardown call.
	// Matches the legacy cliexec convention (2s defer in cliexec.Run).
	loopbackShutdownGrace = 2 * time.Second
)

// resolveTimeout picks the effective run timeout for ModeOneShot, in priority
// order:
//  1. opts.Metadata["timeout_seconds_override"] when within range
//  2. profile.TimeoutSeconds when > 0
//  3. defaultExecutionTimeout
//
// ModeLongLived doesn't consult this — long-lived sessions don't have a
// turn-level timeout; the surrounding context's deadline (e.g. the HTTP
// handler's) bounds Start.
func resolveTimeout(profile config.AgentProfile, opts Options) time.Duration {
	if secs, ok := taskTimeoutOverride(opts.Metadata); ok {
		return time.Duration(secs) * time.Second
	}
	if profile.TimeoutSeconds > 0 {
		return time.Duration(profile.TimeoutSeconds) * time.Second
	}
	return defaultExecutionTimeout
}

// taskTimeoutOverride extracts a valid timeout_seconds_override from task
// metadata. Forked from cliexec/timeout.go; shape unchanged.
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

// cancelGraceFromEnv parses TORQUE_SCHED_CANCEL_GRACE for SIGTERM→SIGKILL
// child-process grace; falls back to 5s. Forked verbatim from cliexec.
func cancelGraceFromEnv() time.Duration {
	const def = 5 * time.Second
	v := os.Getenv("TORQUE_SCHED_CANCEL_GRACE")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}
	return d
}
