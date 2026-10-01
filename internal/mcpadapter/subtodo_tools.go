package mcpadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/hollis-labs/torque/internal/service/pagination"
	"strconv"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

func (a *Adapter) registerSubtodoTools() {
	a.addTool(newTool("torque_task_subtodo_list",
		withDescription(`List the structural checklist (subtodos) on a task. Brief shape drops the evidence column; pass verbose="true" for full records.
Use to inspect gating state; sibling torque_task_subtodo_add/torque_task_subtodo_done mutate. Unlike comments, subtodos drive lifecycle (required items block done).
Cursor pages default to 50, max 200, in checklist position ASC order. Pass next_cursor with the same task_id and sort. Byte-cap pages include a concrete continuation call.
Response shape: data = {items: [<briefSubtodo or Subtodo>...], meta: {truncated, returned, limit, has_more, next_cursor, hint?}}.
Example: {"task_id":"T-123"}`),
		withString("task_id", required(), desc("Task ID")),
		withString("limit", desc("Page size, default 50, max 200")),
		withString("cursor", desc("Opaque next_cursor from the previous page")),
		withString("sort_by", desc("Checklist position (position, default)")),
		withString("sort_dir", desc("asc (default) or desc")),
		withString("verbose", desc("Return full records instead of brief (string 'true'/'false', default false)")),
	), a.handleSubtodoList)

	a.addTool(newTool("torque_task_subtodo_add",
		withDescription(`Append a subtodo item to a task's checklist. required=true means the task cannot transition past review until the item is ticked off.
Use for structural gating; torque_task_subtodo_done marks completion, torque_task_subtodo_list reads. Prefer torque_comment_add for non-gating discussion.
Response shape: data = [<Subtodo>...] — returns the updated full checklist.
Example: {"task_id":"T-123","id":"check-1","text":"write regression test","required":true}
Example, id omitted (server generates one): {"task_id":"T-123","text":"write regression test","required":true}`),
		withString("task_id", required(), desc("Task ID")),
		withString("id", desc("Item id (unique per task). Omit to auto-generate a server-assigned id; supply a meaningful slug (e.g. \"check-auth-flow\") to keep it deterministic across repeated emits")),
		withString("text", required(), desc("Human-readable description")),
		withBoolean("required", desc("If true, blocks done until ticked off (default false)")),
	), a.handleSubtodoAdd)

	a.addTool(newTool("torque_task_subtodo_bulk_add",
		withDescription(`Seed a whole checklist onto one task in a single call: each item in items[] gets the same id rule as torque_task_subtodo_add (supply a meaningful slug, or omit id to auto-generate one). Per-item failures (e.g. a duplicate id) are collected, not fatal — the rest of the batch still lands.
Use to bootstrap a task's checklist in one round-trip; torque_task_subtodo_add for a single follow-up item, torque_task_subtodo_list to read the result back.
Response shape: data = {succeeded: [id...], failed: [{id, error: {code, message, field}}...]} — partial success is not an error; ok=true even when some items fail. succeeded holds each landed item's resolved id (caller-supplied or generated), not an echo of the input — this differs from torque_task_bulk_update/_delete/_tag, whose succeeded/failed key off ids the caller already had; here the id is being assigned, not looked up. A failed item's "id" is its caller-supplied id when given, else a positional placeholder ("item[1]") since no id was ever assigned. error.code uses the same taxonomy (arg_invalid/not_found/conflict/domain/permission/internal) as single-item torque_task_subtodo_add.
Example: {"task_id":"T-123","items":"[{\"id\":\"check-1\",\"text\":\"write regression test\",\"required\":true},{\"text\":\"update docs\"}]"}`),
		withString("task_id", required(), desc("Task ID")),
		withString("items", required(), desc("JSON array of {id?, text, required?} subtodo specs — id optional (auto-generated when omitted), text required, required defaults to false")),
	), a.handleSubtodoBulkAdd)

	a.addTool(newTool("torque_task_subtodo_done",
		withDescription(`Mark a subtodo done with an evidence string (artifact id, commit SHA, URL, or note).
Use to unblock required subtodos; torque_task_subtodo_add to create, torque_task_subtodo_list to inspect, torque_task_subtodo_update to edit, torque_task_subtodo_delete to remove.
Response shape: data = [<Subtodo>...] — returns the updated full checklist.
Example: {"task_id":"T-123","id":"check-1","evidence":"abc123 / PR #42"}`),
		withString("task_id", required(), desc("Task ID")),
		withString("id", required(), desc("Item id to mark done")),
		withString("evidence", desc("Optional evidence pointer (artifact id, commit, URL, or note)")),
	), a.handleSubtodoDone)

	a.addTool(newTool("torque_task_subtodo_update",
		withDescription(`Edit the text and/or required flag on an existing subtodo. Omitted fields are left unchanged.
Use for typo fixes or required-flag adjustments; torque_task_subtodo_done to tick off, torque_task_subtodo_delete to remove entirely.
Response shape: data = [<Subtodo>...] — returns the updated full checklist.
Example: {"task_id":"T-123","id":"check-1","text":"write integration test","required":true}`),
		withString("task_id", required(), desc("Task ID")),
		withString("id", required(), desc("Item id to edit")),
		withString("text", desc("New text (omit to leave unchanged)")),
		withBoolean("required", desc("New required flag (omit to leave unchanged)")),
	), a.handleSubtodoUpdate)

	a.addTool(newTool("torque_task_subtodo_delete",
		withDescription(`Remove a subtodo item from a task's checklist. Returns the updated checklist (which may be empty).
Use sparingly — prefer torque_task_subtodo_done with evidence for closure that preserves the audit trail. Delete is for items that should not have been added in the first place.
Response shape: data = [<Subtodo>...] — returns the updated full checklist.
Example: {"task_id":"T-123","id":"check-1"}`),
		withString("task_id", required(), desc("Task ID")),
		withString("id", required(), desc("Item id to remove")),
	), a.handleSubtodoDelete)
}

