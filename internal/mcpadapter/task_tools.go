package mcpadapter

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerTaskTools() {
	a.server.AddTool(mcp.NewTool("clockwork_task_create",
		mcp.WithDescription("Create a new task"),
		mcp.WithString("title", mcp.Required(), mcp.Description("Task title")),
		mcp.WithString("description", mcp.Required(), mcp.Description("Task description")),
		mcp.WithNumber("priority", mcp.Description("Priority 1-5 (default 2)")),
		mcp.WithString("tags", mcp.Description("JSON array of tag strings")),
		mcp.WithString("executor", mcp.Description("Executor type (default cli)")),
		mcp.WithString("agent_profile", mcp.Description("Agent profile name")),
		mcp.WithString("working_dir", mcp.Description("Working directory")),
		mcp.WithString("system_prompt", mcp.Description("System prompt override")),
		mcp.WithString("on_done", mcp.Description("Hook on done")),
		mcp.WithString("on_fail", mcp.Description("Hook on fail")),
		mcp.WithString("on_done_merge", mcp.Description("Merge hook on done")),
		mcp.WithString("depends_on", mcp.Description("JSON array of dependency task IDs")),
		mcp.WithBoolean("manual", mcp.Description("Whether the task is manual")),
		mcp.WithString("sprint_id", mcp.Description("Sprint ID to associate this task with (requires features.sprints)")),
		mcp.WithString("project_id", mcp.Description("Project ID to associate this task with (requires features.projects)")),
		mcp.WithString("epic_id", mcp.Description("Epic ID to associate this task with (requires features.epics)")),
	), a.handleTaskCreate)

	a.server.AddTool(mcp.NewTool("clockwork_task_get",
		mcp.WithDescription("Get a task by ID"),
		mcp.WithString("id", mcp.Required(), mcp.Description("Task ID")),
	), a.handleTaskGet)

	a.server.AddTool(mcp.NewTool("clockwork_task_list",
		mcp.WithDescription("List tasks with optional filters"),
		mcp.WithString("status", mcp.Description("Filter by status")),
		mcp.WithNumber("priority", mcp.Description("Filter by priority")),
		mcp.WithString("executor", mcp.Description("Filter by executor")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 50)")),
	), a.handleTaskList)

	a.server.AddTool(mcp.NewTool("clockwork_task_update",
		mcp.WithDescription("Update a task"),
		mcp.WithString("id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("title", mcp.Description("New title")),
		mcp.WithString("description", mcp.Description("New description")),
		mcp.WithNumber("priority", mcp.Description("New priority")),
		mcp.WithString("tags", mcp.Description("New tags JSON array")),
		mcp.WithString("sprint_id", mcp.Description("Sprint ID (set empty string to unassign)")),
		mcp.WithString("project_id", mcp.Description("Project ID (set empty string to unassign)")),
		mcp.WithString("epic_id", mcp.Description("Epic ID (set empty string to unassign)")),
	), a.handleTaskUpdate)

	a.server.AddTool(mcp.NewTool("clockwork_task_delete",
		mcp.WithDescription("Delete a task"),
		mcp.WithString("id", mcp.Required(), mcp.Description("Task ID")),
	), a.handleTaskDelete)

	a.server.AddTool(mcp.NewTool("clockwork_task_transition",
		mcp.WithDescription("Transition a task to a new status"),
		mcp.WithString("id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("status", mcp.Required(), mcp.Description("Target status")),
	), a.handleTaskTransition)

	a.server.AddTool(mcp.NewTool("clockwork_task_search",
		mcp.WithDescription("Search tasks by text query"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
	), a.handleTaskSearch)

	a.server.AddTool(mcp.NewTool("clockwork_task_bulk_transition",
		mcp.WithDescription("Transition multiple tasks to a new status"),
		mcp.WithString("ids", mcp.Required(), mcp.Description("JSON array of task IDs")),
		mcp.WithString("status", mcp.Required(), mcp.Description("Target status")),
	), a.handleTaskBulkTransition)
}

func (a *Adapter) handleTaskCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	input := service.TaskCreateInput{
		Title:        reqStr(req, "title"),
		Description:  reqStr(req, "description"),
		Priority:     reqInt(req, "priority"),
		Executor:     reqStr(req, "executor"),
		AgentProfile: reqStr(req, "agent_profile"),
		WorkingDir:   reqStr(req, "working_dir"),
		SystemPrompt: reqStr(req, "system_prompt"),
		OnDone:       reqStr(req, "on_done"),
		OnFail:       reqStr(req, "on_fail"),
		OnDoneMerge:  reqStr(req, "on_done_merge"),
		Manual:       reqBool(req, "manual"),
		SprintID:     reqStr(req, "sprint_id"),
		ProjectID:    reqStr(req, "project_id"),
		EpicID:       reqStr(req, "epic_id"),
	}

	if raw := reqStr(req, "tags"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &input.Tags); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid tags JSON: %v", err)), nil
		}
	}
	if raw := reqStr(req, "depends_on"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &input.DependsOn); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid depends_on JSON: %v", err)), nil
		}
	}

	task, err := a.svc.Task.Create(input)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(task)
}

