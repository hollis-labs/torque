package mcpadapter_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUnknownArg_RejectedNotDropped is the regression test for the general
// half of CW-20260907-0060 defect 1: "unknown and non-writable fields should
// never be silently discarded." A false success does not surface as a crash,
// so nothing but a test catches it.
//
// Before this guard, neither the MCP protocol layer nor mcp-go validated an
// argument against the tool's schema, and Torque's handlers read inputs
// presence-based — so an argument no handler reads was simply not read, and
// the call still answered ok:true.
func TestUnknownArg_RejectedNotDropped(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "guard target", "description": "guard target",
	})
	require.False(t, isErr, "create: %s", text)
	var created map[string]interface{}
	parseData(t, text, &created)
	taskID, _ := created["ID"].(string)

	text, isErr = callTool(t, a, "torque_task_update", map[string]interface{}{
		"id":                  taskID,
		"totally_bogus_field": "value",
	})
	require.True(t, isErr, "an undeclared argument must be rejected: %s", text)
	code, msg, field := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Equal(t, "totally_bogus_field", field, "a single offender is named in error.field")
	assert.Contains(t, msg, "totally_bogus_field")
	assert.Contains(t, msg, "torque_task_update", "the error names the tool")
	assert.Contains(t, msg, "accepted arguments:", "the error names its own remedy")
	assert.Contains(t, msg, "title", "the accepted set is enumerated")
}

// The reported shape: `body` passed where the schema says `content`. This is
// the call that persisted an empty comment and answered ok:true.
func TestUnknownArg_MisnamedFieldRejected(t *testing.T) {
	a := setupAdapter(t)
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{"title": "t"})
	require.False(t, isErr, text)
	var created map[string]interface{}
	parseData(t, text, &created)
	taskID, _ := created["ID"].(string)

	text, isErr = callTool(t, a, "torque_comment_add", map[string]interface{}{
		"entity_type": "task",
		"entity_id":   taskID,
		"body":        "this is not the content field",
	})
	require.True(t, isErr, "a misnamed field must be rejected: %s", text)
	_, msg, _ := parseError(t, text)
	assert.Contains(t, msg, "body")
	assert.Contains(t, msg, "content", "the accepted set names the field they meant")

	// Nothing was persisted by the rejected call.
	text, isErr = callTool(t, a, "torque_comment_list", map[string]interface{}{
		"entity_type": "task", "entity_id": taskID,
	})
	require.False(t, isErr, text)
	var listed struct {
		Meta struct {
			Returned int `json:"returned"`
		} `json:"meta"`
	}
	parseData(t, text, &listed)
	assert.Equal(t, 0, listed.Meta.Returned, "a rejected call must not have written anything")
}

// Several offenders are all named, so a caller fixes them in one round trip
// instead of discovering them one at a time.
func TestUnknownArg_AllOffendersNamed(t *testing.T) {
	a := setupAdapter(t)
	text, isErr := callTool(t, a, "torque_task_list", map[string]interface{}{
		"zzz_second": "b",
		"aaa_first":  "a",
	})
	require.True(t, isErr, "%s", text)
	code, msg, field := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Contains(t, msg, "aaa_first")
	assert.Contains(t, msg, "zzz_second")
	assert.Empty(t, field, "error.field stays empty when more than one argument is at fault")
	assert.Less(t, indexOf(msg, "zzz_second"), len(msg))
	assert.Less(t, indexOf(msg, "aaa_first"), indexOf(msg, "zzz_second"),
		"offenders are listed in a stable sorted order")
}

// Trace headers ride alongside a tool's own inputs and are deliberately
// absent from every schema — the guard must not reject them.
func TestUnknownArg_TraceHeadersExempt(t *testing.T) {
	a := setupAdapter(t)
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":        "traced create",
		"_traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"_tracestate":  "hollis=1",
	})
	require.False(t, isErr, "trace context must not be rejected as unknown: %s", text)
	var created map[string]interface{}
	parseData(t, text, &created)
	assert.Equal(t, "traced create", created["Title"])
}

// A tool that declares no arguments still rejects one.
func TestUnknownArg_ZeroArgToolRejects(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_health", map[string]interface{}{})
	require.False(t, isErr, "health with no args: %s", text)

	text, isErr = callTool(t, a, "torque_health", map[string]interface{}{"verbose": "true"})
	require.True(t, isErr, "%s", text)
	_, msg, _ := parseError(t, text)
	assert.Contains(t, msg, "this tool accepts no arguments")
}

// The loopback subset is registered through the same addTool chokepoint, so
// the guard covers the worker surface too.
func TestUnknownArg_LoopbackToolsGuarded(t *testing.T) {
	fix := setupLoopback(t)
	text, isErr := callTool(t, fix.loopback, "torque_task_get", map[string]interface{}{
		"comment": "true", // singular — the real argument is `comments`
	})
	require.True(t, isErr, "%s", text)
	_, msg, _ := parseError(t, text)
	assert.Contains(t, msg, "comment")
	assert.Contains(t, msg, "comments_limit")
}

// Every declared argument is still accepted — the guard must not reject a
// legitimate call.
func TestUnknownArg_DeclaredArgsStillAccepted(t *testing.T) {
	a := setupAdapter(t)
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "fully specified",
		"description": "d",
		"priority":    "1",
		"kind":        "agent",
		"executor":    "cli",
		"tags":        `["a","b"]`,
		"manual":      true,
	})
	require.False(t, isErr, "a call using only declared arguments must succeed: %s", text)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
