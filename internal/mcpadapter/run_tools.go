package mcpadapter

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerRunTools() {
	a.server.AddTool(mcp.NewTool("clockwork_run_list",
		mcp.WithDescription("List all runs for a task"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
	), a.handleRunList)

	a.server.AddTool(mcp.NewTool("clockwork_run_get",
		mcp.WithDescription("Get a run by ID"),
		mcp.WithString("id", mcp.Required(), mcp.Description("Run ID (integer)")),
	), a.handleRunGet)
}

func (a *Adapter) handleRunList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	verbose := reqStrBool(req, "verbose")
	runs, err := a.svc.Run.List(reqStr(req, "task_id"))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := defaultGenericListLimit
	items := make([]any, 0, len(runs))
	for _, r := range runs {
		if verbose {
			items = append(items, r)
		} else {
			items = append(items, toBriefRun(r))
		}
	}
	return cappedJSONResult(items, limit)
}

func (a *Adapter) handleRunGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := int64(reqInt(req, "id"))
	run, err := a.svc.Run.Get(id)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(run)
}
