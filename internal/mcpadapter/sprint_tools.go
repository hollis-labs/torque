package mcpadapter

import (
	"context"
	"fmt"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
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
Use for field edits or lifecycle moves; sibling torque_sprint_approve handles task approvals.
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

	a.addTool(mcp.NewTool("torque_sprint_delete",
		mcp.WithDescription(`Hard-delete a sprint; tasks previously assigned have sprint_id cleared but are kept.
Use sparingly — prefer torque_sprint_update status=completed for audit. Similar surfaces: torque_project_delete, torque_epic_delete.
Response shape: data = {id, deleted: true, message}.
Example: {"id":"SP-17"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Sprint ID")),
	), a.handleSprintDelete)

	a.addTool(mcp.NewTool("torque_sprint_list",
		mcp.WithDescription(`List sprints, optionally filtered by status; ordered updated_at DESC.
Use for browsing; torque_sprint_get when you know the ID. Default brief shape drops goal body for size; pass verbose="true" for full records.
Response shape: data = {items: [<briefSprint or SprintRecord>...], meta: {truncated, returned, limit, hint?}}.
Example: {"status":"active"}`),
		mcp.WithString("status", mcp.Description("Filter: active|inactive|completed")),
		mcp.WithString("project_id", mcp.Description("Filter by project ID (requires features.projects)")),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
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

func (a *Adapter) handleSprintUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")

	// Handle status transition separately
	if status := reqStr(req, "status"); status != "" {
		if err := a.svc.Sprint.Transition(id, status); err != nil {
			return errFromService(err)
		}
	}

	// Handle field updates
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
	verbose := reqStrBool(req, "verbose")
	// Wiring an include_archived param into this tool's schema is Phase 4's
	// job (PRIM-004 scope note); default to excluding archived rows here,
	// which is a no-op today since nothing can set archived_at yet.
	sprints, err := a.svc.Sprint.List(reqStr(req, "status"), reqStr(req, "project_id"), false)
	if err != nil {
		return errFromService(err)
	}
	limit := defaultGenericListLimit
	items := make([]any, 0, len(sprints))
	for _, s := range sprints {
		if verbose {
			items = append(items, s)
		} else {
			items = append(items, toBriefSprint(s))
		}
	}
	return cappedJSONResult(items, limit)
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
