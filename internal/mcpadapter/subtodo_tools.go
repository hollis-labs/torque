package mcpadapter

import (
	"context"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerSubtodoTools() {
	a.server.AddTool(mcp.NewTool("clockwork_task_subtodo_list",
		mcp.WithDescription(`List the structural checklist (subtodos) on a task. Brief shape drops the evidence column; pass verbose="true" for full records.
Use to inspect gating state; sibling clockwork_task_subtodo_add/clockwork_task_subtodo_done mutate. Unlike comments, subtodos drive lifecycle (required items block done).
Response shape: data = {items: [<briefSubtodo or Subtodo>...], meta: {truncated, returned, limit, hint?}}.
Example: {"task_id":"T-123"}`),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
	), a.handleSubtodoList)

	a.server.AddTool(mcp.NewTool("clockwork_task_subtodo_add",
		mcp.WithDescription(`Append a subtodo item to a task's checklist. required=true means the task cannot transition past review until the item is ticked off.
Use for structural gating; clockwork_task_subtodo_done marks completion, clockwork_task_subtodo_list reads. Prefer clockwork_comment_add for non-gating discussion.
Response shape: data = [<Subtodo>...] — returns the updated full checklist.
Example: {"task_id":"T-123","id":"check-1","text":"write regression test","required":true}`),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("id", mcp.Required(), mcp.Description("Item id (unique per task)")),
		mcp.WithString("text", mcp.Required(), mcp.Description("Human-readable description")),
		mcp.WithBoolean("required", mcp.Description("If true, blocks done until ticked off (default false)")),
	), a.handleSubtodoAdd)

	a.server.AddTool(mcp.NewTool("clockwork_task_subtodo_done",
		mcp.WithDescription(`Mark a subtodo done with an evidence string (artifact id, commit SHA, URL, or note).
Use to unblock required subtodos; clockwork_task_subtodo_add to create, clockwork_task_subtodo_list to inspect. No separate subtodo_delete — items are forever-tracked.
Response shape: data = [<Subtodo>...] — returns the updated full checklist.
Example: {"task_id":"T-123","id":"check-1","evidence":"abc123 / PR #42"}`),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("id", mcp.Required(), mcp.Description("Item id to mark done")),
		mcp.WithString("evidence", mcp.Description("Optional evidence pointer (artifact id, commit, URL, or note)")),
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
