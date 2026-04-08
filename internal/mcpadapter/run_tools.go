package mcpadapter

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerRunTools() {
	a.server.AddTool(mcp.NewTool("clockwork_run_list",
		mcp.WithDescription("List all runs for a task"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
	), a.handleRunList)

	a.server.AddTool(mcp.NewTool("clockwork_run_get",
		mcp.WithDescription("Get a run by ID"),
		mcp.WithNumber("id", mcp.Required(), mcp.Description("Run ID")),
	), a.handleRunGet)
}

func (a *Adapter) handleRunList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	runs, err := a.svc.Run.List(reqStr(req, "task_id"))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(runs)
}

func (a *Adapter) handleRunGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := int64(reqInt(req, "id"))
	run, err := a.svc.Run.Get(id)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(run)
}
