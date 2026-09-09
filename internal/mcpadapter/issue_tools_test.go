package mcpadapter_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFullStack_IssueList_MergedListSearchFilters exercises the merged
// torque_issue_list tool: the former torque_issue_search's query behavior,
// the new status filter, and kind=issue scoping away from a same-title
// plain task (ADR-0004 §3 merge).
func TestFullStack_IssueList_MergedListSearchFilters(t *testing.T) {
	a := setupAdapterWithFeatures(t)
	projText, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
		"name": "Issue Project", "repo_path": t.TempDir(),
	})
	require.False(t, isErr, "project create should not error: %s", projText)
	var project map[string]interface{}
	parseData(t, projText, &project)
	projectID := project["ID"].(string)

	loginText, isErr := callTool(t, a, "torque_issue_create", map[string]interface{}{
		"title": "Login bug", "details": "Users see 500", "project_id": projectID,
	})
	require.False(t, isErr, "issue create should not error: %s", loginText)
	var login map[string]interface{}
	parseData(t, loginText, &login)

	rocketText, isErr := callTool(t, a, "torque_issue_create", map[string]interface{}{
		"title": "Rocket bug", "details": "Explodes on launch", "project_id": projectID,
	})
	require.False(t, isErr, "issue create should not error: %s", rocketText)
	var rocket map[string]interface{}
	parseData(t, rocketText, &rocket)

	// A same-term plain task must never leak into issue-scoped results
	// (kind=issue is hard-scoped, same as before the merge).
	_, isErr = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "Rocket task", "description": "Same term, different kind.", "project_id": projectID,
	})
	require.False(t, isErr)

	// Plain list, no query: both issues, project-scoped.
	listText, isErr := callTool(t, a, "torque_issue_list", map[string]interface{}{"project_id": projectID})
	require.False(t, isErr, "list should not error: %s", listText)
	var listed struct {
		Items []map[string]interface{} `json:"items"`
	}
	parseData(t, listText, &listed)
	require.Len(t, listed.Items, 2)

	// Merged query behavior (formerly torque_issue_search): "Rocket" must
	// match only the issue, not the same-term task.
	searchText, isErr := callTool(t, a, "torque_issue_list", map[string]interface{}{
		"project_id": projectID, "query": "Rocket",
	})
	require.False(t, isErr, "merged query should not error: %s", searchText)
	var found struct {
		Items []map[string]interface{} `json:"items"`
	}
	parseData(t, searchText, &found)
	require.Len(t, found.Items, 1)
	require.Equal(t, rocket["ID"], found.Items[0]["id"])

	// Force one issue out of backlog (its FSM start state has no
	// transitions defined — see torque_issue_bulk_transition's docstring)
	// so the new status filter has two distinct values to discriminate.
	loginID := login["ID"].(string)
	_, isErr = callTool(t, a, "torque_task_transition", map[string]interface{}{
		"id": loginID, "status": "todo", "force": true,
	})
	require.False(t, isErr, "force transition should not error")

	statusText, isErr := callTool(t, a, "torque_issue_list", map[string]interface{}{
		"project_id": projectID, "status": "todo",
	})
	require.False(t, isErr, "status filter should not error: %s", statusText)
	var byStatus struct {
		Items []map[string]interface{} `json:"items"`
	}
	parseData(t, statusText, &byStatus)
	require.Len(t, byStatus.Items, 1)
	require.Equal(t, login["ID"], byStatus.Items[0]["id"])

	backlogText, isErr := callTool(t, a, "torque_issue_list", map[string]interface{}{
		"project_id": projectID, "status": "backlog",
	})
	require.False(t, isErr)
	var byBacklog struct {
		Items []map[string]interface{} `json:"items"`
	}
	parseData(t, backlogText, &byBacklog)
	require.Len(t, byBacklog.Items, 1)
	require.Equal(t, rocket["ID"], byBacklog.Items[0]["id"])
}

