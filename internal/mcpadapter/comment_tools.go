package mcpadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerCommentTools() {
	a.addTool(mcp.NewTool("torque_comment_add",
		mcp.WithDescription(`Append a comment (freeform prose) to an entity; returns the persisted CommentRecord with assigned ID.
Comments are polymorphic — entity_type selects which kind of entity the comment is attached to. Supported: "task", "project", "epic", "sprint" (Issue/Plan already covered via entity_type="task" since they're Task rows). An unsupported entity_type returns error.code=arg_invalid. Use for agent-to-user channel, review notes, or blocked-reason explanation; structured audit trails should go in artifacts via torque_artifact_create. Comments never drive lifecycle.
Response shape: data = {<CommentRecord fields>} — singleton.
Example: {"entity_type":"task","entity_id":"T-123","author":"reviewer","content":"Please also cover the null-parent case."}`),
		mcp.WithString("entity_type", mcp.Required(), mcp.Description(`Entity kind the comment is attached to. Valid: "task", "project", "epic", "sprint".`)),
		mcp.WithString("entity_id", mcp.Required(), mcp.Description("ID of the entity (e.g. task ID for entity_type=task)")),
		mcp.WithString("author", mcp.Description(`Comment author slug/id. Optional; omitted stores "" (unattributed), which torque_comment_delete matches by passing author="" explicitly.`)),
		mcp.WithString("content", mcp.Required(), mcp.Description("Comment body (prose). Required for real — empty or whitespace-only content is rejected rather than stored.")),
	), a.handleCommentAdd)

	a.addTool(mcp.NewTool("torque_comment_list",
		mcp.WithDescription(`List comments on one entity — or, via entity_ids, across a caller-resolved SET of entity refs of the same entity_type (e.g. every task in a sprint: first torque_task_list {"sprint_id":...} for the task ids, then pass those as entity_ids here) — ordered created_at ASC (oldest first, tiebreak id ASC) by default. Pass sort_by ("created_at") and sort_dir (asc|desc) to change order; an unrecognized value returns error.code=arg_invalid. Default brief shape includes a 100-char excerpt of the body; pass verbose="true" for full content.
Use to review the discussion thread for a given entity (or set of entities); torque_comment_add to append. For newest-first cross-entity content search use torque_comment_search.
Cursor pagination: pass the previous call's meta.next_cursor back as cursor to fetch the next page; meta.next_cursor is null once exhausted. A cursor is only valid for the exact sort_by/sort_dir it was issued under.
entity_ids accepts a JSON array string or native string array; whole null, [null], non-string members, malformed JSON, and an empty list as the only scope reject. If both entity_id and entity_ids are supplied, entity_id takes precedence. Explicit malformed or negative limit values reject; default/max are 50/200.
Response shape: data = {items: [<briefComment or CommentRecord>...], meta: {truncated, returned, limit, has_more, next_cursor}}.
Example: {"entity_type":"task","entity_id":"T-123","limit":"50"}`),
		mcp.WithString("entity_type", mcp.Required(), mcp.Description(`Entity kind. Valid: "task", "project", "epic", "sprint".`)),
		mcp.WithString("entity_id", mcp.Description("ID of the entity — required unless entity_ids is given instead")),
		mcp.WithString("entity_ids", mcp.Description("JSON array of entity IDs (same entity_type) — OR-matched; alternative to entity_id for querying comments across a resolved set of refs at once")),
		mcp.WithString("author", mcp.Description("Filter by exact author match (optional)")),
		mcp.WithString("created_after", mcp.Description("Only comments created at/after this instant (RFC3339, e.g. 2026-01-02T15:04:05Z)")),
		mcp.WithString("created_before", mcp.Description("Only comments created at/before this instant (RFC3339)")),
		mcp.WithString("limit", mcp.Description("Max results (integer, default 50, max 200)")),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
		mcp.WithString("sort_by", mcp.Description(`Sort field: "created_at" (default, only supported value)`)),
		mcp.WithString("sort_dir", mcp.Description("Sort direction: asc|desc (default asc)")),
		mcp.WithString("cursor", mcp.Description("Opaque pagination cursor from a previous call's meta.next_cursor; omit for the first page. Must match this call's sort_by/sort_dir.")),
	), a.handleCommentList)

	a.addTool(mcp.NewTool("torque_comment_search",
		mcp.WithDescription(`Search comments by content across all entities, optionally scoped to one entity (entity_type + entity_id), a set of entity refs (entity_type + entity_ids), an author, and/or a created_at range. Returns newest first (created_at DESC, tiebreak id ASC) by default; pass sort_by/sort_dir to change order.
Use to discover comments by content; torque_comment_list for per-entity (or per-entity-set) chronological history.
Cursor pagination: pass the previous call's meta.next_cursor back as cursor to fetch the next page; meta.next_cursor is null once exhausted. A cursor is only valid for the exact sort_by/sort_dir it was issued under.
entity_ids accepts a JSON array string or native string array; whole null, [null], non-string members, and malformed JSON reject. If both entity_id and entity_ids are supplied, entity_id takes precedence. Explicit malformed or negative limit values reject; default/max are 25/100.
Response shape: data = {items: [<CommentRecord>...], meta: {truncated, returned, limit, has_more, next_cursor}}.
Example: {"query":"review notes","entity_type":"task","entity_id":"T-123"}`),
		mcp.WithString("query", mcp.Required(), mcp.Description("Substring match on comment content")),
		mcp.WithString("entity_type", mcp.Description(`Restrict to one entity kind (optional). Valid: "task", "project", "epic", "sprint".`)),
		mcp.WithString("entity_id", mcp.Description("Restrict to one entity (optional; usually paired with entity_type)")),
		mcp.WithString("entity_ids", mcp.Description("JSON array of entity IDs (same entity_type) — OR-matched; alternative to entity_id")),
		mcp.WithString("author", mcp.Description("Exact author match (optional)")),
		mcp.WithString("created_after", mcp.Description("Only comments created at/after this instant (RFC3339)")),
		mcp.WithString("created_before", mcp.Description("Only comments created at/before this instant (RFC3339)")),
		mcp.WithString("limit", mcp.Description("Max results (integer, default 25, max 100)")),
		mcp.WithString("sort_by", mcp.Description(`Sort field: "created_at" (default, only supported value)`)),
		mcp.WithString("sort_dir", mcp.Description("Sort direction: asc|desc (default desc)")),
		mcp.WithString("cursor", mcp.Description("Opaque pagination cursor from a previous call's meta.next_cursor; omit for the first page. Must match this call's sort_by/sort_dir.")),
	), a.handleCommentSearch)

	a.addTool(mcp.NewTool("torque_comment_update",
		mcp.WithDescription(`Edit an existing comment's content. Author-scoped: only the comment's original author may edit it — a mismatched author returns error.code=permission. Returns the updated CommentRecord.
Use to correct/amend a comment you posted; torque_comment_delete to remove it entirely. Comments never drive lifecycle — editing content doesn't affect any entity state.
Response shape: data = {<CommentRecord fields>} — singleton.
Example: {"id":"42","author":"reviewer","content":"Updated: please also cover the null-parent case."}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Comment ID (integer; pass as string)")),
		mcp.WithString("author", mcp.Required(), mcp.Description("Caller's author slug/id — must exactly match the comment's original author")),
		mcp.WithString("content", mcp.Required(), mcp.Description("New comment body (prose)")),
	), a.handleCommentUpdate)

	a.addTool(mcp.NewTool("torque_comment_delete",
		mcp.WithDescription(`Hard-delete a comment. By default this uses exact author matching as an accidental-deletion guard: a mismatched author returns error.code=permission. author is caller-supplied text, not an authenticated principal. There is no undo.
Use to remove a comment you posted in error. Pass force=true only when you deliberately want to bypass the author-match guard; force does not bypass invalid IDs or missing-comment errors.
Unattributed comments (author stored as "", which is what an omitted author on torque_comment_add produces) are deleted by passing author="" explicitly. Omitting author entirely is still an error — the empty string has to be deliberate.
Response shape: data = {id, deleted: true}.
Example: {"id":"42","author":"reviewer"}
Example, an unattributed comment: {"id":"42","author":""}
Example, deliberate override: {"id":"42","author":"reviewer","force":true}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Comment ID (integer; pass as string)")),
		mcp.WithString("author", mcp.Required(), mcp.Description(`Caller's author slug/id — must exactly match the comment's original author unless force=true. Pass "" to match an unattributed comment; the field itself must still be present.`)),
		mcp.WithBoolean("force", mcp.Description(`Optional. true deliberately bypasses only the author-match guard; omitted/false preserves exact-author behavior.`)),
	), a.handleCommentDelete)

	a.addTool(mcp.NewTool("torque_comment_bulk_add",
		mcp.WithDescription(`Post the same comment text to many entity refs in one call (e.g. broadcast a note to every task in a sprint); per-target failures are collected, not fatal. Unlike torque_task_bulk_update/delete/tag (which operate on an existing ids[]), this CREATES a new comment per target rather than modifying existing rows.
Use for broadcast notes across a known set of entity refs; torque_comment_add for a single target.
Response shape: data = {succeeded: [<CommentRecord fields>...], failed: [{target: {entity_type, entity_id}, error: {code, message, field}}...]} — partial success is not an error; ok=true even when some targets fail.
Example: {"targets":"[{\"entity_type\":\"task\",\"entity_id\":\"T-1\"},{\"entity_type\":\"task\",\"entity_id\":\"T-2\"}]","author":"reviewer","content":"Sprint review starts in 1 hour."}`),
		mcp.WithString("targets", mcp.Required(), mcp.Description(`JSON array of {"entity_type":...,"entity_id":...} objects naming every entity to comment on`)),
		mcp.WithString("author", mcp.Description("Comment author slug/id")),
		mcp.WithString("content", mcp.Required(), mcp.Description("Comment body (prose), posted identically to every target")),
	), a.handleCommentBulkAdd)
}

// commentContentRequired is the one emptiness check every comment-writing
// tool shares (CW-20260903-0044).
//
// torque_comment_add declared content as required and did not check it: a
// call that omitted it — or passed `body`, a plausible slip for a prose
// field — was accepted, returned ok:true with a real comment ID, and
// persisted a row storing nothing. torque_comment_bulk_add and
// torque_comment_update had always checked. Routing all three through one
// helper is what stops them drifting apart again, and is why the error text
// is defined here rather than repeated at three call sites.
//
// Whitespace-only content counts as empty. A comment of three spaces
// destroys the audit trail exactly as thoroughly as one of none.
func commentContentRequired(content string) *mcp.CallToolResult {
	if strings.TrimSpace(content) != "" {
		return nil
	}
	res, _ := errResult(ErrCodeArgInvalid, "content is required", "content")
	return res
}

func (a *Adapter) handleCommentAdd(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	entityType := reqStr(req, "entity_type")
	entityID := reqStr(req, "entity_id")
	if entityType == "" {
		return errResult(ErrCodeArgInvalid, "entity_type is required", "entity_type")
	}
	if entityID == "" {
		return errResult(ErrCodeArgInvalid, "entity_id is required", "entity_id")
	}
	if res := commentContentRequired(reqStr(req, "content")); res != nil {
		return res, nil
	}
	comment, err := a.svc.Comment.Add(
		entityType,
		entityID,
		reqStr(req, "author"),
		reqStr(req, "content"),
	)
	if err != nil {
		return errFromService(err)
	}
	// comment is typed as *sqlstore.CommentRecord — return the bare record so
	// callers can see the assigned ID / timestamp without a second round-trip.
	return okResult(comment)
}

// commentSortValue preserves timestamp precision in cursor values.
func commentSortValue(c sqlstore.CommentRecord, sortBy string) string {
	return service.CommentQuerySortValue(c, sortBy)
}

// commentCursorID formats a CommentRecord's integer id into the opaque
// cursor's string id field.
func commentCursorID(c sqlstore.CommentRecord) string {
	return service.CommentQueryCursorID(c)
}

// commentListEnvelope builds the shared {items, meta} cursor-pagination
// response for both torque_comment_list and torque_comment_search — see
// taskListCursorEnvelope for the reference pattern this mirrors.
func commentListEnvelope(comments []sqlstore.CommentRecord, limit int, verbose bool, sortBy, sortDir string, hasMoreFromQuery bool) (*mcp.CallToolResult, error) {
	items := make([]any, 0, len(comments))
	for _, c := range comments {
		if verbose {
			items = append(items, c)
		} else {
			items = append(items, toBriefComment(c))
		}
	}
	cursorAt := func(i int) (sortValue, id string) {
		return commentSortValue(comments[i], sortBy), commentCursorID(comments[i])
	}
	return cappedCursorJSONResult(items, limit, sortBy, sortDir, hasMoreFromQuery, cursorAt)
}

func (a *Adapter) handleCommentList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	verbose, errRes := reqQueryBool(req, "verbose")
	if errRes != nil {
		return errRes, nil
	}
	entityType, errRes := reqQueryString(req, "entity_type")
	if errRes != nil {
		return errRes, nil
	}
	if entityType == "" {
		return errResult(ErrCodeArgInvalid, "entity_type is required", "entity_type")
	}
	entityID, errRes := reqQueryString(req, "entity_id")
	if errRes != nil {
		return errRes, nil
	}
	entityIDs, err := reqTaskListOperatorStringSlice(req, "entity_ids")
	if err != nil {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid entity_ids JSON: %v", err), "entity_ids")
	}
	if entityID == "" && len(entityIDs) == 0 {
		return errResult(ErrCodeArgInvalid, "entity_id or entity_ids is required", "entity_id")
	}
	author, errRes := reqQueryString(req, "author")
	if errRes != nil {
		return errRes, nil
	}
	createdAfter, errRes := reqQueryString(req, "created_after")
	if errRes != nil {
		return errRes, nil
	}
	createdBefore, errRes := reqQueryString(req, "created_before")
	if errRes != nil {
		return errRes, nil
	}
	cursor, errRes := reqQueryCursor(req)
	if errRes != nil {
		return errRes, nil
	}

	filter, normalized, err := service.NormalizeCommentListQuery(service.CommentQuery{
		EntityType:    entityType,
		EntityID:      entityID,
		EntityIDs:     entityIDs,
		Author:        author,
		CreatedAfter:  createdAfter,
		CreatedBefore: createdBefore,
		CursorQuery:   cursor,
	})
	if err != nil {
		return errFromService(err)
	}

	comments, err := a.svc.Comment.ListFiltered(filter)
	if err != nil {
		return errFromService(err)
	}

	hasMoreFromQuery := len(comments) > normalized.Limit
	if hasMoreFromQuery {
		comments = comments[:normalized.Limit]
	}
	return commentListEnvelope(comments, normalized.Limit, verbose, normalized.SortBy, normalized.SortDir, hasMoreFromQuery)
}

