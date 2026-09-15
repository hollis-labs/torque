package mcpadapter

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerArtifactTools() {
	a.addTool(mcp.NewTool("torque_artifact_create",
		mcp.WithDescription(`Create an artifact (file pointer, URL, or inline content) attached to a task; returns the persisted ArtifactRecord with assigned numeric ID.
Use for deliverables and evidence; prefer torque_comment_add for discussion prose. Subtodo evidence strings go through torque_task_subtodo_done.
Response shape: data = {<ArtifactRecord fields>} — singleton.
Example: {"task_id":"T-123","type":"file","file_path":"/tmp/report.md"}`),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("type", mcp.Required(), mcp.Description("Artifact type (file|url|inline|diff|...)")),
		mcp.WithString("content", mcp.Description("Inline content (for type=inline)")),
		mcp.WithString("url", mcp.Description("URL (for type=url)")),
		mcp.WithString("file_path", mcp.Description("Filesystem path (for type=file)")),
		mcp.WithString("run_id", artifactNullableInt64Schema(), mcp.Description("Optional run ID linked to this artifact. Must exist and belong to task_id. Omit or null for no link.")),
		mcp.WithObject("metadata", artifactMetadataSchema(), mcp.Description("Optional JSON object metadata. Omit unchanged/not set; null clears on update. Legacy JSON-string object is accepted.")),
	), a.handleArtifactCreate)

	a.addTool(mcp.NewTool("torque_artifact_list",
		mcp.WithDescription(`List all artifacts for a task, newest first. Default brief shape drops inline content body for size; pass verbose="true" for full records.
Use to discover outputs; torque_artifact_get when you have the numeric ID. torque_comment_list is the prose/discussion analog.
Response shape: data = {items: [<briefArtifact or ArtifactRecord>...], meta: {truncated, returned, limit, hint?}}.
Example: {"task_id":"T-123"}`),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
	), a.handleArtifactList)

	a.addTool(mcp.NewTool("torque_artifact_get",
		mcp.WithDescription(`Fetch one artifact's full record by numeric ID (pass as string per numeric-as-string convention).
Use when you have the ID; torque_artifact_list for discovery.
Response shape: data = {<ArtifactRecord fields>} — singleton.
Example: {"artifact_id":"42"}`),
		mcp.WithString("artifact_id", mcp.Required(), mcp.Description("Artifact ID (integer; pass as string)")),
	), a.handleArtifactGet)

	a.addTool(mcp.NewTool("torque_artifact_update",
		mcp.WithDescription(`Patch mutable artifact fields while preserving ID, task_id and created_at.
Omitted fields stay unchanged. Empty content/url/file_path explicitly clears that field. type is still required to be nonblank if supplied.
run_id accepts an exact integer or null to clear. metadata accepts a JSON object or null to clear; legacy JSON-string objects are accepted and decoded with number precision preserved.
Response shape: data = {<ArtifactRecord fields>} — singleton.
Example: {"artifact_id":"42","content":"","metadata":{"status":"corrected"}}`),
		mcp.WithString("artifact_id", mcp.Required(), mcp.Description("Artifact ID (integer; pass as string)")),
		mcp.WithString("type", mcp.Description("New artifact type; must be nonblank when supplied")),
		mcp.WithString("content", mcp.Description("New inline content; pass empty string to clear")),
		mcp.WithString("url", mcp.Description("New URL; pass empty string to clear")),
		mcp.WithString("file_path", mcp.Description("New filesystem path; pass empty string to clear")),
		mcp.WithString("run_id", artifactNullableInt64Schema(), mcp.Description("Exact run ID to link, or null to clear. Linked run must belong to the artifact task.")),
		mcp.WithObject("metadata", artifactMetadataSchema(), mcp.Description("JSON object metadata, JSON-string object, or null to clear")),
	), a.handleArtifactUpdate)

	a.addTool(mcp.NewTool("torque_artifact_delete",
		mcp.WithDescription(`Delete an artifact row by numeric ID. Does NOT remove the referenced file on disk — caller owns filesystem cleanup.
Use for artifact-row cleanup; for deleting an entire task + its artifacts, use torque_task_delete (cascade).
Response shape: data = {id, deleted: true}.
Example: {"artifact_id":"42"}`),
		mcp.WithString("artifact_id", mcp.Required(), mcp.Description("Artifact ID (integer; pass as string)")),
	), a.handleArtifactDelete)
}

