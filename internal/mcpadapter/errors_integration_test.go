package mcpadapter_test

// End-to-end assertions for the Phase C error taxonomy. Each test induces
// one ErrorCode path via real tool calls and parses the structured
// `{ok:false, error: {code, message, field?}}` envelope to confirm the
// mapper classified it correctly.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestError_ArgInvalid_BadJSON — passing malformed JSON to the `tags`
// arg on clockwork_task_create should surface as arg_invalid with
// field="tags", not as a generic internal error.
func TestError_ArgInvalid_BadJSON(t *testing.T) {
	a := setupAdapter(t)
	text, isErr := callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title":       "bad tags",
		"description": "x",
		"tags":        "not-json-at-all",
	})
	require.True(t, isErr, "malformed JSON must be an error: %s", text)
	code, _, field := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Equal(t, "tags", field, "arg_invalid must pinpoint the offending field")
}

// TestError_NotFound_UnknownTask — clockwork_task_get on a nonexistent ID
// must map to not_found via the sqlstore.ErrTaskNotFound sentinel.
func TestError_NotFound_UnknownTask(t *testing.T) {
	a := setupAdapter(t)
	text, isErr := callTool(t, a, "clockwork_task_get", map[string]interface{}{
		"id": "T-DOES-NOT-EXIST",
	})
	require.True(t, isErr, "unknown id must be an error: %s", text)
	code, msg, _ := parseError(t, text)
	assert.Equal(t, "not_found", code)
	assert.Contains(t, msg, "not found", "message should reflect the miss")
}

// TestError_Conflict_InvalidTransition — transitioning a task from done back
// to doing violates the lifecycle FSM and must map to conflict.
func TestError_Conflict_InvalidTransition(t *testing.T) {
	a := setupAdapter(t)

	// Seed a task and drive it to done.
	text, _ := callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title":       "conflict test",
		"description": "x",
	})
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	for _, status := range []string{"doing", "review", "done"} {
		_, err := callTool(t, a, "clockwork_task_transition", map[string]interface{}{
			"id": id, "status": status,
		})
		require.False(t, err, "seed transition %s: %v", status, err)
	}

	// Attempt the illegal done -> doing reversal.
	text, isErr := callTool(t, a, "clockwork_task_transition", map[string]interface{}{
		"id": id, "status": "doing",
	})
	require.True(t, isErr, "done->doing must be a conflict: %s", text)
	code, _, _ := parseError(t, text)
	assert.Equal(t, "conflict", code)
}

// TestError_Domain_FeatureDisabled — clockwork_sprint_* tools are only
// registered when features.sprints is enabled, so a disabled-feature call
// returns a JSON-RPC "method not found" at the framing layer. When the
// feature IS enabled but a sibling domain rule rejects the input, the
// service's FeatureDisabledError maps to domain. We exercise the mapper
// path directly for the domain classification test (see errors_test.go
// TestMapServiceError_Table) and confirm here the MCP surface correctly
// wraps an underlying domain error in the envelope shape.
//
// Sibling fixture: creating a template with id="" returns a validation
// failure from the service layer that maps to arg_invalid, so we can't
// use that. Instead, use clockwork_template_delete with a referencing
// task — ErrTemplateReferenced maps to conflict, not domain. A pure
// domain-path test requires an injected FeatureDisabledError without
// the tool-registration gate, which the current adapter surface doesn't
// expose. Document the gap and rely on the mapper unit test for
// classification coverage.
func TestError_Domain_DocumentedGap(t *testing.T) {
	t.Skip("domain-path coverage lives in TestMapServiceError_Table; no MCP surface currently wraps FeatureDisabledError without the tool-registration gate. Follow-up if a domain rule lands elsewhere.")
}

// TestError_Internal_UnmappedError — verified via the mapper unit test
// (TestMapServiceError_InternalSanitizes in errors_test.go). Surfacing an
// internal error through the full stack deterministically requires error
// injection into the service layer, which isn't in scope for Phase C.
// Documented here so the exit-gate matrix (arg_invalid, not_found,
// conflict, domain, internal) is accounted for in one place.
func TestError_Internal_DocumentedGap(t *testing.T) {
	t.Skip("internal-path coverage lives in TestMapServiceError_InternalSanitizes in the internal errors_test.go. Full-stack induction requires service-layer error injection; out of scope for Phase C.")
}

// TestError_NotFound_UnknownCheckpoint — clockwork_task_checkpoint_get
// with an unknown correlation_id must map to not_found via
// sqlstore.ErrCheckpointNotFound.
func TestError_NotFound_UnknownCheckpoint(t *testing.T) {
	a := setupAdapter(t)
	text, isErr := callTool(t, a, "clockwork_task_checkpoint_get", map[string]interface{}{
		"correlation_id": "01HK_DOES_NOT_EXIST",
	})
	require.True(t, isErr, "unknown correlation_id must be an error: %s", text)
	code, _, _ := parseError(t, text)
	assert.Equal(t, "not_found", code)
}

// TestError_Conflict_DeleteReferencedTemplate — clockwork_template_delete
// with a template still referenced by tasks returns ErrTemplateReferenced
// which the mapper classifies as conflict.
func TestError_Conflict_DeleteReferencedTemplate(t *testing.T) {
	a := setupAdapter(t)

	// Create a template and instantiate it so a task references it.
	_, isErr := callTool(t, a, "clockwork_template_create", map[string]interface{}{
		"id":          "ref-test",
		"name":        "Ref Test",
		"description": "x",
		"kind":        "agent",
		"executor":    "cli",
	})
	require.False(t, isErr)

	_, isErr = callTool(t, a, "clockwork_task_create_from_template", map[string]interface{}{
		"template_id": "ref-test",
		"title":       "ref-holder",
	})
	require.False(t, isErr)

	// Delete should now be rejected.
	text, isErr := callTool(t, a, "clockwork_template_delete", map[string]interface{}{
		"id": "ref-test",
	})
	require.True(t, isErr, "referenced-template delete must be a conflict: %s", text)
	code, _, _ := parseError(t, text)
	assert.Equal(t, "conflict", code)
}
