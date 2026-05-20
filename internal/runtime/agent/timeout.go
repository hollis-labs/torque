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

	// defaultInactivityThreshold is the wall of silence after which a
	// ModeLongLived worker is treated as stalled and idle-reaped. Tuned at
	// the high end of "still plausibly thinking" — claude streams events on
	// every tool call, every delta, every usage update, so 30 minutes of
	// dead air is a genuine stall (a hung subprocess, a runaway prompt loop,
	// a model that exited without signaling). Heartbeats reset the clock;
	// a producing worker runs as long as it needs.
	defaultInactivityThreshold = 30 * time.Minute

	// inactivityThresholdMinSeconds / inactivityThresholdMaxSeconds clamp
	// task-level overrides via metadata.inactivity_threshold_seconds. Lower
	// bound is the existing turn-budget floor (60s — anything less is a
	// configuration mistake); upper bound is six hours (long enough for a
	// genuinely heavy multi-turn ticket, short enough that a forgotten
	// override doesn't pin a slot indefinitely).
	inactivityThresholdMinSeconds = 60
	inactivityThresholdMaxSeconds = 21600

	// defaultHardCeiling is the absolute wall after which a ModeLongLived
	// worker is reaped regardless of activity — the safety net for the
	// "still producing but hopelessly stuck in a loop" case. Activity does
	// NOT reset it; this clock starts at Boot and runs to the end. 12 hours
	// matches the operator-stated "never hard-kill a producing worker"
	// posture: in practice no legitimate ticket should run this long, but
	// the cap exists so a forgotten worker can't hold a slot forever.
	defaultHardCeiling = 12 * time.Hour

	// hardCeilingMinSeconds / hardCeilingMaxSeconds clamp task-level
	// overrides via metadata.hard_ceiling_seconds. Lower bound is one hour
	// (anything shorter is the inactivity threshold's job); upper bound is
	// 48 hours (operator-grade override for a multi-day batch).
	hardCeilingMinSeconds = 3600
	hardCeilingMaxSeconds = 172800
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
	return clampedSecondsFromMetadata(md, "timeout_seconds_override", timeoutOverrideMinSeconds, timeoutOverrideMaxSeconds)
}

// resolveInactivityThreshold picks the effective inactivity-reset threshold
// for a ModeLongLived worker, in priority order:
//
//  1. opts.Metadata["inactivity_threshold_seconds"] when within range
//  2. defaultInactivityThreshold
//
// Profile.TimeoutSeconds is deliberately NOT consulted — it is the
// ModeOneShot wall-clock budget (turn time) and its semantics do not
// translate to a "max silence" gate. Operators wanting a different
// inactivity wall set it per-task via metadata.
func resolveInactivityThreshold(opts Options) time.Duration {
	if secs, ok := clampedSecondsFromMetadata(opts.Metadata, "inactivity_threshold_seconds", inactivityThresholdMinSeconds, inactivityThresholdMaxSeconds); ok {
		return time.Duration(secs) * time.Second
	}
	return defaultInactivityThreshold
}

// resolveHardCeiling picks the effective absolute wall-clock ceiling for a
// ModeLongLived worker. Hard ceiling fires regardless of activity — it is
// the "forgotten worker" safety net, not a budget. Priority:
//
//  1. opts.Metadata["hard_ceiling_seconds"] when within range
//  2. defaultHardCeiling
func resolveHardCeiling(opts Options) time.Duration {
	if secs, ok := clampedSecondsFromMetadata(opts.Metadata, "hard_ceiling_seconds", hardCeilingMinSeconds, hardCeilingMaxSeconds); ok {
		return time.Duration(secs) * time.Second
	}
	return defaultHardCeiling
}

// clampedSecondsFromMetadata reads an integer-seconds value from a task
// metadata map, verifies it falls within [minSec, maxSec] inclusive, and
// returns (value, true). On any failure (key absent, non-integer, out of
// range, fractional float) returns (0, false). Shared by the three
// metadata-driven knobs (timeout_seconds_override, inactivity_threshold_
// seconds, hard_ceiling_seconds) so they share a single coercion path —
// in particular, the silent-zero-on-fractional-float behavior matches
// the legacy cliexec.taskTimeoutOverride contract callers already rely on.
func clampedSecondsFromMetadata(md map[string]any, key string, minSec, maxSec int) (int, bool) {
	if md == nil {
		return 0, false
	}
	raw, ok := md[key]
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
	if secs < minSec || secs > maxSec {
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
