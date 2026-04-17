package mcpadapter

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerCommentTools() {
	a.server.AddTool(mcp.NewTool("clockwork_comment_add",
		mcp.WithDescription("Add a comment to a task"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("author", mcp.Description("Comment author")),
		mcp.WithString("content", mcp.Required(), mcp.Description("Comment content")),
	), a.handleCommentAdd)

	a.server.AddTool(mcp.NewTool("clockwork_comment_list",
		mcp.WithDescription("List all comments for a task"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
	), a.handleCommentList)
}

func (a *Adapter) handleCommentAdd(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if _, err := a.svc.Comment.Add(
		reqStr(req, "task_id"),
		reqStr(req, "author"),
		reqStr(req, "content"),
	); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText("comment added"), nil
}

func (a *Adapter) handleCommentList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	comments, err := a.svc.Comment.List(reqStr(req, "task_id"))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(comments)
}
