package mcpadapter

import (
	"context"
	"fmt"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerSprintTools() {
	a.server.AddTool(mcp.NewTool("clockwork_sprint_create",
		mcp.WithDescription("Create a new sprint (requires features.sprints = true)"),
		mcp.WithString("name", mcp.Required(), mcp.Description("Sprint name")),
		mcp.WithString("goal", mcp.Description("Sprint goal")),
		mcp.WithString("approval_mode", mcp.Description("Approval mode: auto, approve_sprint, approve_each (default: approve_each)")),
		mcp.WithString("cost_budget", mcp.Description("Maximum cost budget for the sprint (numeric)")),
	), a.handleSprintCreate)

	a.server.AddTool(mcp.NewTool("clockwork_sprint_get",
		mcp.WithDescription("Get a sprint by ID"),
		mcp.WithString("id", mcp.Required(), mcp.Description("Sprint ID")),
	), a.handleSprintGet)

	a.server.AddTool(mcp.NewTool("clockwork_sprint_update",
		mcp.WithDescription("Update sprint fields"),
		mcp.WithString("id", mcp.Required(), mcp.Description("Sprint ID")),
		mcp.WithString("name", mcp.Description("New name")),
		mcp.WithString("goal", mcp.Description("New goal")),
		mcp.WithString("approval_mode", mcp.Description("New approval mode")),
		mcp.WithString("cost_budget", mcp.Description("New cost budget (numeric)")),
		mcp.WithString("status", mcp.Description("Transition to new status (active|inactive|completed). active↔inactive; both can transition directly to completed (terminal)")),
	), a.handleSprintUpdate)

	a.server.AddTool(mcp.NewTool("clockwork_sprint_delete",
		mcp.WithDescription("Delete a sprint"),
		mcp.WithString("id", mcp.Required(), mcp.Description("Sprint ID")),
	), a.handleSprintDelete)

	a.server.AddTool(mcp.NewTool("clockwork_sprint_list",
		mcp.WithDescription("List sprints"),
		mcp.WithString("status", mcp.Description("Filter by status: active, inactive, completed")),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
	), a.handleSprintList)

	a.server.AddTool(mcp.NewTool("clockwork_sprint_approve",
		mcp.WithDescription("Approve tasks in a sprint. With task_id, approves one task. Without, approves all tasks in review."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Sprint ID")),
		mcp.WithString("task_id", mcp.Description("Specific task ID to approve (omit for approve-all)")),
	), a.handleSprintApprove)
}

func (a *Adapter) handleSprintCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	input := service.SprintCreateInput{
		Name:         reqStr(req, "name"),
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