func (a *Adapter) handleSubtodoList(ctx context.Context, req map[string]any) (any, error) {
	if err := validateTaskListStringArgs(req, "task_id", "cursor", "sort_by", "sort_dir"); err != nil {
		return nil, err
	}
	rawLimit, _, err := reqTaskListInt(req, "limit")
	if err != nil {
		return nil, argError(ErrCodeArgInvalid, err.Error(), "limit")
	}
	if rawLimit < 0 {
		return nil, argError(ErrCodeArgInvalid, "limit must be positive", "limit")
	}
	limit := clampLimit(rawLimit, pagination.DefaultLimit, pagination.MaxLimit)
	verbose, err := reqTaskListBool(req, "verbose")
	if err != nil {
		return nil, argError(ErrCodeArgInvalid, err.Error(), "verbose")
	}
	sortBy, sortDir, value, afterID, err := resolveSortAndCursor(req, "position", "asc", "position")
	if err != nil {
		return nil, err
	}
	after := -1
	if afterID != "" {
		after, err = strconv.Atoi(value)
		if err != nil || after < 0 {
			return nil, argError(ErrCodeArgInvalid, "invalid checklist cursor position", "cursor")
		}
	}
	items, err := a.svc.Task.ListSubtodos(reqStr(req, "task_id"))
	if err != nil {
		return errFromService(err)
	}
	positions := make([]int, 0, len(items))
	for i := range items {
		if afterID != "" && ((sortDir == "asc" && i <= after) || (sortDir == "desc" && i >= after)) {
			continue
		}
		positions = append(positions, i)
	}
	if sortDir == "desc" {
		for i, j := 0, len(positions)-1; i < j; i, j = i+1, j-1 {
			positions[i], positions[j] = positions[j], positions[i]
		}
	}
	more := len(positions) > limit
	if more {
		positions = positions[:limit]
	}
	out := make([]any, 0, len(positions))
	for _, i := range positions {
		if verbose {
			out = append(out, items[i])
		} else {
			out = append(out, toBriefSubtodo(items[i]))
		}
	}
	return cappedCursorJSONResult(out, limit, sortBy, sortDir, more, func(i int) (string, string) { index := positions[i]; return strconv.Itoa(index), items[index].ID })
}

func (a *Adapter) handleSubtodoAdd(ctx context.Context, req map[string]any) (any, error) {
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

func (a *Adapter) handleSubtodoBulkAdd(ctx context.Context, req map[string]any) (any, error) {
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

func (a *Adapter) handleSubtodoDone(ctx context.Context, req map[string]any) (any, error) {
	items, err := a.svc.Task.MarkSubtodoDone(reqStr(req, "task_id"), reqStr(req, "id"), reqStr(req, "evidence"))
	if err != nil {
		return errFromService(err)
	}
	return okResult(items)
}

func (a *Adapter) handleSubtodoUpdate(ctx context.Context, req map[string]any) (any, error) {
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

func (a *Adapter) handleSubtodoDelete(ctx context.Context, req map[string]any) (any, error) {
	items, err := a.svc.Task.DeleteSubtodo(reqStr(req, "task_id"), reqStr(req, "id"))
	if err != nil {
		return errFromService(err)
	}
	return okResult(items)
}
