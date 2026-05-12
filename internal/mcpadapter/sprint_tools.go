package mcpadapter

import (
	"context"
	"fmt"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerSprintTools() {
	a.addTool(mcp.NewTool("clockwork_sprint_create",
		mcp.WithDescription(`Create a sprint (feature-flagged: requires features.sprints). Returns the SprintRecord.
Use to scope a cohort of tasks under a common approval_mode + cost budget; prefer clockwork_epic_create for long-running multi-sprint initiatives, clockwork_project_create for infrastructure grouping.
Response shape: data = {<SprintRecord fields>} — singleton.
Example: {"name":"Sprint 17","goal":"Land Phase C","approval_mode":"approve_each","cost_budget":"50"}`),
		mcp.WithString("name", mcp.Required(), mcp.Description("Sprint name")),
		mcp.WithString("goal", mcp.Description("Sprint goal")),
		mcp.WithString("approval_mode", mcp.Description("auto|approve_sprint|approve_each (default approve_each)")),
		mcp.WithString("cost_budget", mcp.Description("Maximum cost budget (numeric)")),
		mcp.WithString("project_id", mcp.Description("Project ID to associate this sprint with (requires features.projects)")),
	), a.handleSprintCreate)

	a.addTool(mcp.NewTool("clockwork_sprint_get",
		mcp.WithDescription(`Fetch a sprint by ID plus derived budget headroom (within_budget, cost_remaining).
Use when you need the definition + live budget check; clockwork_sprint_list for browsing, clockwork_task_list with sprint_id filter for the sprint's tasks.
Response shape: data = {sprint: <SprintRecord>, within_budget: bool, cost_remaining: float}.
Example: {"id":"SP-17"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Sprint ID")),
	), a.handleSprintGet)

	a.addTool(mcp.NewTool("clockwork_sprint_update",
		mcp.WithDescription(`Partial update of sprint fields; pass status to transition (active<->inactive, either to completed terminal).
Use for field edits or lifecycle moves; sibling clockwork_sprint_approve handles task approvals.
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

	a.addTool(mcp.NewTool("clockwork_sprint_delete",
		mcp.WithDescription(`Hard-delete a sprint; tasks previously assigned have sprint_id cleared but are kept.
Use sparingly — prefer clockwork_sprint_update status=completed for audit. Similar surfaces: clockwork_project_delete, clockwork_epic_delete.
Response shape: data = {id, deleted: true, message}.
Example: {"id":"SP-17"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Sprint ID")),
	), a.handleSprintDelete)

	a.addTool(mcp.NewTool("clockwork_sprint_list",
		mcp.WithDescription(`List sprints, optionally filtered by status; ordered updated_at DESC.
Use for browsing; clockwork_sprint_get when you know the ID. Default brief shape drops goal body for size; pass verbose="true" for full records.
Response shape: data = {items: [<briefSprint or SprintRecord>...], meta: {truncated, returned, limit, hint?}}.
Example: {"status":"active"}`),
		mcp.WithString("status", mcp.Description("Filter: active|inactive|completed")),
		mcp.WithString("project_id", mcp.Description("Filter by project ID (requires features.projects)")),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
	), a.handleSprintList)

	a.addTool(mcp.NewTool("clockwork_sprint_approve",
		mcp.WithDescription(`Approve tasks in a sprint. With task_id, approves one task; without, approves every task currently in review.
Use for sprint-level review-gate closures; clockwork_task_transition for single-task control and clockwork_task_bulk_transition when approving outside a sprint.
Response shape: data = {sprint_id, task_id?, approved: count, message}.
Example: {"id":"SP-17"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Sprint ID")),
		mcp.WithString("task_id", mcp.Description("Specific task ID (omit for approve-all-in-review)")),
	), a.handleSprintApprove)
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
	sprints, err := a.svc.Sprint.List(reqStr(req, "status"), reqStr(req, "project_id"))
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
