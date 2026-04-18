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

	a.server.AddTool(mcp.NewTool("clockwork_artifact_get",
		mcp.WithDescription("Get a single artifact by numeric ID"),
		mcp.WithString("artifact_id", mcp.Required(), mcp.Description("Artifact ID (integer)")),
	), a.handleArtifactGet)

	a.server.AddTool(mcp.NewTool("clockwork_artifact_delete",
		mcp.WithDescription("Delete an artifact row by numeric ID. Does not remove the referenced file on disk."),
		mcp.WithString("artifact_id", mcp.Required(), mcp.Description("Artifact ID (integer)")),
	), a.handleArtifactDelete)
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

func (a *Adapter) handleArtifactGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := int64(reqInt(req, "artifact_id"))
	art, err := a.svc.Artifact.Get(id)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(art)
}

func (a *Adapter) handleArtifactDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := int64(reqInt(req, "artifact_id"))
	if err := a.svc.Artifact.Delete(id); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(map[string]interface{}{"id": id, "deleted": true})
}
