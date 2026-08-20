package mcpadapter_test

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/service"

	_ "modernc.org/sqlite"
)

// planDetailTask is the subset of PlanDetail.Task (a *sqlstore.TaskRecord,
// which has no json tags so fields serialize with Go's default
// capitalization) the plan tool tests below need.
type planDetailTask struct {
	ID          string `json:"ID"`
	Title       string `json:"Title"`
	Description string `json:"Description"`
	Kind        string `json:"Kind"`
	Priority    int    `json:"Priority"`
}

type planDetailEnvelope struct {
	Task planDetailTask `json:"task"`
}

// createPlan is a test helper that creates a plan via torque_plan_create and
// returns its assigned task ID.
func createPlan(t *testing.T, a *mcpadapter.Adapter, title string) string {
	t.Helper()
	text, isErr := callTool(t, a, "torque_plan_create", map[string]interface{}{
		"title": title,
	})
	require.False(t, isErr, "plan create should not error: %s", text)
	var detail planDetailEnvelope
	parseData(t, text, &detail)
	require.NotEmpty(t, detail.Task.ID)
	return detail.Task.ID
}

// TestPlanStart_NoSessionsWired_ReturnsDomainError is the regression test for
// the legacy stdio-mcp behavior — without WithSessions wired, plan_start
// surfaces ErrSessionMgrMissing instead of attempting to boot.
func TestPlanStart_NoSessionsWired_ReturnsDomainError(t *testing.T) {
	a := setupAdapter(t) // mcpadapter.New(svc, nil) without .WithSessions

	text, isErr := callTool(t, a, "torque_plan_start", map[string]interface{}{
		"plan_id": "CW-DOES-NOT-MATTER",
	})

	assert.True(t, isErr, "MCP result.isError must be set for the dual-surface error contract")
	code, msg, _ := parseError(t, text)
	assert.Equal(t, "domain", code)
	assert.Contains(t, msg, "session manager not configured",
		"plan_start without WithSessions must surface ErrSessionMgrMissing")
}

// TestPlanStart_SessionsWired_PassesNilCheck is the CW-20260509-0013
// regression test — wiring agent.Manager into the stdio adapter via
// .WithSessions makes plan_start route through to planstart.Start, which
// then errors on the (intentionally bogus) plan_id with ErrPlanNotFound.
// The point is to prove we got PAST the ErrSessionMgrMissing nil check —
// the specific downstream error is incidental.
func TestPlanStart_SessionsWired_PassesNilCheck(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	svc := service.New(store)
	deps := &agent.Dependencies{Store: store}
	deps.Sessions = agent.NewManager(deps)

	a := mcpadapter.New(svc, nil).WithSessions(deps.Sessions)

	text, isErr := callTool(t, a, "torque_plan_start", map[string]interface{}{
		"plan_id": "CW-DOES-NOT-EXIST-IN-DB",
	})

	assert.True(t, isErr, "MCP result.isError must be set for the dual-surface error contract")
	code, msg, _ := parseError(t, text)
	// We expect arg_invalid (ErrPlanNotFound), NOT domain (ErrSessionMgrMissing).
	assert.Equal(t, "arg_invalid", code,
		"plan_start with sessions wired must NOT return ErrSessionMgrMissing — got code=%q msg=%q", code, msg)
	assert.NotContains(t, msg, "session manager not configured",
		"with sessions wired, the error must be plan-related, not session-manager-related")
}

// TestPlanStart_SessionsWired_MissingPlanID is the input-validation test —
// after sessions are wired, the explicit empty-plan_id arg check still
// surfaces the dedicated arg_invalid error (not ErrSessionMgrMissing).
func TestPlanStart_SessionsWired_MissingPlanID(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	svc := service.New(store)
	deps := &agent.Dependencies{Store: store}
	deps.Sessions = agent.NewManager(deps)

	a := mcpadapter.New(svc, nil).WithSessions(deps.Sessions)

	text, isErr := callTool(t, a, "torque_plan_start", map[string]interface{}{
		// no plan_id
	})

	assert.True(t, isErr, "MCP result.isError must be set for the dual-surface error contract")
	code, msg, field := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Equal(t, "plan_id", field)
	assert.Contains(t, msg, "plan_id is required")
}

// TestFullStack_PlanList_KindScoped is ENT-PLAN's acceptance criterion:
// torque_plan_list exists as a dedicated tool (not just
// torque_task_list kind=plan) and is hard-scoped to kind=plan — a sibling
// non-plan task must never appear in its results.
func TestFullStack_PlanList_KindScoped(t *testing.T) {
	a := setupAdapter(t)

	planID := createPlan(t, a, "the plan")
	_, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "an ordinary task",
		"description": "x",
	})
	require.False(t, isErr)

	text, isErr := callTool(t, a, "torque_plan_list", map[string]interface{}{})
	require.False(t, isErr, "list should not error: %s", text)
	var env taskListCursorEnvelope
	parseData(t, text, &env)

	require.Len(t, env.Items, 1, "only the plan should appear, never the sibling ordinary task")
	assert.Equal(t, planID, env.Items[0]["id"])
	assert.Equal(t, "plan", env.Items[0]["kind"])
}

