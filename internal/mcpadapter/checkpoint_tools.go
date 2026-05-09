package mcpadapter

import (
	"context"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

func (a *Adapter) registerCheckpointTools() {
	a.addTool(mcp.NewTool("clockwork_task_checkpoint_emit",
		mcp.WithDescription(`Emit a pending checkpoint on a task. If the task's checkpoint_mode is "blocking", the task parks until respond/cancel.
Use for mid-run user-interaction gates or data-collection stops; sibling clockwork_task_checkpoint_respond to resolve, clockwork_task_checkpoint_cancel to abandon. clockwork_task_checkpoint_list/pending for discovery.
Response shape: data = {<CheckpointRecord fields>} — singleton with correlation_id, status="pending".
Example: {"task_id":"T-123","type":"collect_data","payload_json":"{\"q\":\"?\"}","emitter_source_type":"system"}`),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID to attach the checkpoint to")),
		mcp.WithString("type", mcp.Required(), mcp.Description("Free-form checkpoint type (e.g. collect_data)")),
		mcp.WithString("payload_json", mcp.Required(), mcp.Description("Opaque JSON payload — schema owned by go-envelope (deferred)")),
		mcp.WithString("emitter_source_type", mcp.Description("agent|user|api|system|webhook|import (default system)")),
		mcp.WithString("emitter_source_ref", mcp.Description("Originating slug/id")),
		mcp.WithString("timeout_at", mcp.Description("Optional RFC3339 timestamp for the timeout sweeper")),
	), a.handleCheckpointEmit)

	a.addTool(mcp.NewTool("clockwork_task_checkpoint_respond",
		mcp.WithDescription(`Resolve a pending checkpoint with a JSON response; applies the task's on_checkpoint_response rule (resume|review|custom).
Use to unpark a blocking task; clockwork_task_checkpoint_cancel to abandon without resolution. Responding to a terminal checkpoint returns error.code=conflict.
Response shape: data = {<CheckpointRecord fields>} — singleton, status="responded".
Example: {"correlation_id":"01HK...","response_json":"{\"decision\":\"ship\"}","responder_source_type":"user"}`),
		mcp.WithString("correlation_id", mcp.Required(), mcp.Description("Checkpoint correlation_id (ULID)")),
		mcp.WithString("response_json", mcp.Required(), mcp.Description("Opaque JSON response body")),
		mcp.WithString("responder_source_type", mcp.Required(), mcp.Description("agent|user|api|system|webhook|import")),
		mcp.WithString("responder_source_ref", mcp.Description("Responder slug/id")),
	), a.handleCheckpointRespond)

	a.addTool(mcp.NewTool("clockwork_task_checkpoint_cancel",
		mcp.WithDescription(`Cancel a pending checkpoint without a response; the task stays in review with the canceled reason logged.
Use when the checkpoint became obsolete; clockwork_task_checkpoint_respond when you have a real answer. Canceling a terminal checkpoint returns error.code=conflict.
Response shape: data = {<CheckpointRecord fields>} — singleton, status="canceled".
Example: {"correlation_id":"01HK...","reason":"superseded","canceler_source_type":"user"}`),
		mcp.WithString("correlation_id", mcp.Required()),
		mcp.WithString("reason", mcp.Description("Human-readable cancellation reason")),
		mcp.WithString("canceler_source_type", mcp.Description("agent|user|api|system|webhook|import")),
		mcp.WithString("canceler_source_ref", mcp.Description("Canceler slug/id")),
	), a.handleCheckpointCancel)

	a.addTool(mcp.NewTool("clockwork_task_checkpoint_list",
		mcp.WithDescription(`List checkpoints emitted against one task, newest first. Brief shape drops payload/response bodies; pass verbose="true" for full records.
Use to inspect one task's checkpoint history; clockwork_task_checkpoints_pending for cross-task pending-only view.
Response shape: data = {items: [<briefCheckpoint or CheckpointRecord>...], meta: {truncated, returned, limit, hint?}}.
Example: {"task_id":"T-123"}`),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
	), a.handleCheckpointList)

	a.addTool(mcp.NewTool("clockwork_task_checkpoint_get",
		mcp.WithDescription(`Fetch one checkpoint's full record by correlation_id (ULID).
Use when you have the correlation_id; clockwork_task_checkpoint_list for a task's history, clockwork_task_checkpoints_pending for cross-task pending.
Response shape: data = {<CheckpointRecord fields>} — singleton.
Example: {"correlation_id":"01HK..."}`),
		mcp.WithString("correlation_id", mcp.Required(), mcp.Description("Checkpoint correlation_id (ULID)")),
	), a.handleCheckpointGet)

	a.addTool(mcp.NewTool("clockwork_task_checkpoints_pending",
		mcp.WithDescription(`List every pending (unresolved) checkpoint across all tasks, oldest first — the global response queue.
Use for agent/user dashboards that need to triage outstanding decision gates; clockwork_task_checkpoint_list for single-task scope.
Response shape: data = {items: [<briefCheckpoint or CheckpointRecord>...], meta: {truncated, returned, limit, hint?}}.
Example: {}`),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
	), a.handleCheckpointPending)
}

func (a *Adapter) handleCheckpointEmit(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	in := service.CheckpointEmitInput{
		TaskID:            reqStr(req, "task_id"),
		Type:              reqStr(req, "type"),
		PayloadJSON:       reqStr(req, "payload_json"),
		EmitterSourceType: reqStr(req, "emitter_source_type"),
		EmitterSourceRef:  reqStr(req, "emitter_source_ref"),
	}
	if raw := reqStr(req, "timeout_at"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid timeout_at: %v", err), "timeout_at")
		}
		in.TimeoutAt = &t
	}
	out, err := a.svc.Checkpoint.Emit(in)
	if err != nil {
		return errFromService(err)
	}
	return okResult(out)
}

