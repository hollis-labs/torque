package mcpadapter

import (
	"context"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerCommentTools() {
	a.server.AddTool(mcp.NewTool("clockwork_comment_add",
		mcp.WithDescription(`Append a comment (freeform prose) to a task; returns the persisted CommentRecord with assigned ID.
Use for agent-to-user channel, review notes, or blocked-reason explanation; structured audit trails should go in artifacts via clockwork_artifact_create. Comments never drive lifecycle.
Response shape: data = {<CommentRecord fields>} — singleton.
Example: {"task_id":"T-123","author":"reviewer","content":"Please also cover the null-parent case."}`),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("author", mcp.Description("Comment author slug/id")),
		mcp.WithString("content", mcp.Required(), mcp.Description("Comment body (prose)")),
	), a.handleCommentAdd)

	a.server.AddTool(mcp.NewTool("clockwork_comment_list",
		mcp.WithDescription(`List all comments on a task, newest first. Default brief shape includes a 100-char excerpt of the body; pass verbose="true" for full content.
Use to review the discussion thread; clockwork_comment_add to append. No comment_get/delete yet — brief ID + list is the read surface.
Response shape: data = {items: [<briefComment or CommentRecord>...], meta: {truncated, returned, limit, hint?}}.
Example: {"task_id":"T-123"}`),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
	), a.handleCommentList)

	a.server.AddTool(mcp.NewTool("clockwork_comment_search",
		mcp.WithDescription(`Search comments by content across all tasks, optionally scoped to a task or author. Returns newest first.
Use to discover comments by content; clockwork_comment_list for per-task ordered history. No comment_get/delete yet.
Response shape: data = {items: [<CommentRecord>...], meta: {truncated, returned, limit, hint?}}.
Example: {"query":"review notes","task_id":"T-123"}`),
		mcp.WithString("query", mcp.Required(), mcp.Description("Substring match on comment content")),
		mcp.WithString("task_id", mcp.Description("Restrict to one task (optional)")),
		mcp.WithString("author", mcp.Description("Exact author match (optional)")),
		mcp.WithString("limit", mcp.Description("Max results (integer, default 25, max 100)")),
	), a.handleCommentSearch)
}

func (a *Adapter) handleCommentAdd(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	taskID := reqStr(req, "task_id")
	comment, err := a.svc.Comment.Add(
		taskID,
		reqStr(req, "author"),
		reqStr(req, "content"),
	)
	if err != nil {
		return errFromService(err)
	}
	// comment is typed as *sqlstore.CommentRecord — return the bare record so
	// callers can see the assigned ID / timestamp without a second round-trip.
	return okResult(comment)
}

func (a *Adapter) handleCommentList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	verbose := reqStrBool(req, "verbose")
	comments, err := a.svc.Comment.List(reqStr(req, "task_id"))
	if err != nil {
		return errFromService(err)
	}
	limit := defaultGenericListLimit
	items := make([]any, 0, len(comments))
	for _, c := range comments {
		if verbose {
			items = append(items, c)
		} else {
			items = append(items, toBriefComment(c))
		}
	}
	return cappedJSONResult(items, limit)
}

const (
	defaultCommentSearchLimit = 25
	maxCommentSearchLimit     = 100
)

func (a *Adapter) handleCommentSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query := reqStr(req, "query")
	if query == "" {
		return errResult(ErrCodeArgInvalid, "query is required", "query")
	}
	limit := clampLimit(reqInt(req, "limit"), defaultCommentSearchLimit, maxCommentSearchLimit)

	filter := sqlstore.CommentFilter{
		Search: query,
		TaskID: reqStr(req, "task_id"),
		Author: reqStr(req, "author"),
		Limit:  limit,
	}

	comments, err := a.svc.Comment.Search(filter)
	if err != nil {
		return errFromService(err)
	}

	items := make([]any, 0, len(comments))
	for _, c := range comments {
		items = append(items, c)
	}
	return cappedJSONResult(items, limit)
}