func (a *Adapter) handleCommentSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, errRes := reqQueryString(req, "query")
	if errRes != nil {
		return errRes, nil
	}
	if query == "" {
		return errResult(ErrCodeArgInvalid, "query is required", "query")
	}
	entityType, errRes := reqQueryString(req, "entity_type")
	if errRes != nil {
		return errRes, nil
	}
	entityID, errRes := reqQueryString(req, "entity_id")
	if errRes != nil {
		return errRes, nil
	}
	entityIDs, err := reqTaskListOperatorStringSlice(req, "entity_ids")
	if err != nil {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid entity_ids JSON: %v", err), "entity_ids")
	}
	author, errRes := reqQueryString(req, "author")
	if errRes != nil {
		return errRes, nil
	}
	createdAfter, errRes := reqQueryString(req, "created_after")
	if errRes != nil {
		return errRes, nil
	}
	createdBefore, errRes := reqQueryString(req, "created_before")
	if errRes != nil {
		return errRes, nil
	}
	cursor, errRes := reqQueryCursor(req)
	if errRes != nil {
		return errRes, nil
	}

	filter, normalized, err := service.NormalizeCommentSearchQuery(service.CommentQuery{
		Search:        query,
		EntityType:    entityType,
		EntityID:      entityID,
		EntityIDs:     entityIDs,
		Author:        author,
		CreatedAfter:  createdAfter,
		CreatedBefore: createdBefore,
		CursorQuery:   cursor,
	})
	if err != nil {
		return errFromService(err)
	}

	comments, err := a.svc.Comment.Search(filter)
	if err != nil {
		return errFromService(err)
	}

	hasMoreFromQuery := len(comments) > normalized.Limit
	if hasMoreFromQuery {
		comments = comments[:normalized.Limit]
	}
	// torque_comment_search has always returned full CommentRecord items
	// (no brief/verbose toggle existed pre-ENT-COMMENT) — preserved as-is;
	// only pagination/sort/filter changed here.
	return commentListEnvelope(comments, normalized.Limit, true, normalized.SortBy, normalized.SortDir, hasMoreFromQuery)
}

