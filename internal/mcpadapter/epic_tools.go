package mcpadapter

import (
	"context"
	"fmt"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

func (a *Adapter) registerEpicTools() {
	a.addTool(newTool("torque_epic_create",
		withDescription(`Create an epic (feature-flagged: requires features.epics). Returns the EpicRecord.
Use to group multiple sprints under one multi-sprint initiative; sibling torque_sprint_create for short-cycle cohorts, torque_project_create for repo-level grouping.
Response shape: data = {<EpicRecord fields>} — singleton.
Example: {"name":"Auth Overhaul","description":"Replace entire auth stack","priority":"1"}`),
		withString("name", required(), desc("Epic name")),
		withString("description", desc("Epic description")),
		withString("priority", desc("Priority (integer; lower typically means higher priority, no enforced range). A non-integer value returns error.code=arg_invalid.")),
		withString("project_id", desc("Project ID to associate this epic with (requires features.projects)")),
	), a.handleEpicCreate)

	a.addTool(newTool("torque_epic_get",
		withDescription(`Fetch an epic's full record by ID.
Use when you know the ID; torque_epic_list for browsing, torque_task_list with epic_id filter for the epic's task set.
Response shape: data = {<EpicRecord fields>} — singleton.
Example: {"id":"EP-4"}`),
		withString("id", required(), desc("Epic ID")),
	), a.handleEpicGet)

	a.addTool(newTool("torque_epic_update",
		withDescription(`Partial update of epic fields or status.
Use for edits or active<->inactive transitions. No dedicated epic approval flow — close via status=inactive.
Response shape: data = {id, updated: bool, message}.
Example: {"id":"EP-4","status":"inactive"}`),
		withString("id", required(), desc("Epic ID")),
		withString("name", desc("New name")),
		withString("description", desc("New description")),
		withString("status", desc("New status: active|inactive")),
		withString("priority", desc("New priority (integer; lower typically means higher priority, no enforced range). A non-integer value returns error.code=arg_invalid and leaves the stored priority unchanged.")),
		withString("project_id", desc("Project ID to associate this epic with (requires features.projects); pass empty string to clear")),
	), a.handleEpicUpdate)

	a.addTool(newTool("torque_epic_delete",
		withDescription(`Hard-delete an epic; linked tasks have epic_id cleared.
Prefer torque_epic_update status=inactive for audit, or torque_epic_archive to soft-delete while preserving the row. Similar surfaces: torque_sprint_delete, torque_project_delete.
Response shape: data = {id, deleted: true, message}.
Example: {"id":"EP-4"}`),
		withString("id", required(), desc("Epic ID")),
	), a.handleEpicDelete)

	a.addTool(newTool("torque_epic_archive",
		withDescription(`Archive an epic (soft-delete; preserves the row and audit trail). Independent of status — archiving isn't the same fact as the epic being "done".
Excluded from torque_epic_list by default; pass include_archived="true" to surface it. Prefer torque_epic_delete only when the row should be gone permanently.
Response shape: data = {id, archived: true, message}.
Example: {"id":"EP-4"}`),
		withString("id", required(), desc("Epic ID")),
	), a.handleEpicArchive)

	a.addTool(newTool("torque_epic_unarchive",
		withDescription(`Restore a previously archived epic back to normal visibility. Does not change status.
Use after torque_epic_archive when the epic needs to reappear in torque_epic_list's default (non-include_archived) results.
Response shape: data = {id, unarchived: true, message}.
Example: {"id":"EP-4"}`),
		withString("id", required(), desc("Epic ID")),
	), a.handleEpicUnarchive)

	a.addTool(newTool("torque_epic_list",
		withDescription(`List epics with optional status/project filters and free-text search, ordered updated_at DESC by default. Pass sort_by (name|status|updated_at|created_at) and sort_dir (asc|desc) to change order; an unrecognized value returns error.code=arg_invalid.
Use for browsing, filtered cohorts, or free-text discovery in one call (no separate _search tool); torque_epic_get when you know the ID. Default brief shape; pass verbose="true" for full records.
Cursor pagination: pass the previous call's meta.next_cursor back as cursor to fetch the next page; meta.next_cursor is null once exhausted. A cursor is only valid for the exact sort_by/sort_dir it was issued under — pass a different sort_by/sort_dir without dropping cursor and you get error.code=arg_invalid.
Explicit malformed, blank, fractional, overflow, unsafe native-float, or negative limit values reject with error.code=arg_invalid, field=limit; omitted limit defaults to 100 and oversized limits clamp to 500.
Response shape: data = {items: [<briefEpic or EpicRecord>...], meta: {truncated, returned, limit, has_more, next_cursor}}.
Example: {"status":"active","search":"auth","limit":"50","sort_by":"updated_at","sort_dir":"desc"}`),
		withString("status", desc("Filter: active|inactive")),
		withString("project_id", desc("Filter by project ID (requires features.projects)")),
		withString("search", desc("Substring match on id + name + description (case-insensitive)")),
		withString("include_archived", desc("Include archived rows. Default false: archived epics are hidden unless requested. Accepts 'true'/'1'/'yes'.")),
		withString("limit", desc("Max results (integer, default 100, max 500)")),
		withString("verbose", desc("Return full records instead of brief (string 'true'/'false', default false)")),
		withString("sort_by", desc("Sort field: name|status|updated_at|created_at (default updated_at)")),
		withString("sort_dir", desc("Sort direction: asc|desc (default desc)")),
		withString("cursor", desc("Opaque pagination cursor from a previous call's meta.next_cursor; omit for the first page. Must match this call's sort_by/sort_dir.")),
	), a.handleEpicList)

	a.addTool(newTool("torque_epic_bulk_update",
		withDescription(`Apply the same partial update to many epics in one call; per-epic failures are collected, not fatal. Same field set and semantics as torque_epic_update.
Use for batch field edits (e.g. re-priority or re-home a cohort under a project); prefer torque_epic_update for a single epic.
Response shape: data = {succeeded: [id...], failed: [{id, error: {code, message, field}}...]} — partial success is not an error; ok=true even when some ids fail. error.code uses the same taxonomy (arg_invalid/not_found/conflict/domain/permission/internal) as single-item torque_epic_update.
Example: {"ids":"[\"EP-1\",\"EP-2\"]","status":"inactive"}`),
		withString("ids", required(), desc("JSON array of epic IDs")),
		withString("name", desc("New name")),
		withString("description", desc("New description")),
		withString("status", desc("New status: active|inactive")),
		withString("priority", desc("New priority (integer; lower typically means higher priority, no enforced range). A non-integer value returns error.code=arg_invalid and leaves the stored priority unchanged.")),
		withString("project_id", desc("Project ID to associate every epic with (requires features.projects); pass empty string to clear")),
	), a.handleEpicBulkUpdate)
}

func (a *Adapter) handleEpicCreate(ctx context.Context, req map[string]any) (any, error) {
	priority, errRes := reqEpicPriorityPtr(req)
	if errRes != nil {
		return nil, errRes
	}

	input := service.EpicCreateInput{
		Name:        reqStr(req, "name"),
		Description: reqStr(req, "description"),
		Priority:    priority,
		ProjectID:   reqStr(req, "project_id"),
	}

	epic, err := a.svc.Epic.Create(input)
	if err != nil {
		return errFromService(err)
	}
	return okResult(epic)
}

func (a *Adapter) handleEpicGet(ctx context.Context, req map[string]any) (any, error) {
	epic, err := a.svc.Epic.Get(reqStr(req, "id"))
	if err != nil {
		return errFromService(err)
	}
	return okResult(epic)
}

// reqEpicPriorityPtr parses the epic "priority" MCP param (an integer
// string) into a *int64 for EpicCreateInput/EpicUpdateInput.Priority.
// Presence-based (mirrors FIX-001's task field-detection fix) so an
// explicit priority="0" is distinguishable from the param being omitted
// entirely — a value-based `if v != 0` check would silently drop a
// caller's intentional priority=0.
func reqEpicPriorityPtr(req map[string]any) (*int64, error) {
	n, present, errRes := reqPriorityArg(req)
	if !present {
		return nil, nil
	}
	if errRes != nil {
		return nil, errRes
	}
	v := int64(n)
	return &v, nil
}

// buildEpicUpdateInput turns a torque_epic_update-shaped request's
// arguments into a service.EpicUpdateInput plus whether any field was
// actually set. All fields are presence-based (SWEEP-001: name/description/
// status previously used value-based detection — an empty string meant "not
// provided" — which silently dropped an explicit clear, the same bug class
// FIX-001 fixed for Task; status="" and name="" are now rejected downstream
// by EpicService.Update's validation instead of being silently ignored).
// Shared by handleEpicUpdate (single-id) and handleEpicBulkUpdate (PRIM-003)
// so single- and bulk-update can never drift apart on semantics — mirrors
// Task's buildTaskUpdateInput, including its third return: a non-nil result
// is a validation failure the caller must return as-is without inspecting
// the (zero-value) EpicUpdateInput.
func buildEpicUpdateInput(req map[string]any) (service.EpicUpdateInput, bool, error) {
	input := service.EpicUpdateInput{}
	hasUpdate := false

	if reqHasArg(req, "name") {
		v := reqStr(req, "name")
		input.Name = &v
		hasUpdate = true
	}
	if reqHasArg(req, "description") {
		v := reqStr(req, "description")
		input.Description = &v
		hasUpdate = true
	}
	if reqHasArg(req, "status") {
		v := reqStr(req, "status")
		input.Status = &v
		hasUpdate = true
	}
	p, errRes := reqEpicPriorityPtr(req)
	if errRes != nil {
		return service.EpicUpdateInput{}, false, errRes
	}
	if p != nil {
		input.Priority = p
		hasUpdate = true
	}
	if reqHasArg(req, "project_id") {
		v := reqStr(req, "project_id")
		input.ProjectID = &v
		hasUpdate = true
	}

	return input, hasUpdate, nil
}

func (a *Adapter) handleEpicUpdate(ctx context.Context, req map[string]any) (any, error) {
	id := reqStr(req, "id")
	input, hasUpdate, errRes := buildEpicUpdateInput(req)
	if errRes != nil {
		return nil, errRes
	}

	if !hasUpdate {
		return okResult(map[string]any{
			"id":      id,
			"updated": false,
			"message": "No changes specified",
		})
	}

	if err := a.svc.Epic.Update(id, input); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"id":      id,
		"updated": true,
		"message": fmt.Sprintf("Epic %s updated", id),
	})
}