func (a *Adapter) handleArtifactCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	taskID, errRes := reqRequiredString(req, "task_id")
	if errRes != nil {
		return errRes, nil
	}
	typ, errRes := reqRequiredString(req, "type")
	if errRes != nil {
		return errRes, nil
	}
	content, errRes := reqCreateOptionalString(req, "content")
	if errRes != nil {
		return errRes, nil
	}
	url, errRes := reqCreateOptionalString(req, "url")
	if errRes != nil {
		return errRes, nil
	}
	filePath, errRes := reqCreateOptionalString(req, "file_path")
	if errRes != nil {
		return errRes, nil
	}
	rec := &sqlstore.ArtifactRecord{
		TaskID:   taskID,
		Type:     typ,
		Content:  content,
		URL:      url,
		FilePath: filePath,
	}
	if runID, present, errRes := reqNullableInt64(req, "run_id"); errRes != nil {
		return errRes, nil
	} else if present && runID != nil {
		rec.RunID = sql.NullInt64{Int64: *runID, Valid: true}
	}
	if metadata, present, errRes := reqNullableMetadata(req, "metadata"); errRes != nil {
		return errRes, nil
	} else if present && metadata != nil {
		rec.Metadata = sql.NullString{String: string(metadata), Valid: true}
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
	id, errRes := reqStrictInt64(req, "artifact_id")
	if errRes != nil {
		return errRes, nil
	}
	art, err := a.svc.Artifact.Get(id)
	if err != nil {
		return errFromService(err)
	}
	return okResult(art)
}

func (a *Adapter) handleArtifactUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, errRes := reqStrictInt64(req, "artifact_id")
	if errRes != nil {
		return errRes, nil
	}
	args := req.GetArguments()
	var in service.ArtifactUpdateInput
	if _, ok := args["type"]; ok {
		v, errRes := reqStrictString(req, "type")
		if errRes != nil {
			return errRes, nil
		}
		in.Type = &v
	}
	if _, ok := args["content"]; ok {
		v, errRes := reqStrictString(req, "content")
		if errRes != nil {
			return errRes, nil
		}
		in.Content = &v
	}
	if _, ok := args["url"]; ok {
		v, errRes := reqStrictString(req, "url")
		if errRes != nil {
			return errRes, nil
		}
		in.URL = &v
	}
	if _, ok := args["file_path"]; ok {
		v, errRes := reqStrictString(req, "file_path")
		if errRes != nil {
			return errRes, nil
		}
		in.FilePath = &v
	}
	if runID, present, errRes := reqNullableInt64(req, "run_id"); errRes != nil {
		return errRes, nil
	} else if present {
		if runID == nil {
			in.RunID = &sql.NullInt64{}
		} else {
			in.RunID = &sql.NullInt64{Int64: *runID, Valid: true}
		}
	}
	if metadata, present, errRes := reqNullableMetadata(req, "metadata"); errRes != nil {
		return errRes, nil
	} else if present {
		if metadata == nil {
			in.Metadata = &sql.NullString{}
		} else {
			in.Metadata = &sql.NullString{String: string(metadata), Valid: true}
		}
	}
	art, err := a.svc.Artifact.Update(id, in)
	if err != nil {
		return errFromService(err)
	}
	return okResult(art)
}

func (a *Adapter) handleArtifactDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, errRes := reqStrictInt64(req, "artifact_id")
	if errRes != nil {
		return errRes, nil
	}
	if err := a.svc.Artifact.Delete(id); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]interface{}{"id": id, "deleted": true})
}

func artifactNullableInt64Schema() mcp.PropertyOption {
	return func(schema map[string]any) {
		delete(schema, "type")
		schema["anyOf"] = []map[string]any{
			{"type": "string", "pattern": "^-?[0-9]+$"},
			{"type": "integer"},
			{"type": "null"},
		}
	}
}

func artifactMetadataSchema() mcp.PropertyOption {
	return func(schema map[string]any) {
		delete(schema, "type")
		delete(schema, "properties")
		schema["anyOf"] = []map[string]any{
			{"type": "object"},
			{"type": "string"},
			{"type": "null"},
		}
	}
}