// TestFullStack_IssueList_LimitPushedToDBAndCursorPaginates proves the
// pre-merge bug is fixed (IssueService.List never passed Limit to the DB,
// so torque_issue_list fetched every row then truncated in Go) by paging
// through a cursor with limit=1: a full-fetch-then-truncate implementation
// could never produce a working has_more/next_cursor round trip since it
// never set them at all.
func TestFullStack_IssueList_LimitPushedToDBAndCursorPaginates(t *testing.T) {
	a := setupAdapterWithFeatures(t)
	projText, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
		"name": "Issue Project", "repo_path": t.TempDir(),
	})
	require.False(t, isErr)
	var project map[string]interface{}
	parseData(t, projText, &project)
	projectID := project["ID"].(string)

	ids := make(map[string]bool)
	for i := 0; i < 3; i++ {
		text, isErr := callTool(t, a, "torque_issue_create", map[string]interface{}{
			"title": "Issue", "details": "x", "project_id": projectID,
		})
		require.False(t, isErr)
		var issue map[string]interface{}
		parseData(t, text, &issue)
		ids[issue["ID"].(string)] = true
	}

	seen := make(map[string]bool)
	cursor := ""
	for page := 0; page < 10; page++ {
		args := map[string]interface{}{
			"project_id": projectID, "limit": "1", "sort_by": "created_at", "sort_dir": "asc",
		}
		if cursor != "" {
			args["cursor"] = cursor
		}
		text, isErr := callTool(t, a, "torque_issue_list", args)
		require.False(t, isErr, "paginated list should not error: %s", text)

		var page struct {
			Items []map[string]interface{} `json:"items"`
			Meta  struct {
				Returned   int     `json:"returned"`
				Limit      int     `json:"limit"`
				HasMore    bool    `json:"has_more"`
				NextCursor *string `json:"next_cursor"`
			} `json:"meta"`
		}
		parseData(t, text, &page)
		require.LessOrEqual(t, page.Meta.Returned, 1, "DB-level limit=1 must cap each page, not a Go-side truncation of the full set")
		for _, item := range page.Items {
			seen[item["id"].(string)] = true
		}
		if !page.Meta.HasMore {
			require.Nil(t, page.Meta.NextCursor)
			break
		}
		require.NotNil(t, page.Meta.NextCursor)
		cursor = *page.Meta.NextCursor
	}
	require.Equal(t, ids, seen, "cursor pagination must visit every issue exactly once")
}

// TestFullStack_IssueBulkUpdate_PartialSuccess mirrors
// TestFullStack_TaskBulkUpdate_PartialSuccess: a bogus id and a non-issue
// task id both fail as distinct per-item errors (not_found vs arg_invalid
// from IssueService.Get's kind=issue scoping) while real issue ids succeed.
func TestFullStack_IssueBulkUpdate_PartialSuccess(t *testing.T) {
	a := setupAdapterWithFeatures(t)
	projText, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
		"name": "Issue Project", "repo_path": t.TempDir(),
	})
	require.False(t, isErr)
	var project map[string]interface{}
	parseData(t, projText, &project)
	projectID := project["ID"].(string)

	text, isErr := callTool(t, a, "torque_issue_create", map[string]interface{}{
		"title": "bulk-1", "details": "x", "project_id": projectID,
	})
	require.False(t, isErr)
	var issue1 map[string]interface{}
	parseData(t, text, &issue1)
	id1 := issue1["ID"].(string)

	text, isErr = callTool(t, a, "torque_issue_create", map[string]interface{}{
		"title": "bulk-2", "details": "x", "project_id": projectID,
	})
	require.False(t, isErr)
	var issue2 map[string]interface{}
	parseData(t, text, &issue2)
	id2 := issue2["ID"].(string)

	text, isErr = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "plain task", "description": "not an issue", "project_id": projectID,
	})
	require.False(t, isErr)
	var plainTask map[string]interface{}
	parseData(t, text, &plainTask)
	taskID := plainTask["ID"].(string)

	text, isErr = callTool(t, a, "torque_issue_bulk_update", map[string]interface{}{
		"ids":   `["` + id1 + `","CW-does-not-exist","` + taskID + `","` + id2 + `"]`,
		"title": "Renamed via bulk",
	})
	require.False(t, isErr, "bulk_update call itself must not be a call-level error: %s", text)

	var resp bulkResponse
	parseData(t, text, &resp)
	require.ElementsMatch(t, []string{id1, id2}, resp.Succeeded)
	require.Len(t, resp.Failed, 2)

	byID := map[string]string{}
	for _, f := range resp.Failed {
		byID[f.ID] = f.Error.Code
	}
	require.Equal(t, "not_found", byID["CW-does-not-exist"])
	require.Equal(t, "arg_invalid", byID[taskID], "a non-issue task id must fail via IssueService.Get's kind=issue scoping, not silently succeed")

	getText, isErr := callTool(t, a, "torque_issue_get", map[string]interface{}{"id": id1})
	require.False(t, isErr)
	var got1 map[string]interface{}
	parseData(t, getText, &got1)
	require.Equal(t, "Renamed via bulk", got1["Title"])
}

