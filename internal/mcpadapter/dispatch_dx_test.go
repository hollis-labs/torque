package mcpadapter_test

// DX-fix coverage for CW-20260517-0011 edges 5, 7, 8:
//   - edge 5: torque_sprint_start opens the dispatch gate for a sprint.
//   - edge 7: torque_project_create rejects a missing repo_path.
//   - edge 8: torque_task_create surfaces a dispatch_notice explaining the
//     forced manual=true state and how to promote.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- edge 8: task_create dispatch_notice -----------------------------------

func TestTaskCreateSurfacesManualDispatchNotice(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Fix auth bug",
		"description": "Login returns 500",
	})
	require.False(t, isErr, "task_create should succeed: %s", text)

	var data struct {
		ID             string `json:"ID"`
		Manual         bool   `json:"Manual"`
		DispatchNotice struct {
			Manual       bool   `json:"manual"`
			Dispatchable bool   `json:"dispatchable"`
			Message      string `json:"message"`
			PromoteWith  string `json:"promote_with"`
		} `json:"dispatch_notice"`
	}
	parseData(t, text, &data)

	// The safety override (CW-20260417-0133) forces manual=true.
	assert.True(t, data.Manual, "task_create must force manual=true")
	assert.True(t, data.DispatchNotice.Manual)
	assert.False(t, data.DispatchNotice.Dispatchable,
		"a manual task must not be reported as dispatchable")
	assert.Contains(t, data.DispatchNotice.Message, "manual=true")
	// The promote_with field must name the exact promotion call, with the
	// real task ID interpolated.
	require.NotEmpty(t, data.DispatchNotice.PromoteWith)
	assert.Contains(t, data.DispatchNotice.PromoteWith, "torque_task_update")
	assert.Contains(t, data.DispatchNotice.PromoteWith, data.ID)
	assert.Contains(t, data.DispatchNotice.PromoteWith, `"manual":false`)
}

// --- edge 7: project repo_path validation ----------------------------------

func TestProjectCreateRejectsMissingRepoPath(t *testing.T) {
	a := setupAdapterWithFeatures(t)

	text, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
		"name":      "Stale",
		"repo_path": "/tmp/torque-nonexistent-mcp-" + t.Name(),
	})
	require.True(t, isErr, "project_create with a missing repo_path must fail")

	code, message, field := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Equal(t, "repo_path", field)
	assert.Contains(t, message, "does not exist")
}

func TestProjectCreateAcceptsExistingRepoPath(t *testing.T) {
	a := setupAdapterWithFeatures(t)

	text, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
		"name":      "Healthy",
		"repo_path": t.TempDir(),
	})
	require.False(t, isErr, "project_create with an existing repo_path should succeed: %s", text)
}

// --- edge 5: torque_sprint_start -------------------------------------------

func TestSprintStartPromotesParkedTasks(t *testing.T) {
	a := setupAdapterWithFeatures(t)

	// Create a sprint in approve_sprint mode.
	sprintText, isErr := callTool(t, a, "torque_sprint_create", map[string]interface{}{
		"name":          "Sprint S",
		"approval_mode": "approve_sprint",
	})
	require.False(t, isErr, "sprint_create: %s", sprintText)
	var sprint struct {
		ID string `json:"ID"`
	}
	parseData(t, sprintText, &sprint)
	require.NotEmpty(t, sprint.ID)

	// Create two tasks in the sprint — both forced manual=true by the
	// CW-20260417-0133 safety override.
	for _, title := range []string{"Task one", "Task two"} {
		taskText, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
			"title":       title,
			"description": "body",
			"sprint_id":   sprint.ID,
		})
		require.False(t, isErr, "task_create: %s", taskText)
	}

	// Start the sprint — this is the missing "begin the sprint" action.
	startText, isErr := callTool(t, a, "torque_sprint_start", map[string]interface{}{
		"id": sprint.ID,
	})
	require.False(t, isErr, "sprint_start: %s", startText)

	var start struct {
		SprintID        string   `json:"sprint_id"`
		Promoted        int      `json:"promoted"`
		PromotedIDs     []string `json:"promoted_ids"`
		AlreadyEligible int      `json:"already_eligible"`
		Message         string   `json:"message"`
	}
	parseData(t, startText, &start)

	assert.Equal(t, sprint.ID, start.SprintID)
	assert.Equal(t, 2, start.Promoted, "both parked tasks should be promoted")
	assert.Len(t, start.PromotedIDs, 2)
	assert.Equal(t, 0, start.AlreadyEligible)
	assert.Contains(t, start.Message, "started")

	// A second call is idempotent: nothing left to promote.
	startText2, isErr := callTool(t, a, "torque_sprint_start", map[string]interface{}{
		"id": sprint.ID,
	})
	require.False(t, isErr, "sprint_start (2nd): %s", startText2)
	var start2 struct {
		Promoted        int `json:"promoted"`
		AlreadyEligible int `json:"already_eligible"`
	}
	parseData(t, startText2, &start2)
	assert.Equal(t, 0, start2.Promoted)
	assert.Equal(t, 2, start2.AlreadyEligible)
}

func TestSprintStartEmptySprint(t *testing.T) {
	a := setupAdapterWithFeatures(t)

	sprintText, isErr := callTool(t, a, "torque_sprint_create", map[string]interface{}{
		"name": "Empty Sprint",
	})
	require.False(t, isErr, "sprint_create: %s", sprintText)
	var sprint struct {
		ID string `json:"ID"`
	}
	parseData(t, sprintText, &sprint)

	startText, isErr := callTool(t, a, "torque_sprint_start", map[string]interface{}{
		"id": sprint.ID,
	})
	require.False(t, isErr, "sprint_start on empty sprint should still succeed: %s", startText)
	var start struct {
		Promoted int    `json:"promoted"`
		Message  string `json:"message"`
	}
	parseData(t, startText, &start)
	assert.Equal(t, 0, start.Promoted)
	assert.Contains(t, start.Message, "no tasks")
}
