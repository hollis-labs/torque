package mcpadapter

import (
	"context"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerArtifactTools() {
	a.server.AddTool(mcp.NewTool("clockwork_artifact_create",
		mcp.WithDescription("Create an artifact for a task"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("type", mcp.Required(), mcp.Description("Artifact type")),
		mcp.WithString("content", mcp.Description("Artifact content")),
		mcp.WithString("url", mcp.Description("Artifact URL")),
		mcp.WithString("file_path", mcp.Description("Artifact file path")),
	), a.handleArtifactCreate)

	a.server.AddTool(mcp.NewTool("clockwork_artifact_list",
		mcp.WithDescription("List all artifacts for a task"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
	), a.handleArtifactList)
}

func (a *Adapter) handleArtifactCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rec := &sqlstore.ArtifactRecord{
		TaskID:   reqStr(req, "task_id"),
		Type:     reqStr(req, "type"),
		Content:  reqStr(req, "content"),
		URL:      reqStr(req, "url"),
		FilePath: reqStr(req, "file_path"),
	}
	if err := a.svc.Artifact.Create(rec); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(rec)
}

func (a *Adapter) handleArtifactList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	artifacts, err := a.svc.Artifact.List(reqStr(req, "task_id"))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(artifacts)
}
