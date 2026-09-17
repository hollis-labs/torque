package mcpadapter_test

// Regression coverage for the write-path priority validation gap.
//
// torque_task_list rejected an unparseable priority with arg_invalid, but
// every WRITE path fed the same argument through reqInt, which returns 0 for
// anything it cannot parse. The two halves of the API therefore disagreed
// about what a priority is:
//
//	torque_task_create {"priority":"high"}     -> silently stored the default 2
//	torque_task_update {"priority":"banana"}   -> silently OVERWROTE a real
//	                                              priority with 0, ok=true
//
// The update case is the damaging one: a caller's typo destroyed an existing
// value and the response reported success. These tests pin the corrected
// behavior — reject on write, and keep every legitimate encoding working.

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPriority_Create_RejectsNonInteger — a word where an integer belongs is
// a caller error, not a request to use the default.
func TestPriority_Create_RejectsNonInteger(t *testing.T) {
	a := setupAdapter(t)

	for _, bad := range []string{"high", "banana", "2.5.1", "", "p1"} {
		t.Run(fmt.Sprintf("priority=%q", bad), func(t *testing.T) {
			text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
				"title":    "priority probe",
				"priority": bad,
			})
			require.True(t, isErr, "non-integer priority must be an error: %s", text)
			code, _, field := parseError(t, text)
			assert.Equal(t, "arg_invalid", code)
			assert.Equal(t, "priority", field, "arg_invalid must pinpoint the offending field")
		})
	}
}

// TestPriority_Update_RejectsNonInteger is the important one: before the fix
// this returned ok=true and clobbered the stored priority with 0.
func TestPriority_Update_RejectsNonInteger(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":    "priority update probe",
		"priority": "3",
	})
	require.False(t, isErr, "seed create should succeed: %s", text)
	var created struct {
		ID       string `json:"ID"`
		Priority int    `json:"Priority"`
	}
	parseData(t, text, &created)
	require.NotEmpty(t, created.ID)
	require.Equal(t, 3, created.Priority, "seed must start at a known non-default priority")

	text, isErr = callTool(t, a, "torque_task_update", map[string]interface{}{
		"id":       created.ID,
		"priority": "banana",
	})
	require.True(t, isErr, "non-integer priority must be an error: %s", text)
	code, _, field := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Equal(t, "priority", field)

	// The stored value must be untouched — a rejected write writes nothing.
	text, isErr = callTool(t, a, "torque_task_get", map[string]interface{}{"id": created.ID})
	require.False(t, isErr, "get should succeed: %s", text)
	var got struct {
		Priority int `json:"Priority"`
	}
	parseData(t, text, &got)
	assert.Equal(t, 3, got.Priority, "a rejected update must not modify the stored priority")
}

// TestPriority_Write_AcceptsValidEncodings guards against over-correcting:
// string-encoded integers are the normal shape from LLM-backed MCP clients
// (see adapter_numeric_test.go) and must keep working, as must a real number.
func TestPriority_Write_AcceptsValidEncodings(t *testing.T) {
	a := setupAdapter(t)

	cases := []struct {
		name string
		val  interface{}
		want int
	}{
		{"string-encoded", "4", 4},
		{"json-number", float64(5), 5},
		{"explicit-1", "1", 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
				"title":    "valid priority " + tc.name,
				"priority": tc.val,
			})
			require.False(t, isErr, "valid priority must not error: %s", text)
			var created struct {
				Priority int `json:"Priority"`
			}
			parseData(t, text, &created)
			assert.Equal(t, tc.want, created.Priority)
		})
	}
}

// TestPriority_Create_ZeroStillDefaults pins the pre-existing sentinel
// behavior that this change deliberately does NOT alter: priority=0 on create
// is the "unset" signal that service.CreateTask turns into 2. Only genuinely
// unparseable input is newly rejected.
func TestPriority_Create_ZeroStillDefaults(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":    "explicit zero priority",
		"priority": "0",
	})
	require.False(t, isErr, "priority=0 must remain accepted: %s", text)
	var created struct {
		Priority int `json:"Priority"`
	}
	parseData(t, text, &created)
	assert.Equal(t, 2, created.Priority, "0 is the unset sentinel and still defaults to 2")
}

// TestPriority_Omitted_StillDefaults — the common path must be unaffected.
func TestPriority_Omitted_StillDefaults(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "no priority supplied",
	})
	require.False(t, isErr, "omitted priority must not error: %s", text)
	var created struct {
		Priority int `json:"Priority"`
	}
	parseData(t, text, &created)
	assert.Equal(t, 2, created.Priority)
}

// TestPriority_Plan_RejectsNonInteger — plan create/update share the gap.
func TestPriority_Plan_RejectsNonInteger(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_plan_create", map[string]interface{}{
		"title":    "plan priority probe",
		"priority": "high",
	})
	require.True(t, isErr, "non-integer plan priority must be an error: %s", text)
	code, _, field := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Equal(t, "priority", field)
}