// TestFullStack_PlanList_CursorPaginationAndSort exercises PRIM-001/PRIM-002
// end to end on the dedicated plan list tool: paging via meta.next_cursor
// visits every plan exactly once, and has_more/next_cursor accurately
// reflect exhaustion — the same acceptance bar task_tools_test.go's
// TestFullStack_TaskList_* cursor tests hold torque_task_list to.
func TestFullStack_PlanList_CursorPaginationAndSort(t *testing.T) {
	a := setupAdapter(t)

	const total = 5
	created := make(map[string]bool, total)
	for i := 0; i < total; i++ {
		id := createPlan(t, a, fmt.Sprintf("plan %d", i))
		created[id] = true
	}

	seen := map[string]bool{}
	cursor := ""
	for pages := 0; ; pages++ {
		require.LessOrEqual(t, pages, total, "too many pages — likely an infinite loop from a broken cursor")

		args := map[string]interface{}{"limit": "2"}
		if cursor != "" {
			args["cursor"] = cursor
		}
		text, isErr := callTool(t, a, "torque_plan_list", args)
		require.False(t, isErr, "list should not error: %s", text)

		var env taskListCursorEnvelope
		parseData(t, text, &env)
		for _, item := range env.Items {
			id := item["id"].(string)
			require.False(t, seen[id], "duplicate id %s seen across pages", id)
			seen[id] = true
		}

		if !env.Meta.HasMore {
			require.Nil(t, env.Meta.NextCursor, "next_cursor must be null once exhausted")
			break
		}
		require.NotNil(t, env.Meta.NextCursor)
		cursor = *env.Meta.NextCursor
	}
	require.Len(t, seen, total)
	for id := range created {
		require.True(t, seen[id], "plan %s missing from paged results", id)
	}
}

// TestFullStack_PlanList_InvalidSortBy mirrors torque_task_list's
// PRIM-002 acceptance criterion for the dedicated plan list tool: an
// unrecognized sort_by is a clean error.code=arg_invalid.
func TestFullStack_PlanList_InvalidSortBy(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_plan_list", map[string]interface{}{
		"sort_by": "not_a_real_field",
	})
	require.True(t, isErr, "list should error on invalid sort_by: %s", text)
	code, _, field := parseError(t, text)
	require.Equal(t, "arg_invalid", code)
	require.Equal(t, "sort_by", field)
}

// TestFullStack_PlanUpdate_HappyPath verifies torque_plan_update edits a
// plan's own fields and returns the refreshed PlanDetail.
func TestFullStack_PlanUpdate_HappyPath(t *testing.T) {
	a := setupAdapter(t)
	planID := createPlan(t, a, "original title")

	text, isErr := callTool(t, a, "torque_plan_update", map[string]interface{}{
		"plan_id":     planID,
		"title":       "updated title",
		"description": "updated description",
	})
	require.False(t, isErr, "update should not error: %s", text)
	var detail planDetailEnvelope
	parseData(t, text, &detail)
	assert.Equal(t, "updated title", detail.Task.Title)
	assert.Equal(t, "updated description", detail.Task.Description)

	// Persisted, not just echoed.
	text, isErr = callTool(t, a, "torque_plan_get", map[string]interface{}{"plan_id": planID})
	require.False(t, isErr)
	var fetched planDetailEnvelope
	parseData(t, text, &fetched)
	assert.Equal(t, "updated title", fetched.Task.Title)
}

// TestFullStack_PlanUpdate_RejectsNonPlanID is ENT-PLAN's kind-guard
// acceptance criterion: torque_plan_update must reject a non-plan task id
// rather than silently editing it (which the generic torque_task_update
// would happily do with no kind check).
func TestFullStack_PlanUpdate_RejectsNonPlanID(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "ordinary task",
		"description": "x",
	})
	require.False(t, isErr)
	var created map[string]interface{}
	parseData(t, text, &created)
	taskID := created["ID"].(string)

	text, isErr = callTool(t, a, "torque_plan_update", map[string]interface{}{
		"plan_id": taskID,
		"title":   "should not apply",
	})
	require.True(t, isErr, "update against a non-plan id must error: %s", text)
	code, msg, field := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Equal(t, "kind", field)
	assert.Contains(t, msg, "not a plan")

	// Confirm the title truly did not change.
	text, isErr = callTool(t, a, "torque_task_get", map[string]interface{}{"id": taskID})
	require.False(t, isErr)
	var fetched map[string]interface{}
	parseData(t, text, &fetched)
	assert.Equal(t, "ordinary task", fetched["Title"])
}

