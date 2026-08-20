package mcpadapter

import (
	"context"
	"fmt"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/service/pagination"
	"github.com/mark3labs/mcp-go/mcp"
)

// sprintSortAllowList is torque_sprint_list's sort_by allow-list (PRIM-002),
// per pagination.ValidateSortBy's doc comment ("Epic/Sprint/Project: name,
// status, updated_at, created_at").
var sprintSortAllowList = []string{"name", "status", "updated_at", "created_at"}

// sprintSortDefaultBy/sprintSortDefaultDir are torque_sprint_list's default
// sort_by/sort_dir when the caller omits both — chosen to preserve the
// pre-existing `updated_at DESC` order (FIX-004's confirmed docstring/order
// match) as closely as the cursor mechanism allows.
const (
	sprintSortDefaultBy  = "updated_at"
	sprintSortDefaultDir = "desc"
)

func (a *Adapter) registerSprintTools() {
	a.addTool(mcp.NewTool("torque_sprint_create",
		mcp.WithDescription(`Create a sprint (feature-flagged: requires features.sprints). Returns the SprintRecord.
Use to scope a cohort of tasks under a common approval_mode + cost budget; prefer torque_epic_create for long-running multi-sprint initiatives, torque_project_create for infrastructure grouping. approval_mode=approve_sprint is a completion gate, not a kickoff action: start work by promoting the first sprint tasks to manual=false via torque_task_update, then let the scheduler dispatch them.
Response shape: data = {<SprintRecord fields>} — singleton.
Example: {"name":"Sprint 17","goal":"Land Phase C","approval_mode":"approve_each","cost_budget":"50"}`),
		mcp.WithString("name", mcp.Required(), mcp.Description("Sprint name")),
		mcp.WithString("goal", mcp.Description("Sprint goal")),
		mcp.WithString("approval_mode", mcp.Description("auto|approve_sprint|approve_each (default approve_each)")),
		mcp.WithString("cost_budget", mcp.Description("Maximum cost budget (numeric)")),
		mcp.WithString("project_id", mcp.Description("Project ID to associate this sprint with (requires features.projects)")),
	), a.handleSprintCreate)

	a.addTool(mcp.NewTool("torque_sprint_get",
		mcp.WithDescription(`Fetch a sprint by ID plus derived budget headroom (within_budget, cost_remaining).
Use when you need the definition + live budget check; torque_sprint_list for browsing, torque_task_list with sprint_id filter for the sprint's tasks.
Response shape: data = {sprint: <SprintRecord>, within_budget: bool, cost_remaining: float}.
Example: {"id":"SP-17"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Sprint ID")),
	), a.handleSprintGet)

	a.addTool(mcp.NewTool("torque_sprint_update",
		mcp.WithDescription(`Partial update of sprint fields; pass status to transition (active<->inactive, either to completed terminal).
Use for field edits or lifecycle moves; sibling torque_sprint_approve handles task approvals, torque_sprint_archive/_unarchive for soft-delete (orthogonal to status), torque_sprint_bulk_update for the same edit across many sprints at once.
Response shape: data = {id, updated: bool, message}.
Example: {"id":"SP-17","status":"completed"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Sprint ID")),
		mcp.WithString("name", mcp.Description("New name")),
		mcp.WithString("goal", mcp.Description("New goal")),
		mcp.WithString("approval_mode", mcp.Description("New approval mode")),
		mcp.WithString("cost_budget", mcp.Description("New cost budget (numeric)")),
		mcp.WithString("project_id", mcp.Description("Project ID to associate this sprint with (requires features.projects); pass empty string to clear")),
		mcp.WithString("status", mcp.Description("Transition target: active|inactive|completed")),
	), a.handleSprintUpdate)

	a.addTool(mcp.NewTool("torque_sprint_bulk_update",
		mcp.WithDescription(`Apply the same partial field update and/or status transition to many sprints in one call; per-sprint failures are collected, not fatal. Same field set and semantics as torque_sprint_update — only keys you actually pass change; status (if any) transitions each sprint through its own current-status FSM before the field update applies.
Use for batch field edits or lifecycle moves across a cohort of sprints (e.g. re-home several sprints to a project, or close out a batch); prefer torque_sprint_update for a single sprint.
Response shape: data = {succeeded: [id...], failed: [{id, error: {code, message, field}}...]} — partial success is not an error; ok=true even when some ids fail. error.code uses the same taxonomy (arg_invalid/not_found/conflict/domain/permission/internal) as single-item torque_sprint_update.
Example: {"ids":"[\"SP-17\",\"SP-18\"]","project_id":"PRJ-1"}`),
		mcp.WithString("ids", mcp.Required(), mcp.Description("JSON array of sprint IDs")),
		mcp.WithString("name", mcp.Description("New name")),
		mcp.WithString("goal", mcp.Description("New goal")),
		mcp.WithString("approval_mode", mcp.Description("New approval mode")),
		mcp.WithString("cost_budget", mcp.Description("New cost budget (numeric)")),
		mcp.WithString("project_id", mcp.Description("Project ID to associate these sprints with (requires features.projects); pass empty string to clear")),
		mcp.WithString("status", mcp.Description("Transition target applied to every id: active|inactive|completed")),
	), a.handleSprintBulkUpdate)

	a.addTool(mcp.NewTool("torque_sprint_delete",
		mcp.WithDescription(`Hard-delete a sprint; tasks previously assigned have sprint_id cleared but are kept.
Use sparingly — prefer torque_sprint_update status=completed for audit, or torque_sprint_archive to soft-delete while keeping the row (and its tasks' sprint_id links) intact. Similar surfaces: torque_project_delete, torque_epic_delete.
Response shape: data = {id, deleted: true, message}.
Example: {"id":"SP-17"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Sprint ID")),
	), a.handleSprintDelete)

	a.addTool(mcp.NewTool("torque_sprint_archive",
		mcp.WithDescription(`Archive a sprint (soft-delete; preserves audit trail and every task's sprint_id link). Independent of status — an archived sprint keeps whatever status it had.
Use instead of torque_sprint_delete when you want the row to stay around, just hidden from default torque_sprint_list results; torque_sprint_unarchive reverses it.
Response shape: data = {id, archived: true, message}.
Example: {"id":"SP-17"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Sprint ID")),
	), a.handleSprintArchive)

	a.addTool(mcp.NewTool("torque_sprint_unarchive",
		mcp.WithDescription(`Restore an archived sprint back to visible in default torque_sprint_list results. Status is untouched throughout — unarchiving never changes active/inactive/completed.
Use to reverse a torque_sprint_archive call; sibling torque_sprint_update handles ordinary field edits and lifecycle status moves.
Response shape: data = {id, unarchived: true, message}.
Example: {"id":"SP-17"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Sprint ID")),
	), a.handleSprintUnarchive)

	a.addTool(mcp.NewTool("torque_sprint_list",
		mcp.WithDescription(`List sprints with optional status/project/budget filters; ordered updated_at DESC (tiebreak id ASC, per DEC-001) by default. Pass sort_by (name|status|updated_at|created_at) and sort_dir (asc|desc) to change order; an unrecognized value returns error.code=arg_invalid.
Use for browsing; torque_sprint_get when you know the ID. Default brief shape drops goal body for size; pass verbose="true" for full records. Archived sprints are excluded unless include_archived="true".
Cursor pagination: pass the previous call's meta.next_cursor back as cursor to fetch the next page; meta.next_cursor is null once exhausted. A cursor is only valid for the exact sort_by/sort_dir it was issued under — pass a different sort_by/sort_dir without dropping cursor and you get error.code=arg_invalid.
Response shape: data = {items: [<briefSprint or SprintRecord>...], meta: {truncated, returned, limit, has_more, next_cursor}}.
Example: {"status":"active","cost_budget_min":"10","sort_by":"updated_at","sort_dir":"desc"}`),
		mcp.WithString("status", mcp.Description("Filter: active|inactive|completed")),
		mcp.WithString("project_id", mcp.Description("Filter by project ID (requires features.projects)")),
		mcp.WithString("include_archived", mcp.Description("Include archived sprints (string 'true'/'false', default false)")),
		mcp.WithString("cost_budget_min", mcp.Description("Only sprints with cost_budget >= this value (numeric; sprints with no budget set never match)")),
		mcp.WithString("cost_budget_max", mcp.Description("Only sprints with cost_budget <= this value (numeric; sprints with no budget set never match)")),
		mcp.WithString("over_budget", mcp.Description("Only sprints whose total run cost exceeds their cost_budget (string 'true'/'false', default false)")),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
		mcp.WithString("limit", mcp.Description("Max results (integer, default 100, max 500)")),
		mcp.WithString("sort_by", mcp.Description("Sort field: name|status|updated_at|created_at (default updated_at)")),
		mcp.WithString("sort_dir", mcp.Description("Sort direction: asc|desc (default desc)")),
		mcp.WithString("cursor", mcp.Description("Opaque pagination cursor from a previous call's meta.next_cursor; omit for the first page. Must match this call's sort_by/sort_dir.")),
	), a.handleSprintList)

	a.addTool(mcp.NewTool("torque_sprint_approve",
		mcp.WithDescription(`Approve tasks in a sprint. With task_id, approves one task; without, approves every task currently in review.
Use for sprint-level review-gate closures (CLOSING the cohort) after tasks have already run and reached review; this does NOT start dispatch. torque_sprint_start OPENS the dispatch gate. Confirm scheduler state with torque_scheduler_status. Use torque_task_transition for single-task control and torque_task_bulk_transition when approving outside a sprint.
Response shape: data = {sprint_id, task_id?, approved: count, message}.
Example: {"id":"SP-17"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Sprint ID")),
		mcp.WithString("task_id", mcp.Description("Specific task ID (omit for approve-all-in-review)")),
	), a.handleSprintApprove)

	a.addTool(mcp.NewTool("torque_sprint_start",
		mcp.WithDescription(`Open the dispatch gate for a sprint: promote every parked (manual=true) task in it to manual=false so the scheduler can begin dispatching them.
Use this to "start" a sprint under approval_mode=approve_sprint — torque_sprint_approve only CLOSES the review gate (review->done) and reports "0 tasks approved" on a fresh sprint. Mental model: Torque has ONE dispatch gate, the per-task manual flag; approval_mode is workflow metadata, not a scheduler gate. Starting a sprint = bulk-promoting its tasks. Idempotent: already-eligible tasks are left alone.
Response shape: data = {sprint_id, promoted: count, promoted_ids[], already_eligible: count, skipped[]?, message}.
Example: {"id":"SP-17"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Sprint ID")),
	), a.handleSprintStart)
}

func (a *Adapter) handleSprintCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	input := service.SprintCreateInput{
		Name:         reqStr(req, "name"),
		Goal:         reqStr(req, "goal"),
		ApprovalMode: reqStr(req, "approval_mode"),
		ProjectID:    reqStr(req, "project_id"),
	}

	if budget := reqFloat(req, "cost_budget"); budget > 0 {
		input.CostBudget = &budget
	}

	sprint, err := a.svc.Sprint.Create(input)
	if err != nil {
		return errFromService(err)
	}
	return okResult(sprint)
}

func (a *Adapter) handleSprintGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sprint, err := a.svc.Sprint.Get(reqStr(req, "id"))
	if err != nil {
		return errFromService(err)
	}

	withinBudget, remaining := a.svc.Sprint.CheckCostBudget(sprint.ID)
	return okResult(map[string]interface{}{
		"sprint":         sprint,
		"within_budget":  withinBudget,
		"cost_remaining": remaining,
	})
}

// buildSprintUpdate turns a torque_sprint_update/torque_sprint_bulk_update-
// shaped request's arguments into a sqlstore.SprintUpdate, plus whether any
// field was actually set (hasUpdate). Shared by the single-id and bulk
// handlers (PRIM-003) so their field semantics can never drift apart.
//
// Field-detection semantics are preserved exactly as they were before this
// helper was extracted: name/goal/approval_mode/cost_budget are value-based
// (a zero value means "not provided" — cost_budget in particular can't be
// cleared to 0 this way), project_id is presence-based via reqHasArg (an
// explicit empty string clears it). This mismatch predates ENT-SPRINT and
// mirrors Task's pre-FIX-001 behavior; fixing it is out of this task's scope
// (FIX-001 was Task-specific).
func buildSprintUpdate(req mcp.CallToolRequest) (sqlstore.SprintUpdate, bool) {
	update := sqlstore.SprintUpdate{}
	hasUpdate := false

	if v := reqStr(req, "name"); v != "" {
		update.Name = &v
		hasUpdate = true
	}
	if v := reqStr(req, "goal"); v != "" {
		update.Goal = &v
		hasUpdate = true
	}
	if v := reqStr(req, "approval_mode"); v != "" {
		update.ApprovalMode = &v
		hasUpdate = true
	}
	if v := reqFloat(req, "cost_budget"); v > 0 {
		update.CostBudget = &v
		hasUpdate = true
	}
	if reqHasArg(req, "project_id") {
		v := reqStr(req, "project_id")
		update.ProjectID = &v
		hasUpdate = true
	}
	return update, hasUpdate
}

func (a *Adapter) handleSprintUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")

	// Handle status transition separately
	if status := reqStr(req, "status"); status != "" {
		if err := a.svc.Sprint.Transition(id, status); err != nil {
			return errFromService(err)
		}
	}

	update, hasUpdate := buildSprintUpdate(req)
	if hasUpdate {
		if err := a.svc.Sprint.Update(id, update); err != nil {
			return errFromService(err)
		}
	}

	return okResult(map[string]any{
		"id":      id,
		"updated": true,
		"message": fmt.Sprintf("Sprint %s updated", id),
	})
}

// handleSprintBulkUpdate is torque_sprint_bulk_update's handler (PRIM-003).
// Builds the shared field update once via buildSprintUpdate and applies it
// (plus an optional status transition) to every id through
// SprintService.BulkUpdate, then shapes the result through bulkResult — the
// same {succeeded, failed} envelope every bulk_* verb uses.
func (a *Adapter) handleSprintBulkUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ids, errRes := reqIDs(req)
	if errRes != nil {
		return errRes, nil
	}

	update, _ := buildSprintUpdate(req)
	status := reqStr(req, "status")

	succeeded, failed := a.svc.Sprint.BulkUpdate(ids, update, status)
	return bulkResult(succeeded, failed)
}

func (a *Adapter) handleSprintDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")
	if err := a.svc.Sprint.Delete(id); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"id":      id,
		"deleted": true,
		"message": fmt.Sprintf("Sprint %s deleted", id),
	})
}

func (a *Adapter) handleSprintList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := clampLimit(reqInt(req, "limit"), defaultGenericListLimit, maxGenericListLimit)
	verbose := reqStrBool(req, "verbose")

	// PRIM-002: sort_by/sort_dir, allow-list validated. Omitted values fall
	// back to the pre-existing `updated_at DESC` default order (FIX-004's
	// confirmed docstring/order match) — see sprintSortDefaultBy/Dir.
	sortBy := sprintSortDefaultBy
	if raw := reqStr(req, "sort_by"); raw != "" {
		v, err := pagination.ValidateSortBy(raw, sprintSortAllowList...)
		if err != nil {
			return errResult(ErrCodeArgInvalid, err.Error(), "sort_by")
		}
		sortBy = v
	}
	sortDir := sprintSortDefaultDir
	if raw := reqStr(req, "sort_dir"); raw != "" {
		v, err := pagination.ValidateSortDir(raw)
		if err != nil {
			return errResult(ErrCodeArgInvalid, err.Error(), "sort_dir")
		}
		sortDir = v
	}

	// PRIM-001: decode + validate the incoming cursor, if any, against this
	// request's (now-resolved) sort_by/sort_dir. DEC-001: cursors aren't
	// portable across sort orders.
	var afterSortValue, afterID string
	if raw := reqStr(req, "cursor"); raw != "" {
		c, err := pagination.Decode(raw)
		if err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid cursor: %v", err), "cursor")
		}
		if err := c.Validate(sortBy, sortDir); err != nil {
			return errResult(ErrCodeArgInvalid, err.Error(), "cursor")
		}
		afterSortValue, afterID = c.SortValue, c.ID
	}

	filter := sqlstore.SprintFilter{
		Status:          reqStr(req, "status"),
		ProjectID:       reqStr(req, "project_id"),
		IncludeArchived: reqStrBool(req, "include_archived"),
		OverBudget:      reqStrBool(req, "over_budget"),
		// Fetch one extra row beyond limit so has_more can be determined
		// without a separate COUNT(*) query (DEC-001's cheaper-default
		// choice). Trimmed back to limit below before building the envelope.
		Limit:          limit + 1,
		SortBy:         sortBy,
		SortDir:        sortDir,
		AfterSortValue: afterSortValue,
		AfterID:        afterID,
	}
	if reqHasArg(req, "cost_budget_min") {
		v := reqFloat(req, "cost_budget_min")
		filter.CostBudgetMin = &v
	}
	if reqHasArg(req, "cost_budget_max") {
		v := reqFloat(req, "cost_budget_max")
		filter.CostBudgetMax = &v
	}

	sprints, err := a.svc.Sprint.List(filter)
	if err != nil {
		return errFromService(err)
	}

	hasMoreFromQuery := len(sprints) > limit
	if hasMoreFromQuery {
		sprints = sprints[:limit]
	}
	return a.sprintListCursorEnvelope(sprints, limit, verbose, sortBy, sortDir, hasMoreFromQuery)
}

// sprintSortValue formats a SprintRecord's sortBy column into the string
// encoding PRIM-001's cursor uses for meta.next_cursor (DEC-001's `sv`
// field). Mirrors taskSortValue's contract in task_tools.go.
func sprintSortValue(sp sqlstore.SprintRecord, sortBy string) string {
	switch sortBy {
	case "name":
		return sp.Name
	case "status":
		return sp.Status
	case "updated_at":
		return sp.UpdatedAt.UTC().Format(sqlstore.SQLiteDatetimeLayout)
	case "created_at":
		return sp.CreatedAt.UTC().Format(sqlstore.SQLiteDatetimeLayout)
	default:
		return ""
	}
}

// sprintListCursorEnvelope builds torque_sprint_list's {items, meta} cursor-
// pagination response, mirroring taskListCursorEnvelope's contract. sprints
// must already be trimmed to at most `limit` records.
func (a *Adapter) sprintListCursorEnvelope(sprints []sqlstore.SprintRecord, limit int, verbose bool, sortBy, sortDir string, hasMoreFromQuery bool) (*mcp.CallToolResult, error) {
	items := make([]any, 0, len(sprints))
	for _, sp := range sprints {
		if verbose {
			items = append(items, sp)
		} else {
			items = append(items, toBriefSprint(sp))
		}
	}

	cursorAt := func(i int) (sortValue, id string) {
		return sprintSortValue(sprints[i], sortBy), sprints[i].ID
	}
	return cappedCursorJSONResult(items, limit, sortBy, sortDir, hasMoreFromQuery, cursorAt)
}

// handleSprintArchive/handleSprintUnarchive wire PRIM-004's archive
// primitive (SprintService.Archive/Unarchive, already built) onto the MCP
// surface, following torque_collection_archive/_unarchive's reference
// response shape (collection_tools.go).
func (a *Adapter) handleSprintArchive(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")
	if err := a.svc.Sprint.Archive(id); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"id":       id,
		"archived": true,
		"message":  fmt.Sprintf("Sprint %s archived", id),
	})
}

func (a *Adapter) handleSprintUnarchive(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")
	if err := a.svc.Sprint.Unarchive(id); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"id":         id,
		"unarchived": true,
		"message":    fmt.Sprintf("Sprint %s unarchived", id),
	})
}

func (a *Adapter) handleSprintApprove(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sprintID := reqStr(req, "id")
	taskID := reqStr(req, "task_id")

	if taskID != "" {
		if err := a.svc.Sprint.ApproveTask(sprintID, taskID); err != nil {
			return errFromService(err)
		}
		return okResult(map[string]any{
			"sprint_id": sprintID,
			"task_id":   taskID,
			"approved":  1,
			"message":   fmt.Sprintf("Task %s approved in sprint %s", taskID, sprintID),
		})
	}

	count, err := a.svc.Sprint.ApproveAll(sprintID)
	if err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"sprint_id": sprintID,
		"approved":  count,
		"message":   fmt.Sprintf("%d tasks approved in sprint %s", count, sprintID),
	})
}

// handleSprintStart opens a sprint's dispatch gate. See SprintService.Start
// and the torque_sprint_start description for the mental model: this is the
// missing "begin the sprint" action for approval_mode=approve_sprint
// (CW-20260517-0011 edge 5).
func (a *Adapter) handleSprintStart(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sprintID := reqStr(req, "id")

	res, err := a.svc.Sprint.Start(sprintID)
	if err != nil {
		return errFromService(err)
	}

	promotedIDs := res.PromotedIDs
	if promotedIDs == nil {
		promotedIDs = []string{}
	}

	var message string
	switch {
	case res.Promoted > 0:
		message = fmt.Sprintf("Sprint %s started: promoted %d task(s) to manual=false — the scheduler will now dispatch them in priority order as dependencies clear. %d task(s) were already dispatch-eligible.",
			sprintID, res.Promoted, res.AlreadyEligible)
	case res.AlreadyEligible > 0:
		message = fmt.Sprintf("Sprint %s already started: all %d task(s) are dispatch-eligible (manual=false). No changes made.",
			sprintID, res.AlreadyEligible)
	default:
		message = fmt.Sprintf("Sprint %s has no tasks to start — add tasks with sprint_id=%s, then call torque_sprint_start again. (Reminder: torque_task_create force-sets manual=true; this tool clears it for the whole cohort.)",
			sprintID, sprintID)
	}

	data := map[string]any{
		"sprint_id":        sprintID,
		"promoted":         res.Promoted,
		"promoted_ids":     promotedIDs,
		"already_eligible": res.AlreadyEligible,
		"message":          message,
	}
	if len(res.Skipped) > 0 {
		data["skipped"] = res.Skipped
	}
	return okResult(data)
}
