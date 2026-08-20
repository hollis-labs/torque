package mcpadapter

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerSubtodoTools() {
	a.addTool(mcp.NewTool("torque_task_subtodo_list",
		mcp.WithDescription(`List the structural checklist (subtodos) on a task. Brief shape drops the evidence column; pass verbose="true" for full records.
Use to inspect gating state; sibling torque_task_subtodo_add/torque_task_subtodo_done mutate. Unlike comments, subtodos drive lifecycle (required items block done).
Response shape: data = {items: [<briefSubtodo or Subtodo>...], meta: {truncated, returned, limit, hint?}}.
Example: {"task_id":"T-123"}`),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
	), a.handleSubtodoList)

	a.addTool(mcp.NewTool("torque_task_subtodo_add",
		mcp.WithDescription(`Append a subtodo item to a task's checklist. required=true means the task cannot transition past review until the item is ticked off.
Use for structural gating; torque_task_subtodo_done marks completion, torque_task_subtodo_list reads. Prefer torque_comment_add for non-gating discussion.
Response shape: data = [<Subtodo>...] — returns the updated full checklist.
Example: {"task_id":"T-123","id":"check-1","text":"write regression test","required":true}
Example, id omitted (server generates one): {"task_id":"T-123","text":"write regression test","required":true}`),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("id", mcp.Description("Item id (unique per task). Omit to auto-generate a server-assigned id; supply a meaningful slug (e.g. \"check-auth-flow\") to keep it deterministic across repeated emits")),
		mcp.WithString("text", mcp.Required(), mcp.Description("Human-readable description")),
		mcp.WithBoolean("required", mcp.Description("If true, blocks done until ticked off (default false)")),
	), a.handleSubtodoAdd)

	a.addTool(mcp.NewTool("torque_task_subtodo_bulk_add",
		mcp.WithDescription(`Seed a whole checklist onto one task in a single call: each item in items[] gets the same id rule as torque_task_subtodo_add (supply a meaningful slug, or omit id to auto-generate one). Per-item failures (e.g. a duplicate id) are collected, not fatal — the rest of the batch still lands.
Use to bootstrap a task's checklist in one round-trip; torque_task_subtodo_add for a single follow-up item, torque_task_subtodo_list to read the result back.
Response shape: data = {succeeded: [id...], failed: [{id, error: {code, message, field}}...]} — partial success is not an error; ok=true even when some items fail. succeeded holds each landed item's resolved id (caller-supplied or generated), not an echo of the input — this differs from torque_task_bulk_update/_delete/_tag, whose succeeded/failed key off ids the caller already had; here the id is being assigned, not looked up. A failed item's "id" is its caller-supplied id when given, else a positional placeholder ("item[1]") since no id was ever assigned. error.code uses the same taxonomy (arg_invalid/not_found/conflict/domain/permission/internal) as single-item torque_task_subtodo_add.
Example: {"task_id":"T-123","items":"[{\"id\":\"check-1\",\"text\":\"write regression test\",\"required\":true},{\"text\":\"update docs\"}]"}`),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("items", mcp.Required(), mcp.Description("JSON array of {id?, text, required?} subtodo specs — id optional (auto-generated when omitted), text required, required defaults to false")),
	), a.handleSubtodoBulkAdd)

	a.addTool(mcp.NewTool("torque_task_subtodo_done",
		mcp.WithDescription(`Mark a subtodo done with an evidence string (artifact id, commit SHA, URL, or note).
Use to unblock required subtodos; torque_task_subtodo_add to create, torque_task_subtodo_list to inspect, torque_task_subtodo_update to edit, torque_task_subtodo_delete to remove.
Response shape: data = [<Subtodo>...] — returns the updated full checklist.
Example: {"task_id":"T-123","id":"check-1","evidence":"abc123 / PR #42"}`),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("id", mcp.Required(), mcp.Description("Item id to mark done")),
		mcp.WithString("evidence", mcp.Description("Optional evidence pointer (artifact id, commit, URL, or note)")),
	), a.handleSubtodoDone)

	a.addTool(mcp.NewTool("torque_task_subtodo_update",
		mcp.WithDescription(`Edit the text and/or required flag on an existing subtodo. Omitted fields are left unchanged.
Use for typo fixes or required-flag adjustments; torque_task_subtodo_done to tick off, torque_task_subtodo_delete to remove entirely.
Response shape: data = [<Subtodo>...] — returns the updated full checklist.
Example: {"task_id":"T-123","id":"check-1","text":"write integration test","required":true}`),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("id", mcp.Required(), mcp.Description("Item id to edit")),
		mcp.WithString("text", mcp.Description("New text (omit to leave unchanged)")),
		mcp.WithBoolean("required", mcp.Description("New required flag (omit to leave unchanged)")),
	), a.handleSubtodoUpdate)

	a.addTool(mcp.NewTool("torque_task_subtodo_delete",
		mcp.WithDescription(`Remove a subtodo item from a task's checklist. Returns the updated checklist (which may be empty).
Use sparingly — prefer torque_task_subtodo_done with evidence for closure that preserves the audit trail. Delete is for items that should not have been added in the first place.
Response shape: data = [<Subtodo>...] — returns the updated full checklist.
Example: {"task_id":"T-123","id":"check-1"}`),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("id", mcp.Required(), mcp.Description("Item id to remove")),
	), a.handleSubtodoDelete)
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

// subtodoAddSpec is the wire shape of one items[] entry for
// torque_task_subtodo_bulk_add — deliberately narrower than sqlstore.Subtodo
// (no done/evidence) so bulk_add exposes exactly the field set
// torque_task_subtodo_add does, just repeated per item.
type subtodoAddSpec struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Required bool   `json:"required"`
}