func (a *Adapter) handleCommentUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := int64(reqInt(req, "id"))
	if id <= 0 {
		return errResult(ErrCodeArgInvalid, "id must be a positive integer", "id")
	}
	author := reqStr(req, "author")
	if author == "" {
		return errResult(ErrCodeArgInvalid, "author is required", "author")
	}
	content := reqStr(req, "content")
	if res := commentContentRequired(content); res != nil {
		return res, nil
	}
	comment, err := a.svc.Comment.Update(id, author, content)
	if err != nil {
		return errFromService(err)
	}
	return okResult(comment)
}

func (a *Adapter) handleCommentDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := exactInt64(req.GetArguments()["id"], "id")
	if err != nil || id <= 0 {
		return errResult(ErrCodeArgInvalid, "id must be a positive integer", "id")
	}
	// author is required, but an EXPLICIT empty string is a legitimate value:
	// it names the unattributed author (CW-20260903-0044).
	//
	// author is optional on torque_comment_add and an omitted one stores "",
	// which torque_task_transition's description already documents as
	// "empty = unattributed". Delete is author-scoped by exact match, and
	// this handler used to reject "" as missing — so no argument could ever
	// match an unattributed row, and every unattributed comment in the store
	// was permanently unremovable. That is a reachable state created by
	// ordinary use of a documented feature, not only by the empty-content bug
	// this task reported.
	//
	// Distinguishing absent from explicitly-empty is only reliable because
	// presence is read off the argument map; a caller who forgets the field
	// still gets "author is required".
	//
	rawAuthor, supplied := req.GetArguments()["author"]
	if !supplied {
		return errResult(ErrCodeArgInvalid, "author is required", "author")
	}
	author, ok := rawAuthor.(string)
	if !ok {
		return errResult(ErrCodeArgInvalid, "author must be a string", "author")
	}
	force, errRes := reqExactBool(req, "force")
	if errRes != nil {
		return errRes, nil
	}
	if err := a.svc.Comment.Delete(id, author, force); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{"id": id, "deleted": true})
}

