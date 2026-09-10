package mcpadapter_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTaskCreate_DecisionKindDefaultsCheckpointMode is the regression test
// CW-20260907-0060 defect 2 asks for on the CREATE path specifically: the
// documented default (checkpoint_mode=none) used to be invalid for a
// documented kind, so a schema-reading caller could only discover the
// coupling by being rejected.
func TestTaskCreate_DecisionKindDefaultsCheckpointMode(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "a decision",
		"kind":  "decision",
	})
	require.False(t, isErr, "kind=decision with no checkpoint_mode must succeed: %s", text)

	var created map[string]interface{}
	parseData(t, text, &created)
	assert.Equal(t, "decision", created["Kind"])
	assert.Equal(t, "blocking", created["CheckpointMode"],
		"the kind's default must be applied, not merely accepted for validation")
}

// An explicit conflicting value is still refused — and the error says it is
// overriding a stated default rather than only naming the required value.
func TestTaskCreate_DecisionKindRejectsExplicitConflict(t *testing.T) {
	a := setupAdapter(t)

	for _, mode := range []string{"none", "non_blocking"} {
		text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
			"title":           "a decision",
			"kind":            "decision",
			"checkpoint_mode": mode,
		})
		require.True(t, isErr, "explicit checkpoint_mode=%s must be rejected: %s", mode, text)
		code, msg, field := parseError(t, text)
		assert.Equal(t, "arg_invalid", code)
		assert.Equal(t, "checkpoint_mode", field)
		assert.Contains(t, msg, "blocking")
		assert.Contains(t, msg, mode, "the error must name what was actually passed")
		assert.Contains(t, msg, "explicitly", "the error must say the caller overrode the default")
		assert.Contains(t, msg, "default", "the error must reference the stated default it overrides")
	}
}

// Non-decision kinds keep checkpoint_mode's documented default of "none" —
// the kind-specific default must not leak across kinds.
func TestTaskCreate_NonDecisionKindKeepsNoneDefault(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "an agent task",
		"kind":  "agent",
	})
	require.False(t, isErr, "create: %s", text)
	var created map[string]interface{}
	parseData(t, text, &created)
	assert.Equal(t, "none", created["CheckpointMode"])
}

// The update surface documents the same coupling, so it must honor it too:
// promoting a task to kind=decision applies the mode rather than 422-ing on
// the value already stored. And the promoted value must be PERSISTED, not
// merely used to satisfy validation.
func TestTaskUpdate_PromotingToDecisionSetsCheckpointMode(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "starts as agent",
		"kind":  "agent",
	})
	require.False(t, isErr, "create: %s", text)
	var created map[string]interface{}
	parseData(t, text, &created)
	taskID, _ := created["ID"].(string)
	require.Equal(t, "none", created["CheckpointMode"])

	text, isErr = callTool(t, a, "torque_task_update", map[string]interface{}{
		"id":   taskID,
		"kind": "decision",
	})
	require.False(t, isErr, "promote to decision: %s", text)
	var updated map[string]interface{}
	parseData(t, text, &updated)
	assert.Equal(t, "decision", updated["Kind"])
	assert.Equal(t, "blocking", updated["CheckpointMode"])

	// Read it back: a mode that validated as blocking must be stored as
	// blocking, not left at the pre-update value.
	text, isErr = callTool(t, a, "torque_task_get", map[string]interface{}{
		"id": taskID, "comments": "false",
	})
	require.False(t, isErr, "task_get: %s", text)
	var fetched map[string]interface{}
	parseData(t, text, &fetched)
	assert.Equal(t, "blocking", fetched["CheckpointMode"], "the promotion must be persisted")
}

func TestTaskUpdate_DecisionKindRejectsExplicitConflict(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "starts as agent", "kind": "agent",
	})
	require.False(t, isErr, "create: %s", text)
	var created map[string]interface{}
	parseData(t, text, &created)
	taskID, _ := created["ID"].(string)

	text, isErr = callTool(t, a, "torque_task_update", map[string]interface{}{
		"id":              taskID,
		"kind":            "decision",
		"checkpoint_mode": "none",
	})
	require.True(t, isErr, "explicit conflict must be rejected: %s", text)
	code, msg, field := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Equal(t, "checkpoint_mode", field)
	assert.Contains(t, msg, "explicitly")
}
