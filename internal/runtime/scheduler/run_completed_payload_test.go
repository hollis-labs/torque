package scheduler

import (
	"encoding/json"
	"testing"

	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunCompletedEventPayload_MinimalShape locks the pre-Phase-3
// minimal payload shape: a ModeOneShot result with VerificationRan=false
// still emits ONLY the canonical {"status","cost"} keys. Operators / SSE
// consumers depending on these keys keep working unchanged.
func TestRunCompletedEventPayload_MinimalShape(t *testing.T) {
	got := runCompletedEventPayload(&executor.ExecutionResult{
		Status: "done",
		Cost:   0.123,
	})
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &decoded))
	assert.Equal(t, "done", decoded["status"])
	assert.Equal(t, 0.123, decoded["cost"])
	assert.NotContains(t, decoded, "verification_ran")
	assert.NotContains(t, decoded, "tool_use_histogram")
	assert.NotContains(t, decoded, "commits_on_run_branch")
	assert.NotContains(t, decoded, "verification_skip_reason")
}

// TestRunCompletedEventPayload_PopulatedVerification covers the Phase 3
// extended shape: a verified long-lived worker's run carries the gate
// flag + histogram + commit count, all surfaced as JSON fields so
// monitors don't have to parse stream.jsonl themselves.
func TestRunCompletedEventPayload_PopulatedVerification(t *testing.T) {
	got := runCompletedEventPayload(&executor.ExecutionResult{
		Status:             "review",
		Cost:               0.5,
		VerificationRan:    true,
		CommitsOnRunBranch: 3,
		ToolUseHistogram:   map[string]int{"Edit": 4, "Bash": 1},
	})
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &decoded))
	assert.Equal(t, "review", decoded["status"])
	assert.Equal(t, true, decoded["verification_ran"])
	assert.Equal(t, float64(3), decoded["commits_on_run_branch"])
	hist, _ := decoded["tool_use_histogram"].(map[string]any)
	require.NotNil(t, hist)
	assert.Equal(t, float64(4), hist["Edit"])
	assert.Equal(t, float64(1), hist["Bash"])
}

// TestRunCompletedEventPayload_VerifiedZeroCommits pins the FAILURE-MODE
// signal — a verified run that landed zero commits is the failure case
// engineers most want to see, and pre-fix the payload was omitting
// commits_on_run_branch when it was 0, making the failure case
// indistinguishable from a ModeOneShot baseline. With VerificationRan
// gating, the field is emitted even when 0.
func TestRunCompletedEventPayload_VerifiedZeroCommits(t *testing.T) {
	got := runCompletedEventPayload(&executor.ExecutionResult{
		Status:             "failed",
		Reason:             "worker exited with edits but no commits on run-branch",
		Cost:               0.5,
		VerificationRan:    true,
		CommitsOnRunBranch: 0,
		ToolUseHistogram:   map[string]int{"Edit": 7, "Bash": 2},
	})
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &decoded))
	assert.Equal(t, "failed", decoded["status"])
	assert.Equal(t, true, decoded["verification_ran"])
	// 0 commits MUST be present (not omitted) so monitors can
	// distinguish "verified=0" from "never verified".
	assert.Equal(t, float64(0), decoded["commits_on_run_branch"])
	require.Contains(t, decoded, "tool_use_histogram")
}

// TestRunCompletedEventPayload_SkipReason exercises the skipped-
// verification path: VerificationRan is still true (the engine looked at
// the run), but the skip reason explains why no real verdict landed.
// Monitors see "the engine couldn't verify" rather than falsely
// believing the worker passed verification.
func TestRunCompletedEventPayload_SkipReason(t *testing.T) {
	got := runCompletedEventPayload(&executor.ExecutionResult{
		Status:                 "review",
		Cost:                   0,
		VerificationRan:        true,
		VerificationSkipReason: "shared mode; engine-side commit verification skipped",
	})
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &decoded))
	assert.Equal(t, true, decoded["verification_ran"])
	assert.Contains(t, decoded["verification_skip_reason"], "shared mode")
	// commits_on_run_branch IS emitted (as 0) even on skip — monitors
	// dashboard logic can key off verification_skip_reason being
	// non-empty to know the count is meaningless. Emitting 0
	// uniformly is simpler than threading an absent value through.
	assert.Equal(t, float64(0), decoded["commits_on_run_branch"])
}
