package mcpadapter

import (
	"context"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerArtifactTools() {
	a.addTool(mcp.NewTool("clockwork_artifact_create",
		mcp.WithDescription(`Create an artifact (file pointer, URL, or inline content) attached to a task; returns the persisted ArtifactRecord with assigned numeric ID.
Use for deliverables and evidence; prefer clockwork_comment_add for discussion prose. Subtodo evidence strings go through clockwork_task_subtodo_done.
Response shape: data = {<ArtifactRecord fields>} — singleton.
Example: {"task_id":"T-123","type":"file","file_path":"/tmp/report.md"}`),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("type", mcp.Required(), mcp.Description("Artifact type (file|url|inline|diff|...)")),
		mcp.WithString("content", mcp.Description("Inline content (for type=inline)")),
		mcp.WithString("url", mcp.Description("URL (for type=url)")),
		mcp.WithString("file_path", mcp.Description("Filesystem path (for type=file)")),
	), a.handleArtifactCreate)

	a.addTool(mcp.NewTool("clockwork_artifact_list",
		mcp.WithDescription(`List all artifacts for a task, newest first. Default brief shape drops inline content body for size; pass verbose="true" for full records.
Use to discover outputs; clockwork_artifact_get when you have the numeric ID. clockwork_comment_list is the prose/discussion analog.
Response shape: data = {items: [<briefArtifact or ArtifactRecord>...], meta: {truncated, returned, limit, hint?}}.
Example: {"task_id":"T-123"}`),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
	), a.handleArtifactList)

	a.addTool(mcp.NewTool("clockwork_artifact_get",
		mcp.WithDescription(`Fetch one artifact's full record by numeric ID (pass as string per numeric-as-string convention).
Use when you have the ID; clockwork_artifact_list for discovery.
Response shape: data = {<ArtifactRecord fields>} — singleton.
Example: {"artifact_id":"42"}`),
		mcp.WithString("artifact_id", mcp.Required(), mcp.Description("Artifact ID (integer; pass as string)")),
	), a.handleArtifactGet)

	a.addTool(mcp.NewTool("clockwork_artifact_delete",
		mcp.WithDescription(`Delete an artifact row by numeric ID. Does NOT remove the referenced file on disk — caller owns filesystem cleanup.
Use for artifact-row cleanup; for deleting an entire task + its artifacts, use clockwork_task_delete (cascade).
Response shape: data = {id, deleted: true}.
Example: {"artifact_id":"42"}`),
		mcp.WithString("artifact_id", mcp.Required(), mcp.Description("Artifact ID (integer; pass as string)")),
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
		return errFromService(err)
	}
	return okResult(rec)
}

func (a *Adapter) handleArtifactList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	verbose := reqStrBool(req, "verbose")
	artifacts, err := a.svc.Artifact.List(reqStr(req, "task_id"))
	if err != nil {
		return errFromService(err)
	}
	limit := defaultGenericListLimit
	items := make([]any, 0, len(artifacts))
	for _, a := range artifacts {
		if verbose {
			items = append(items, a)
		} else {
			items = append(items, toBriefArtifact(a))
		}
	}
	return cappedJSONResult(items, limit)
}

func (a *Adapter) handleArtifactGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := int64(reqInt(req, "artifact_id"))
	art, err := a.svc.Artifact.Get(id)
	if err != nil {
		return errFromService(err)
	}
	return okResult(art)
}

func (a *Adapter) handleArtifactDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := int64(reqInt(req, "artifact_id"))
	if err := a.svc.Artifact.Delete(id); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]interface{}{"id": id, "deleted": true})
}