// TestFullStack_IssueBulkTransition_DelegatesToTaskFSM proves
// torque_issue_bulk_transition reuses TaskService.BulkTransition directly
// (ADR-0004 §3 / ENT-ISSUE "out of scope: any change to
// TaskService.BulkTransition itself"): an issue force-moved to todo can
// transition through the shared FSM, and a bogus id fails per-item without
// aborting the batch.
func TestFullStack_IssueBulkTransition_DelegatesToTaskStatusPolicy(t *testing.T) {
	a := setupAdapterWithFeatures(t)
	projText, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
		"name": "Issue Project", "repo_path": t.TempDir(),
	})
	require.False(t, isErr)
	var project map[string]interface{}
	parseData(t, projText, &project)
	projectID := project["ID"].(string)

	text, isErr := callTool(t, a, "torque_issue_create", map[string]interface{}{
		"title": "transition-me", "details": "x", "project_id": projectID,
	})
	require.False(t, isErr)
	var issue map[string]interface{}
	parseData(t, text, &issue)
	id := issue["ID"].(string)

	// Fresh issues start at status=backlog. This used to be a trap: backlog
	// was outside validTransitions, so Torque created rows its own FSM could
	// not move and the caller needed force=true to escape. Since
	// CW-20260909-0011 backlog is a canonical status like any other and
	// backlog -> todo just works.
	text, isErr = callTool(t, a, "torque_issue_bulk_transition", map[string]interface{}{
		"ids": `["` + id + `"]`, "status": "todo",
	})
	require.False(t, isErr, "bulk_transition call itself must not be a call-level error: %s", text)
	var fromBacklog bulkResponse
	parseData(t, text, &fromBacklog)
	require.Equal(t, []string{id}, fromBacklog.Succeeded, "backlog must no longer be a dead end")
	require.Empty(t, fromBacklog.Failed)

	text, isErr = callTool(t, a, "torque_issue_bulk_transition", map[string]interface{}{
		"ids": `["` + id + `","CW-does-not-exist"]`, "status": "doing",
	})
	require.False(t, isErr, "bulk_transition call itself must not be a call-level error: %s", text)
	var resp bulkResponse
	parseData(t, text, &resp)
	require.Equal(t, []string{id}, resp.Succeeded)
	require.Len(t, resp.Failed, 1)
	require.Equal(t, "CW-does-not-exist", resp.Failed[0].ID)

	getText, isErr := callTool(t, a, "torque_issue_get", map[string]interface{}{"id": id})
	require.False(t, isErr)
	var got map[string]interface{}
	parseData(t, getText, &got)
	require.Equal(t, "doing", got["Status"])
}

