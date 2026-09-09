package mcpadapter

import (
	"context"
	"fmt"

	"github.com/hollis-labs/torque/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
)

// PRIM-003: generalized bulk-operation pattern, applied to Task as the
// reference entity. bulk_update/bulk_delete/bulk_tag all share one adapter-
// response shape (bulkResult, below) built on top of the service-layer
// {succeeded, failed} partial-success contract (service.RunBulk, bulk.go).
// bulk_transition is intentionally untouched — it predates this pattern and
// stays out of scope per ADR-0004 §3 / PRIM-003.

func (a *Adapter) registerTaskBulkTools() {
	a.addTool(mcp.NewTool("torque_task_bulk_update",
		mcp.WithDescription(`Apply the same partial update to many tasks in one call; per-task failures are collected, not fatal. Same field set and presence-in-payload semantics as torque_task_update — only keys you actually pass change; omit a key to leave that field untouched on every task.
Use for batch field edits (e.g. re-priority or re-home a cohort); prefer torque_task_update for a single task and torque_task_bulk_transition for status-only batch moves.
Response shape: data = {succeeded: [id...], failed: [{id, error: {code, message, field}}...]} — partial success is not an error; ok=true even when some ids fail. error.code uses the same taxonomy (arg_invalid/not_found/conflict/domain/permission/internal) as single-item torque_task_update.
Example: {"ids":"[\"T-1\",\"T-2\"]","priority":"1","tags":"[\"p0\"]"}`),
		mcp.WithString("ids", mcp.Required(), mcp.Description("JSON array of task IDs")),
		mcp.WithString("title", mcp.Description("New title")),
		mcp.WithString("description", mcp.Description("New description")),
		mcp.WithString("priority", mcp.Description("New priority (integer 1-5)")),
		mcp.WithBoolean("manual", mcp.Description("Manual flag")),
		mcp.WithString("executor", mcp.Description("Executor type")),
		mcp.WithString("launch_profile", mcp.Description("Torque launch_profile id (preferred). Empty string clears.")),
		mcp.WithString("agent_profile", mcp.Description("Legacy agent_profile name.")),
		mcp.WithString("working_dir", mcp.Description("Working directory")),
		mcp.WithString("system_prompt", mcp.Description("System prompt override")),
		mcp.WithString("agent_file", mcp.Description("Absolute or working_dir-relative path to a YAML agent spec; pass empty string to clear")),
		mcp.WithString("on_done", mcp.Description("Hook on done (close|review|notify)")),
		mcp.WithString("on_fail", mcp.Description("Hook on fail (retry|block|escalate|notify)")),
		mcp.WithString("on_review", mcp.Description("Hook on review (pause|notify|auto-approve)")),
		mcp.WithString("on_done_merge", mcp.Description("Merge hook on done (none|auto|pr|auto-resolve)")),
		mcp.WithString("deliverable_preset", mcp.Description("Deliverable preset name")),
		mcp.WithString("blocked_reason", mcp.Description("Blocked reason")),
		mcp.WithString("cost_budget", mcp.Description("Cost budget (numeric; -1 unlimited, 0 none, or positive)")),
		mcp.WithString("max_retries", mcp.Description("Max retries (non-negative integer)")),
		mcp.WithString("max_duration_ms", mcp.Description("Max duration in ms (integer; -1 unlimited or positive)")),
		mcp.WithString("token_budget", mcp.Description("Token budget (integer; -1 unlimited or positive)")),
		mcp.WithString("tools", mcp.Description("JSON array of tool names")),
		mcp.WithString("files", mcp.Description("JSON array of file paths")),
		mcp.WithString("permissions", mcp.Description("JSON object of permissions")),
		mcp.WithString("environment", mcp.Description("JSON object of env vars")),
		mcp.WithString("escalation_chain", mcp.Description("JSON array of escalation-target names")),
		mcp.WithString("quality_gates", mcp.Description("JSON array of gate names")),
		mcp.WithString("deliverables", mcp.Description("JSON array of Deliverable objects")),
		mcp.WithString("depends_on", mcp.Description("JSON array of dependency task IDs")),
		mcp.WithString("metadata", mcp.Description("JSON object: freeform metadata")),
		mcp.WithString("tags", mcp.Description("JSON array of tag names/slugs — replaces the full linked tag set on every task")),
		mcp.WithString("sprint_id", mcp.Description("Sprint ID (set empty string to unassign)")),
		mcp.WithString("project_id", mcp.Description("Project ID (set empty string to unassign)")),
		mcp.WithString("epic_id", mcp.Description("Epic ID (set empty string to unassign)")),
		mcp.WithString("kind", mcp.Description("New kind (agent|external|wait|decision|parent|plan|internal|issue)")),
		mcp.WithString("source_type", mcp.Description("New source_type")),
		mcp.WithString("source_ref", mcp.Description("New source_ref (empty string clears)")),
		mcp.WithString("trust", mcp.Description("New trust (trusted|normal|untrusted)")),
		mcp.WithString("checkpoint_mode", mcp.Description("New checkpoint_mode (none|blocking|non_blocking)")),
		mcp.WithString("on_checkpoint_response", mcp.Description("New on_checkpoint_response (resume|review|custom)")),
		mcp.WithString("parent_id", mcp.Description("New parent_id (migration 013; empty string clears the parent)")),
	), a.handleTaskBulkUpdate)

	a.addTool(mcp.NewTool("torque_task_bulk_delete",
		mcp.WithDescription(`Hard-delete many tasks in one call (runs, artifacts, comments cascade per task); per-task failures are collected, not fatal.
Use sparingly — prefer torque_task_bulk_transition to "abandoned" for audit-preserving batch closure (reachable from any status in one call). torque_task_delete for a single task.
Response shape: data = {succeeded: [id...], failed: [{id, error: {code, message, field}}...]} — partial success is not an error; ok=true even when some ids fail. error.code uses the same taxonomy as single-item torque_task_delete.
Example: {"ids":"[\"T-1\",\"T-2\"]"}`),
		mcp.WithString("ids", mcp.Required(), mcp.Description("JSON array of task IDs")),
	), a.handleTaskBulkDelete)

	a.addTool(mcp.NewTool("torque_task_bulk_tag",
		mcp.WithDescription(`Add and/or remove tag slugs across many tasks in one call; per-task failures are collected, not fatal. Unlike torque_task_update's tags field (which replaces the full set), this adds/removes only the slugs you name — every other tag on each task is left alone.
Use for cohort-wide tag maintenance (e.g. tag a sprint's tasks "p0", or strip "wip" once review starts); prefer torque_task_update tags for replacing one task's full tag set.
Response shape: data = {succeeded: [id...], failed: [{id, error: {code, message, field}}...]} — partial success is not an error; ok=true even when some ids fail.
Example: {"ids":"[\"T-1\",\"T-2\"]","add":"[\"p0\"]","remove":"[\"wip\"]"}`),
		mcp.WithString("ids", mcp.Required(), mcp.Description("JSON array of task IDs")),
		mcp.WithString("add", mcp.Description("JSON array of tag names/slugs to add (auto-creates unknown tags, same as torque_task_update)")),
		mcp.WithString("remove", mcp.Description("JSON array of tag slugs to remove (unknown/unlinked slugs are a no-op)")),
	), a.handleTaskBulkTag)
}

