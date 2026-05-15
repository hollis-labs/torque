package worktree

import (
	"os"
	"strconv"
)

// defaultKeepDays is the fallback TTL (in days) for orphaned worktrees when
// TORQUE_WORKTREE_KEEP_DAYS is unset or unparseable. Mirrors config.Load's
// envInt("TORQUE_WORKTREE_KEEP_DAYS", 7) default so env-driven (SpecFromEnv)
// and config-driven Spec construction agree.
const defaultKeepDays = 7

// Mode names the work-root strategy for a run. It is the spec-level,
// env-independent expression of "does this run get its own git worktree".
type Mode string

const (
	// ModeShared runs the agent directly in repo_root — work_root == repo_root.
	// This is the historical default and the behaviour when per-run worktrees
	// are off.
	ModeShared Mode = "shared"

	// ModeWorktree runs the agent in a per-run git worktree created detached
	// at origin/main by SetupPerRun — work_root is the worktree path.
	ModeWorktree Mode = "worktree"
)

// Spec is the resolved, env-independent description of how a run's work_root
// should be obtained. It is the substrate Stage 2 (go-agent-launch) builds on:
// callers construct a Spec once (from config / env / explicit overrides) and
// pass it down rather than re-reading TORQUE_WORKTREE_* env vars at each layer.
//
// The env var TORQUE_WORKTREE_PER_RUN is still honoured — SpecFromEnv reads it
// as the default — but it is now a *source* for a Spec, not the only switch.
type Spec struct {
	// Mode selects shared vs per-run-worktree. Zero value ("") resolves to
	// ModeShared in Resolve.
	Mode Mode

	// Root is the parent directory for per-run worktrees (ModeWorktree only).
	// Empty means "${repoRoot}-worktrees", matching PerRunOptions.Root.
	Root string

	// KeepDays is the TTL for orphaned (dirty) worktrees, forwarded to
	// SweepPerRun. 0 = keep forever.
	KeepDays int
}

// SpecFromEnv builds a Spec from the TORQUE_WORKTREE_* environment, preserving
// the historical env-driven behaviour as a default/fallback:
//
//   - Mode      ← TORQUE_WORKTREE_PER_RUN (mirrors config.SchedulerConfig.WorktreePerRun)
//   - Root      ← TORQUE_WORKTREE_ROOT    (mirrors config.SchedulerConfig.WorktreeRoot)
//   - KeepDays  ← TORQUE_WORKTREE_KEEP_DAYS, parsed via strconv.Atoi; falls back
//     to defaultKeepDays (7) when the var is unset or unparseable, matching
//     config.Load's envInt("TORQUE_WORKTREE_KEEP_DAYS", 7) so env-driven and
//     config-driven Spec construction agree.
//
// Callers that already have a config.SchedulerConfig should prefer
// SpecFromConfig-style construction at the call site; SpecFromEnv exists for
// code paths (and tests) that want the raw env reading without importing
// the config package.
func SpecFromEnv() Spec {
	mode := ModeShared
	if v := os.Getenv("TORQUE_WORKTREE_PER_RUN"); v == "1" || v == "true" || v == "TRUE" {
		mode = ModeWorktree
	}
	keepDays := defaultKeepDays
	if v := os.Getenv("TORQUE_WORKTREE_KEEP_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			keepDays = n
		}
	}
	return Spec{
		Mode:     mode,
		Root:     os.Getenv("TORQUE_WORKTREE_ROOT"),
		KeepDays: keepDays,
	}
}

// WorktreeEnabled reports whether the spec asks for a per-run worktree.
func (s Spec) WorktreeEnabled() bool {
	return s.Mode == ModeWorktree
}

// PerRunOptions projects the Spec onto the PerRunOptions struct consumed by
// SetupPerRun / SweepPerRun. This keeps the lower-level git plumbing API
// stable while letting callers think in terms of the Spec.
func (s Spec) PerRunOptions() PerRunOptions {
	return PerRunOptions{
		Enabled:  s.WorktreeEnabled(),
		Root:     s.Root,
		KeepDays: s.KeepDays,
	}
}

// Resolve turns the Spec into a concrete work_root for a run.
//
//   - ModeShared (or zero value): work_root == repoRoot, no worktree created.
//   - ModeWorktree: SetupPerRun creates a detached worktree at origin/main and
//     work_root is that path.
//
// The returned wtPath is non-empty only when a worktree was actually created;
// it is what the caller must later pass to CleanupPerRun. On worktree setup
// failure Resolve returns the error — callers preserving the historical
// "never block dispatch on git issues" contract should fall back to repoRoot
// themselves (the scheduler does exactly this).
func (s Spec) Resolve(repoRoot string, runID int64) (workRoot, wtPath string, err error) {
	if !s.WorktreeEnabled() {
		return repoRoot, "", nil
	}
	path, err := SetupPerRun(s.PerRunOptions(), repoRoot, runID)
	if err != nil {
		return repoRoot, "", err
	}
	return path, path, nil
}