func (a *Adapter) handleTaskGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	task, err := a.svc.Task.Get(reqStr(req, "id"))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(task)
}

func (a *Adapter) handleTaskList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := reqInt(req, "limit")
	if limit == 0 {
		limit = 50
	}
	filter := sqlstore.TaskFilter{
		Status:   reqStr(req, "status"),
		Priority: reqInt(req, "priority"),
		Executor: reqStr(req, "executor"),
		Limit:    limit,
	}
	tasks, err := a.svc.Task.List(filter)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(tasks)
}

func (a *Adapter) handleTaskUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")
	update := sqlstore.TaskUpdate{}

	if v := reqStr(req, "title"); v != "" {
		update.Title = &v
	}
	if v := reqStr(req, "description"); v != "" {
		update.Description = &v
	}
	if v := reqInt(req, "priority"); v != 0 {
		update.Priority = &v
	}
	if v := reqStr(req, "tags"); v != "" {
		update.Tags = &v
	}

	// Association fields — allow setting to empty string to unassign
	args := req.GetArguments()
	if _, ok := args["sprint_id"]; ok {
		v := reqStr(req, "sprint_id")
		ns := sql.NullString{String: v, Valid: v != ""}
		update.SprintID = &ns
	}
	if _, ok := args["project_id"]; ok {
		v := reqStr(req, "project_id")
		ns := sql.NullString{String: v, Valid: v != ""}
		update.ProjectID = &ns
	}
	if _, ok := args["epic_id"]; ok {
		v := reqStr(req, "epic_id")
		ns := sql.NullString{String: v, Valid: v != ""}
		update.EpicID = &ns
	}

	if err := a.svc.Task.Update(id, update); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	task, err := a.svc.Task.Get(id)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(task)
}

func (a *Adapter) handleTaskDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := a.svc.Task.Delete(reqStr(req, "id")); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText("deleted"), nil
}

func (a *Adapter) handleTaskTransition(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := a.svc.Task.Transition(reqStr(req, "id"), reqStr(req, "status")); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	task, err := a.svc.Task.Get(reqStr(req, "id"))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(task)
}

func (a *Adapter) handleTaskSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tasks, err := a.svc.Task.Search(reqStr(req, "query"))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(tasks)
}

func (a *Adapter) handleTaskBulkTransition(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw := reqStr(req, "ids")
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid ids JSON: %v", err)), nil
	}
	status := reqStr(req, "status")
	success, errs := a.svc.Task.BulkTransition(ids, status)

	var errMsgs []string
	for _, e := range errs {
		errMsgs = append(errMsgs, e.Error())
	}

	result := map[string]interface{}{
		"success": success,
		"failed":  len(errs),
	}
	if len(errMsgs) > 0 {
		result["errors"] = strings.Join(errMsgs, "; ")
	}
	return jsonResult(result)
}