// TestFullStack_IssueSearchToolRemoved confirms torque_issue_search no
// longer exists as a separate tool — the merge into torque_issue_list
// (ADR-0004 §3) removes the old dual-tool redundancy entirely rather than
// keeping a deprecated alias, matching the ADR's Consequences section (no
// compat guarantee has been made to external callers yet).
func TestFullStack_IssueSearchToolRemoved(t *testing.T) {
	a := setupAdapterWithFeatures(t)
	require.False(t, toolIsRegistered(t, a, "torque_issue_search"), "torque_issue_search must be removed, merged into torque_issue_list")
	require.True(t, toolIsRegistered(t, a, "torque_issue_list"))
	require.True(t, toolIsRegistered(t, a, "torque_issue_bulk_update"))
	require.True(t, toolIsRegistered(t, a, "torque_issue_bulk_transition"))
	require.True(t, toolIsRegistered(t, a, "torque_issue_delete"))
}

// TestFullStack_IssueDelete covers the gap flagged against the ADR-0004
// target inventory: IssueService had no Delete method at all, so
// torque_issue_delete didn't exist. Deleting a real issue must remove it,
// and deleting a non-issue task id through the issue-scoped tool must be
// rejected (kind=issue scoping, same as get/update) rather than silently
// falling through to a plain task delete.
func TestFullStack_IssueDelete(t *testing.T) {
	a := setupAdapterWithFeatures(t)
	projText, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
		"name": "Issue Project", "repo_path": t.TempDir(),
	})
	require.False(t, isErr)
	var project map[string]interface{}
	parseData(t, projText, &project)
	projectID := project["ID"].(string)

	issueText, isErr := callTool(t, a, "torque_issue_create", map[string]interface{}{
		"title": "to be deleted", "details": "x", "project_id": projectID,
	})
	require.False(t, isErr)
	var issue map[string]interface{}
	parseData(t, issueText, &issue)
	issueID := issue["ID"].(string)

	delText, isErr := callTool(t, a, "torque_issue_delete", map[string]interface{}{"id": issueID})
	require.False(t, isErr, "delete should not error: %s", delText)
	var delResp struct {
		ID      string `json:"id"`
		Deleted bool   `json:"deleted"`
	}
	parseData(t, delText, &delResp)
	require.Equal(t, issueID, delResp.ID)
	require.True(t, delResp.Deleted)

	_, isErr = callTool(t, a, "torque_issue_get", map[string]interface{}{"id": issueID})
	require.True(t, isErr, "deleted issue must no longer be gettable")
}

func TestFullStack_IssueDelete_RejectsNonIssueTask(t *testing.T) {
	a := setupAdapterWithFeatures(t)
	projText, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
		"name": "Issue Project", "repo_path": t.TempDir(),
	})
	require.False(t, isErr)
	var project map[string]interface{}
	parseData(t, projText, &project)
	projectID := project["ID"].(string)

	taskText, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "plain task", "description": "not an issue", "project_id": projectID,
	})
	require.False(t, isErr)
	var task map[string]interface{}
	parseData(t, taskText, &task)
	taskID := task["ID"].(string)

	delText, isErr := callTool(t, a, "torque_issue_delete", map[string]interface{}{"id": taskID})
	require.True(t, isErr, "torque_issue_delete must reject a non-issue task id")
	code, _, field := parseError(t, delText)
	require.Equal(t, "arg_invalid", code)
	require.Equal(t, "kind", field)

	getText, isErr := callTool(t, a, "torque_task_get", map[string]interface{}{"id": taskID})
	require.False(t, isErr, "the task must still exist: %s", getText)
}

func TestFullStack_IssueDelete_NotFound(t *testing.T) {
	a := setupAdapterWithFeatures(t)
	_, isErr := callTool(t, a, "torque_issue_delete", map[string]interface{}{"id": "CW-nonexistent"})
	require.True(t, isErr)
}