func (a *Adapter) handleEpicDelete(ctx context.Context, req map[string]any) (any, error) {
	id := reqStr(req, "id")
	if err := a.svc.Epic.Delete(id); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"id":      id,
		"deleted": true,
		"message": fmt.Sprintf("Epic %s deleted", id),
	})
}

// handleEpicArchive/handleEpicUnarchive apply PRIM-004's already-existing
// EpicService.Archive/Unarchive (built by PRIM-004, internal/service/epic.go)
// to the MCP surface, mirroring torque_collection_archive/unarchive's
// response shape (internal/mcpadapter/collection_tools.go) — the reference
// pattern PRIM-004 itself was built against.
func (a *Adapter) handleEpicArchive(ctx context.Context, req map[string]any) (any, error) {
	id := reqStr(req, "id")
	if err := a.svc.Epic.Archive(id); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"id":       id,
		"archived": true,
		"message":  fmt.Sprintf("Epic %s archived", id),
	})
}

func (a *Adapter) handleEpicUnarchive(ctx context.Context, req map[string]any) (any, error) {
	id := reqStr(req, "id")
	if err := a.svc.Epic.Unarchive(id); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"id":         id,
		"unarchived": true,
		"message":    fmt.Sprintf("Epic %s unarchived", id),
	})
}