func (a *Adapter) handleSubtodoBulkAdd(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	taskID := reqStr(req, "task_id")

	raw := reqStr(req, "items")
	if raw == "" {
		return errResult(ErrCodeArgInvalid, "items must be a non-empty JSON array", "items")
	}
	var specs []subtodoAddSpec
	if err := json.Unmarshal([]byte(raw), &specs); err != nil {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid items JSON: %v", err), "items")
	}
	if len(specs) == 0 {
		return errResult(ErrCodeArgInvalid, "items must be a non-empty JSON array", "items")
	}

	items := make([]sqlstore.Subtodo, len(specs))
	for i, spec := range specs {
		items[i] = sqlstore.Subtodo{ID: spec.ID, Text: spec.Text, Required: spec.Required}
	}

	succeeded, failed := a.svc.Task.BulkAddSubtodo(taskID, items)
	return bulkResult(succeeded, failed)
}

func (a *Adapter) handleSubtodoDone(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	items, err := a.svc.Task.MarkSubtodoDone(reqStr(req, "task_id"), reqStr(req, "id"), reqStr(req, "evidence"))
	if err != nil {
		return errFromService(err)
	}
	return okResult(items)
}

func (a *Adapter) handleSubtodoUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Optional fields use pointer-or-nil to distinguish "omitted" from "set to
	// empty/false". reqHasArg checks presence; the typed reader fills the
	// pointer only when the caller actually supplied the field.
	var text *string
	if reqHasArg(req, "text") {
		v := reqStr(req, "text")
		text = &v
	}
	var required *bool
	if reqHasArg(req, "required") {
		v := reqBool(req, "required")
		required = &v
	}
	items, err := a.svc.Task.UpdateSubtodo(reqStr(req, "task_id"), reqStr(req, "id"), text, required)
	if err != nil {
		return errFromService(err)
	}
	return okResult(items)
}

func (a *Adapter) handleSubtodoDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	items, err := a.svc.Task.DeleteSubtodo(reqStr(req, "task_id"), reqStr(req, "id"))
	if err != nil {
		return errFromService(err)
	}
	return okResult(items)
}
