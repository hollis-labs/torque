package scheduler

import (
	"encoding/json"
	"testing"

	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunCompletedEventPayload_MinimalShape locks the pre-Phase-3
// minimal payload shape: a ModeOneShot result with no verification fields
// still emits the canonical {"status","cost"} keys. Operators / SSE
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
	assert.NotContains(t, decoded, "tool_use_histogram")
	assert.NotContains(t, decoded, "commits_on_run_branch")
	assert.NotContains(t, decoded, "verification_skip_reason")
}

// TestRunCompletedEventPayload_PopulatedVerification covers the Phase 3
// extended shape: a long-lived worker's run carries histogram + commit
// count, both surfaced as JSON fields so monitors don't have to parse
// stream.jsonl themselves.
func TestRunCompletedEventPayload_PopulatedVerification(t *testing.T) {
	got := runCompletedEventPayload(&executor.ExecutionResult{
		Status:             "review",
		Cost:               0.5,
		CommitsOnRunBranch: 3,
		ToolUseHistogram:   map[string]int{"Edit": 4, "Bash": 1},
	})
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &decoded))
	assert.Equal(t, "review", decoded["status"])
	assert.Equal(t, float64(3), decoded["commits_on_run_branch"])
	hist, _ := decoded["tool_use_histogram"].(map[string]any)
	require.NotNil(t, hist)
	assert.Equal(t, float64(4), hist["Edit"])
	assert.Equal(t, float64(1), hist["Bash"])
}

// TestRunCompletedEventPayload_SkipReason exercises the skipped-
// verification path: verification fields are absent EXCEPT
// verification_skip_reason. Monitors see "the engine couldn't verify"
// rather than falsely believing the worker passed verification.
func TestRunCompletedEventPayload_SkipReason(t *testing.T) {
	got := runCompletedEventPayload(&executor.ExecutionResult{
		Status:                 "review",
		Cost:                   0,
		VerificationSkipReason: "shared mode; engine-side commit verification skipped",
	})
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &decoded))
	assert.Contains(t, decoded["verification_skip_reason"], "shared mode")
	assert.NotContains(t, decoded, "commits_on_run_branch")
}