func reqStrictInt64(req mcp.CallToolRequest, field string) (int64, *mcp.CallToolResult) {
	raw, ok := req.GetArguments()[field]
	if !ok {
		return 0, mustErrResult(ErrCodeArgInvalid, field+" is required", field)
	}
	id, err := exactInt64(raw, field)
	if err != nil {
		return 0, mustErrResult(ErrCodeArgInvalid, err.Error(), field)
	}
	return id, nil
}

func reqStrictString(req mcp.CallToolRequest, field string) (string, *mcp.CallToolResult) {
	raw, ok := req.GetArguments()[field]
	if !ok {
		return "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", mustErrResult(ErrCodeArgInvalid, field+" must be a string", field)
	}
	return s, nil
}

func reqRequiredString(req mcp.CallToolRequest, field string) (string, *mcp.CallToolResult) {
	raw, ok := req.GetArguments()[field]
	if !ok {
		return "", mustErrResult(ErrCodeArgInvalid, field+" is required", field)
	}
	s, ok := raw.(string)
	if !ok {
		return "", mustErrResult(ErrCodeArgInvalid, field+" must be a string", field)
	}
	if strings.TrimSpace(s) == "" {
		return "", mustErrResult(ErrCodeArgInvalid, field+" is required", field)
	}
	return s, nil
}

func reqCreateOptionalString(req mcp.CallToolRequest, field string) (string, *mcp.CallToolResult) {
	raw, ok := req.GetArguments()[field]
	if !ok || raw == nil {
		return "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", mustErrResult(ErrCodeArgInvalid, field+" must be a string", field)
	}
	return s, nil
}

func reqNullableInt64(req mcp.CallToolRequest, field string) (*int64, bool, *mcp.CallToolResult) {
	raw, ok := req.GetArguments()[field]
	if !ok {
		return nil, false, nil
	}
	if raw == nil {
		return nil, true, nil
	}
	id, err := exactInt64(raw, field)
	if err != nil {
		return nil, true, mustErrResult(ErrCodeArgInvalid, err.Error()+" or null", field)
	}
	return &id, true, nil
}

func reqNullableMetadata(req mcp.CallToolRequest, field string) (json.RawMessage, bool, *mcp.CallToolResult) {
	raw, ok := req.GetArguments()[field]
	if !ok {
		return nil, false, nil
	}
	if raw == nil {
		return nil, true, nil
	}
	out, err := normalizeMetadataValue(raw)
	if err != nil {
		return nil, true, mustErrResult(ErrCodeArgInvalid, err.Error(), field)
	}
	return out, true, nil
}

func normalizeMetadataValue(raw any) (json.RawMessage, error) {
	switch v := raw.(type) {
	case string:
		dec := json.NewDecoder(strings.NewReader(v))
		dec.UseNumber()
		var obj map[string]any
		if err := dec.Decode(&obj); err != nil {
			return nil, errors.New("metadata must be a JSON object, JSON-string object, or null")
		}
		var extra any
		if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
			return nil, errors.New("metadata must contain a single JSON object")
		}
		return json.Marshal(obj)
	case map[string]any:
		if err := rejectUnsafeMetadataNumbers(v); err != nil {
			return nil, err
		}
		return json.Marshal(v)
	default:
		return nil, errors.New("metadata must be a JSON object, JSON-string object, or null")
	}
}

func rejectUnsafeMetadataNumbers(v any) error {
	switch x := v.(type) {
	case map[string]any:
		for _, child := range x {
			if err := rejectUnsafeMetadataNumbers(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range x {
			if err := rejectUnsafeMetadataNumbers(child); err != nil {
				return err
			}
		}
	case float64:
		if !math.IsNaN(x) && !math.IsInf(x, 0) && math.Trunc(x) != x {
			return nil
		}
		if !isSafeIntegerFloat(x) {
			return errors.New("metadata contains a number that cannot be preserved exactly from native MCP input; pass metadata as a JSON string")
		}
	}
	return nil
}

func isSafeIntegerFloat(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Trunc(v) == v && math.Abs(v) <= 9007199254740991
}

func mustErrResult(code ErrorCode, message, field string) *mcp.CallToolResult {
	res, _ := errResult(code, message, field)
	return res
}
