package mcpadapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

func (a *Adapter) registerCheckpointTools() {
	a.server.AddTool(mcp.NewTool("clockwork_task_checkpoint_emit",
		mcp.WithDescription("Emit a task checkpoint (pending row, parks task if checkpoint_mode=blocking)"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID to attach the checkpoint to")),
		mcp.WithString("type", mcp.Required(), mcp.Description("Free-form checkpoint type (e.g. collect_data)")),
		mcp.WithString("payload_json", mcp.Required(), mcp.Description("Opaque JSON payload — schema owned by go-envelope (deferred)")),
		mcp.WithString("emitter_source_type", mcp.Description("agent|user|api|system|webhook|import (default system)")),
		mcp.WithString("emitter_source_ref", mcp.Description("Originating slug/id")),
		mcp.WithString("timeout_at", mcp.Description("Optional RFC3339 timestamp for the timeout sweeper")),
	), a.handleCheckpointEmit)

	a.server.AddTool(mcp.NewTool("clockwork_task_checkpoint_respond",
		mcp.WithDescription("Respond to a pending checkpoint; applies on_checkpoint_response rule to the task"),
		mcp.WithString("correlation_id", mcp.Required(), mcp.Description("Checkpoint correlation_id (ULID)")),
		mcp.WithString("response_json", mcp.Required(), mcp.Description("Opaque JSON response body")),
		mcp.WithString("responder_source_type", mcp.Required(), mcp.Description("agent|user|api|system|webhook|import")),
		mcp.WithString("responder_source_ref", mcp.Description("Responder slug/id")),
	), a.handleCheckpointRespond)

	a.server.AddTool(mcp.NewTool("clockwork_task_checkpoint_cancel",
		mcp.WithDescription("Cancel a pending checkpoint; task stays in review with a canceled reason"),
		mcp.WithString("correlation_id", mcp.Required()),
		mcp.WithString("reason", mcp.Description("Human-readable cancellation reason")),
		mcp.WithString("canceler_source_type", mcp.Description("agent|user|api|system|webhook|import")),
		mcp.WithString("canceler_source_ref", mcp.Description("Canceler slug/id")),
	), a.handleCheckpointCancel)

	a.server.AddTool(mcp.NewTool("clockwork_task_checkpoint_list",
		mcp.WithDescription("List checkpoints emitted against a task, newest first"),
		mcp.WithString("task_id", mcp.Required()),
	), a.handleCheckpointList)

	a.server.AddTool(mcp.NewTool("clockwork_task_checkpoint_get",
		mcp.WithDescription("Get a checkpoint by correlation_id"),
		mcp.WithString("correlation_id", mcp.Required()),
	), a.handleCheckpointGet)

	a.server.AddTool(mcp.NewTool("clockwork_task_checkpoints_pending",
		mcp.WithDescription("List every pending checkpoint across all tasks (oldest first)"),
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
			return mcp.NewToolResultError(fmt.Sprintf("invalid timeout_at: %v", err)), nil
		}
		in.TimeoutAt = &t
	}
	out, err := a.svc.Checkpoint.Emit(in)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(out)
}

func (a *Adapter) handleCheckpointRespond(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	err := a.svc.Checkpoint.Respond(service.CheckpointRespondInput{
		CorrelationID:       reqStr(req, "correlation_id"),
		ResponseJSON:        reqStr(req, "response_json"),
		ResponderSourceType: reqStr(req, "responder_source_type"),
		ResponderSourceRef:  reqStr(req, "responder_source_ref"),
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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
		return mcp.NewToolResultError(err.Error()), nil
	}
	return a.checkpointResultByCorr(reqStr(req, "correlation_id"))
}

func (a *Adapter) handleCheckpointList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	list, err := a.svc.Checkpoint.ListForTask(reqStr(req, "task_id"))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(list)
}

func (a *Adapter) handleCheckpointGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return a.checkpointResultByCorr(reqStr(req, "correlation_id"))
}

func (a *Adapter) handleCheckpointPending(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pending, err := a.svc.Checkpoint.ListPending()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(pending)
}

// checkpointResultByCorr loads the current row for correlationID and returns
// it as the canonical MCP response shape (mirrors the Go struct, letting
// callers see status / responder / timestamps after the operation).
func (a *Adapter) checkpointResultByCorr(correlationID string) (*mcp.CallToolResult, error) {
	cp, err := a.svc.Checkpoint.Get(correlationID)
	if err != nil {
		if errors.Is(err, sqlstore.ErrCheckpointNotFound) {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(cp)
}