func (a *Adapter) handleCheckpointRespond(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	err := a.svc.Checkpoint.Respond(service.CheckpointRespondInput{
		CorrelationID:       reqStr(req, "correlation_id"),
		ResponseJSON:        reqStr(req, "response_json"),
		ResponderSourceType: reqStr(req, "responder_source_type"),
		ResponderSourceRef:  reqStr(req, "responder_source_ref"),
	})
	if err != nil {
		return errFromService(err)
	}
	return a.checkpointResultByCorr(reqStr(req, "correlation_id"))
}

func (a *Adapter) handleCheckpointCancel(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	err := a.svc.Checkpoint.Cancel(service.CheckpointCancelInput{
		CorrelationID:      reqStr(req, "correlation_id"),
		Reason:             reqStr(req, "reason"),
		CancelerSourceType: reqStr(req, "canceler_source_type"),
		CancelerSourceRef:  reqStr(req, "canceler_source_ref"),
	})
	if err != nil {
		return errFromService(err)
	}
	return a.checkpointResultByCorr(reqStr(req, "correlation_id"))
}

func (a *Adapter) handleCheckpointList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	verbose := reqStrBool(req, "verbose")
	list, err := a.svc.Checkpoint.ListForTask(reqStr(req, "task_id"))
	if err != nil {
		return errFromService(err)
	}
	limit := defaultGenericListLimit
	items := make([]any, 0, len(list))
	for _, c := range list {
		if verbose {
			items = append(items, c)
		} else {
			items = append(items, toBriefCheckpoint(c))
		}
	}
	return cappedJSONResult(items, limit)
}

func (a *Adapter) handleCheckpointGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return a.checkpointResultByCorr(reqStr(req, "correlation_id"))
}

func (a *Adapter) handleCheckpointPending(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	verbose := reqStrBool(req, "verbose")
	pending, err := a.svc.Checkpoint.ListPending()
	if err != nil {
		return errFromService(err)
	}
	limit := defaultGenericListLimit
	items := make([]any, 0, len(pending))
	for _, c := range pending {
		if verbose {
			items = append(items, c)
		} else {
			items = append(items, toBriefCheckpoint(c))
		}
	}
	return cappedJSONResult(items, limit)
}

// checkpointResultByCorr loads the current row for correlationID and returns
// it as the canonical MCP response shape (mirrors the Go struct, letting
// callers see status / responder / timestamps after the operation).
func (a *Adapter) checkpointResultByCorr(correlationID string) (*mcp.CallToolResult, error) {
	cp, err := a.svc.Checkpoint.Get(correlationID)
	if err != nil {
		return errFromService(err)
	}
	return okResult(cp)
}