func (a *Adapter) handleEpicList(ctx context.Context, req map[string]any) (any, error) {
	verbose, errRes := reqQueryBool(req, "verbose")
	if errRes != nil {
		return nil, errRes
	}
	status, errRes := reqQueryString(req, "status")
	if errRes != nil {
		return nil, errRes
	}
	projectID, errRes := reqQueryString(req, "project_id")
	if errRes != nil {
		return nil, errRes
	}
	search, errRes := reqQueryString(req, "search")
	if errRes != nil {
		return nil, errRes
	}
	includeArchived, errRes := reqQueryBool(req, "include_archived")
	if errRes != nil {
		return nil, errRes
	}
	cursor, errRes := reqQueryCursor(req)
	if errRes != nil {
		return nil, errRes
	}

	input, normalized, err := service.NormalizeEpicQuery(service.EpicQuery{
		Status:          status,
		ProjectID:       projectID,
		Search:          search,
		IncludeArchived: includeArchived,
		CursorQuery:     cursor,
	})
	if err != nil {
		return errFromService(err)
	}
	epics, err := a.svc.Epic.ListPaginated(input)
	if err != nil {
		return errFromService(err)
	}

	hasMoreFromQuery := len(epics) > normalized.Limit
	if hasMoreFromQuery {
		epics = epics[:normalized.Limit]
	}
	return epicListCursorEnvelope(epics, normalized.Limit, verbose, normalized.SortBy, normalized.SortDir, hasMoreFromQuery)
}

// epicListCursorEnvelope builds torque_epic_list's {items, meta} cursor-
// pagination response — Epic's analog to taskListCursorEnvelope
// (task_tools.go). epics must already be trimmed to at most `limit`
// records — handleEpicList over-fetches limit+1 to compute
// hasMoreFromQuery, then drops the extra row before calling this.
func epicListCursorEnvelope(epics []sqlstore.EpicRecord, limit int, verbose bool, sortBy, sortDir string, hasMoreFromQuery bool) (any, error) {
	items := make([]any, 0, len(epics))
	for _, e := range epics {
		if verbose {
			items = append(items, e)
		} else {
			items = append(items, toBriefEpic(e))
		}
	}

	cursorAt := func(i int) (sortValue, id string) {
		return service.EpicQuerySortValue(epics[i], sortBy), epics[i].ID
	}
	return cappedCursorJSONResult(items, limit, sortBy, sortDir, hasMoreFromQuery, cursorAt)
}

func (a *Adapter) handleEpicBulkUpdate(ctx context.Context, req map[string]any) (any, error) {
	ids, errRes := reqIDs(req)
	if errRes != nil {
		return nil, errRes
	}
	input, hasUpdate, errRes := buildEpicUpdateInput(req)
	if errRes != nil {
		return nil, errRes
	}
	if !hasUpdate {
		return errResult(ErrCodeArgInvalid, "at least one updatable field must be provided", "")
	}

	succeeded, failed := a.svc.Epic.BulkUpdate(ids, input)
	return bulkResult(succeeded, failed)
}