// bulkFailure is the wire shape for one failed item inside a bulk verb's
// `failed[]` array. Its Error field is the same ErrorInfo shape single-item
// operations return via errResult, so agents parsing a bulk response reuse
// the exact error.code handling they already have for single-item calls.
type bulkFailure struct {
	ID    string     `json:"id"`
	Error *ErrorInfo `json:"error"`
}

// bulkResult shapes a service-layer RunBulk result (succeeded IDs + typed
// per-item errors) into the canonical
// `{succeeded: [...], failed: [{id, error}]}` envelope (ADR-0004 §3,
// PRIM-003). Every bulk_* tool — bulk_update, bulk_delete, bulk_tag here,
// and any future entity's bulk verbs in Phase 4 — must funnel its response
// through this helper instead of hand-rolling a bespoke bulk shape, so
// agents see one identical response regardless of entity or verb.
//
// succeeded and the failed slice are always non-nil (possibly empty) in the
// response so callers don't need a nil check. Each failure's error is run
// through mapServiceError — the exact taxonomy path errFromService uses for
// single-item handlers — so a bulk failure's error.code always matches what
// the same underlying operation would have returned as a single-item call.
//
// Bulk operations never set CallToolResult.IsError: partial success is not
// a call-level failure (ADR-0004 §3, "partial success is not an error
// state") — ok=true even when failed is non-empty. Callers must inspect
// failed[] themselves. Only malformed call-level args (bad ids JSON, empty
// ids, etc.) are call-level errors and are returned via errResult before
// bulkResult is ever reached.
func bulkResult(succeeded []string, failed []service.BulkItemError) (*mcp.CallToolResult, error) {
	if succeeded == nil {
		succeeded = []string{}
	}
	out := make([]bulkFailure, 0, len(failed))
	for _, f := range failed {
		code, msg, field := mapServiceError(f.Err)
		out = append(out, bulkFailure{
			ID:    f.ID,
			Error: &ErrorInfo{Code: code, Message: msg, Field: field},
		})
	}
	return okResult(map[string]any{
		"succeeded": succeeded,
		"failed":    out,
	})
}

// reqIDs extracts and validates the required `ids` JSON-array argument
// shared by every bulk_* tool. Returns a call-level errResult (not a
// per-item failure) when ids is missing, malformed, or empty — an empty
// ids[] means there is nothing to do, which is a caller mistake, not a
// partial-success case.
func reqIDs(req mcp.CallToolRequest) ([]string, *mcp.CallToolResult) {
	ids, err := reqStrSlice(req, "ids")
	if err != nil {
		res, _ := errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid ids JSON: %v", err), "ids")
		return nil, res
	}
	if len(ids) == 0 {
		res, _ := errResult(ErrCodeArgInvalid, "ids must be a non-empty JSON array", "ids")
		return nil, res
	}
	return ids, nil
}

func (a *Adapter) handleTaskBulkUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ids, errRes := reqIDs(req)
	if errRes != nil {
		return errRes, nil
	}
	input, errRes := buildTaskUpdateInput(req)
	if errRes != nil {
		return errRes, nil
	}

	succeeded, failed := a.svc.Task.BulkUpdate(ids, input)
	return bulkResult(succeeded, failed)
}

func (a *Adapter) handleTaskBulkDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ids, errRes := reqIDs(req)
	if errRes != nil {
		return errRes, nil
	}

	succeeded, failed := a.svc.Task.BulkDelete(ids)
	return bulkResult(succeeded, failed)
}

func (a *Adapter) handleTaskBulkTag(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ids, errRes := reqIDs(req)
	if errRes != nil {
		return errRes, nil
	}

	add, err := reqStrSlice(req, "add")
	if err != nil {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid add JSON: %v", err), "add")
	}
	remove, err := reqStrSlice(req, "remove")
	if err != nil {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid remove JSON: %v", err), "remove")
	}
	if len(add) == 0 && len(remove) == 0 {
		return errResult(ErrCodeArgInvalid, "at least one of add/remove must be a non-empty JSON array", "add")
	}

	succeeded, failed := a.svc.Task.BulkTag(ids, add, remove)
	return bulkResult(succeeded, failed)
}