// commentBulkTarget is one {entity_type, entity_id} element of
// torque_comment_bulk_add's `targets` JSON array.
type commentBulkTarget struct {
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
}

// commentBulkAddFailure is torque_comment_bulk_add's per-target failure
// shape — keyed by the target itself (entity_type + entity_id) rather than
// a single "id" string, since BulkAdd creates new rows and has no
// pre-existing id to key on (contrast with bulkFailure in
// task_bulk_tools.go's PRIM-003 pattern, which bulk_update/delete/tag key
// by an existing ids[] entry).
type commentBulkAddFailure struct {
	Target commentBulkTarget `json:"target"`
	Error  *ErrorInfo        `json:"error"`
}

func (a *Adapter) handleCommentBulkAdd(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw := reqStr(req, "targets")
	if raw == "" {
		return errResult(ErrCodeArgInvalid, "targets is required", "targets")
	}
	var targets []commentBulkTarget
	if err := json.Unmarshal([]byte(raw), &targets); err != nil {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid targets JSON: %v", err), "targets")
	}
	if len(targets) == 0 {
		return errResult(ErrCodeArgInvalid, "targets must be a non-empty JSON array", "targets")
	}
	content := reqStr(req, "content")
	if res := commentContentRequired(content); res != nil {
		return res, nil
	}
	author := reqStr(req, "author")

	svcTargets := make([]service.CommentTarget, 0, len(targets))
	byKey := make(map[string]commentBulkTarget, len(targets))
	for i, t := range targets {
		if t.EntityType == "" || t.EntityID == "" {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("targets[%d] must have non-empty entity_type and entity_id", i), "targets")
		}
		svcTargets = append(svcTargets, service.CommentTarget{EntityType: t.EntityType, EntityID: t.EntityID})
		byKey[fmt.Sprintf("%s:%s", t.EntityType, t.EntityID)] = t
	}

	created, _, failed := a.svc.Comment.BulkAdd(svcTargets, author, content)
	if created == nil {
		created = []*sqlstore.CommentRecord{}
	}
	out := make([]commentBulkAddFailure, 0, len(failed))
	for _, f := range failed {
		code, msg, field := mapServiceError(f.Err)
		out = append(out, commentBulkAddFailure{
			Target: byKey[f.ID],
			Error:  &ErrorInfo{Code: code, Message: msg, Field: field},
		})
	}
	return okResult(map[string]any{
		"succeeded": created,
		"failed":    out,
	})
}