// TestFullStack_PlanDelete_HappyPath verifies torque_plan_delete removes
// the plan task row.
func TestFullStack_PlanDelete_HappyPath(t *testing.T) {
	a := setupAdapter(t)
	planID := createPlan(t, a, "to be deleted")

	text, isErr := callTool(t, a, "torque_plan_delete", map[string]interface{}{"plan_id": planID})
	require.False(t, isErr, "delete should not error: %s", text)
	var deleted map[string]interface{}
	parseData(t, text, &deleted)
	assert.Equal(t, planID, deleted["id"])
	assert.Equal(t, true, deleted["deleted"])

	text, isErr = callTool(t, a, "torque_plan_get", map[string]interface{}{"plan_id": planID})
	require.True(t, isErr, "get after delete should error: %s", text)
	code, _, _ := parseError(t, text)
	assert.Equal(t, "not_found", code)
}

// TestFullStack_PlanDelete_RejectsNonPlanID mirrors
// TestFullStack_PlanUpdate_RejectsNonPlanID for delete: a non-plan task id
// must be rejected, not hard-deleted.
func TestFullStack_PlanDelete_RejectsNonPlanID(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "ordinary task",
		"description": "x",
	})
	require.False(t, isErr)
	var created map[string]interface{}
	parseData(t, text, &created)
	taskID := created["ID"].(string)

	text, isErr = callTool(t, a, "torque_plan_delete", map[string]interface{}{"plan_id": taskID})
	require.True(t, isErr, "delete against a non-plan id must error: %s", text)
	code, msg, field := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Equal(t, "kind", field)
	assert.Contains(t, msg, "not a plan")

	// Confirm the task still exists.
	text, isErr = callTool(t, a, "torque_task_get", map[string]interface{}{"id": taskID})
	require.False(t, isErr, "ordinary task must survive the rejected plan_delete call: %s", text)
}

// TestFullStack_PlanListChildren_LimitAdjustable is ENT-PLAN's list_children
// acceptance criterion: limit is a real caller-adjustable param (previously
// hardcoded to maxTaskListLimit with no truncation actually applied — every
// call silently returned the full unbounded child set).
func TestFullStack_PlanListChildren_LimitAdjustable(t *testing.T) {
	a := setupAdapter(t)
	planID := createPlan(t, a, "plan with many children")

	const total = 5
	for i := 0; i < total; i++ {
		text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
			"title":       fmt.Sprintf("child %d", i),
			"description": "x",
			"parent_id":   planID,
		})
		require.False(t, isErr, "child create should not error: %s", text)
	}

	// Explicit small limit is honored — both meta.limit AND the actual
	// items count returned.
	text, isErr := callTool(t, a, "torque_plan_list_children", map[string]interface{}{
		"plan_id": planID,
		"limit":   "2",
	})
	require.False(t, isErr, "list_children should not error: %s", text)
	var env struct {
		Items []map[string]interface{} `json:"items"`
		Meta  struct {
			Limit    int `json:"limit"`
			Returned int `json:"returned"`
		} `json:"meta"`
	}
	parseData(t, text, &env)
	assert.Equal(t, 2, env.Meta.Limit, "meta.limit should reflect the caller-supplied limit")
	assert.Len(t, env.Items, 2, "items must actually be truncated to limit, not just mislabeled")

	// Default limit (no limit param) is the sane generic default (100), not
	// the system's 200 maximum.
	text, isErr = callTool(t, a, "torque_plan_list_children", map[string]interface{}{
		"plan_id": planID,
	})
	require.False(t, isErr, "list_children should not error: %s", text)
	parseData(t, text, &env)
	assert.Equal(t, 100, env.Meta.Limit, "default limit should be the generic list default (100), not the 200 system maximum")
	assert.Len(t, env.Items, total, "all 5 children fit comfortably under the default limit")
}

// TestFullStack_PlanRemovePhase_BlockedByChildren_IsConflict is the
// regression test for the RemovePhase bug found while verifying add_phase/
// remove_phase's current state (ENT-PLAN scope item 4): the tool's own
// docstring promises error.code=conflict when a child task still
// references the phase, but PlanService.RemovePhase previously returned a
// *service.ValidationError (-> arg_invalid), breaking that documented
// contract. Fixed to return *service.ConflictError (-> conflict).
func TestFullStack_PlanRemovePhase_BlockedByChildren_IsConflict(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_plan_create", map[string]interface{}{
		"title":  "phased plan",
		"phases": `[{"name":"A"}]`,
	})
	require.False(t, isErr, "create should not error: %s", text)
	var detail planDetailEnvelope
	parseData(t, text, &detail)
	planID := detail.Task.ID

	_, isErr = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "child of phase A",
		"description": "x",
		"parent_id":   planID,
		"metadata":    `{"phase_id":"ph-1"}`,
	})
	require.False(t, isErr)

	text, isErr = callTool(t, a, "torque_plan_remove_phase", map[string]interface{}{
		"plan_id":  planID,
		"phase_id": "ph-1",
	})
	require.True(t, isErr, "remove_phase should error while a child still references the phase: %s", text)
	code, msg, _ := parseError(t, text)
	assert.Equal(t, "conflict", code, "must match torque_plan_remove_phase's documented error.code=conflict contract")
	assert.Contains(t, msg, "still reference")
}
