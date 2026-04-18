package mcpadapter

import (
	"context"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerSubtodoTools() {
	a.server.AddTool(mcp.NewTool("clockwork_task_subtodo_list",
		mcp.WithDescription("List the structural checklist (subtodos) on a task"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
	), a.handleSubtodoList)

	a.server.AddTool(mcp.NewTool("clockwork_task_subtodo_add",
		mcp.WithDescription("Append a subtodo item to a task's checklist. required=true means lifecycle blocks the task at done until this item is ticked off."),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("id", mcp.Required(), mcp.Description("Item id — must be unique within the task")),
		mcp.WithString("text", mcp.Required(), mcp.Description("Human-readable description")),
		mcp.WithBoolean("required", mcp.Description("If true, task can't transition past review until item is done (default false)")),
	), a.handleSubtodoAdd)

	a.server.AddTool(mcp.NewTool("clockwork_task_subtodo_done",
		mcp.WithDescription("Mark a subtodo as done with an evidence pointer (artifact id, commit SHA, URL, or note)"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("id", mcp.Required(), mcp.Description("Item id to mark done")),
		mcp.WithString("evidence", mcp.Description("Optional evidence string")),
	), a.handleSubtodoDone)
}

func (a *Adapter) handleSubtodoList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	verbose := reqStrBool(req, "verbose")
	items, err := a.svc.Task.ListSubtodos(reqStr(req, "task_id"))
	if err != nil {
		return errFromService(err)
	}
	if items == nil {
		items = []sqlstore.Subtodo{}
	}
	// Subtodos are cheap and always bounded by the parent task's checklist;
	// a generous default limit keeps the envelope consistent without forcing
	// callers to paginate a typical 5-20 item list.
	limit := defaultGenericListLimit
	out := make([]any, 0, len(items))
	for _, it := range items {
		if verbose {
			out = append(out, it)
		} else {
			out = append(out, toBriefSubtodo(it))
		}
	}
	return cappedJSONResult(out, limit)
}

func (a *Adapter) handleSubtodoAdd(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	items, err := a.svc.Task.AddSubtodo(reqStr(req, "task_id"), sqlstore.Subtodo{
		ID:       reqStr(req, "id"),
		Text:     reqStr(req, "text"),
		Required: reqBool(req, "required"),
	})
	if err != nil {
		return errFromService(err)
	}
	return okResult(items)
}

func (a *Adapter) handleSubtodoDone(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	items, err := a.svc.Task.MarkSubtodoDone(reqStr(req, "task_id"), reqStr(req, "id"), reqStr(req, "evidence"))
	if err != nil {
		return errFromService(err)
	}
	return okResult(items)
}
