package mcpadapter

import (
	"context"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerIssueTools() {
	a.addTool(mcp.NewTool("torque_issue_create",
		mcp.WithDescription(`Create a project-scoped issue as a kind=issue backlog task. Requires title, body/context/issue/details, and project_id.
Use for low-friction issue capture that should appear in task views but never auto-dispatch. Response shape: data = {<TaskRecord fields>, Body, Tags[]}.
Validation: project_id is required and must reference an enabled project.
Example: {"title":"Login error","body":"Users see 500 on callback","project_id":"PRJ-..."}`),
		mcp.WithString("title", mcp.Required(), mcp.Description("Issue title")),
		mcp.WithString("body", mcp.Description("Issue body/details/context")),
		mcp.WithString("context", mcp.Description("Alias for body")),
		mcp.WithString("issue", mcp.Description("Alias for body")),
		mcp.WithString("details", mcp.Description("Alias for body")),
		mcp.WithString("project_id", mcp.Required(), mcp.Description("Project ID (requires features.projects)")),
	), a.handleIssueCreate)

	a.addTool(mcp.NewTool("torque_issue_get",
		mcp.WithDescription(`Fetch one issue by ID. Rejects non-issue task IDs.
Response shape: data = {<TaskRecord fields>, Body, Tags[]}.
Use torque_task_get for generic task IDs that may not be issues.
Example: {"id":"CW-20260514-0001"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Issue task ID")),
	), a.handleIssueGet)

	a.addTool(mcp.NewTool("torque_issue_delete",
		mcp.WithDescription(`Hard-delete an issue row and its linkage (runs, artifacts, comments cascade). Rejects non-issue task IDs.
Use sparingly — prefer torque_issue_update to close out via status where possible. Supersedes the torque_task_delete workaround previously needed here (that tool has no kind guard, so it worked but skipped the is-this-actually-an-issue check).
Response shape: data = {id, deleted: true}.
Example: {"id":"CW-20260514-0001"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Issue task ID")),
	), a.handleIssueDelete)

	a.addTool(mcp.NewTool("torque_issue_list",
		mcp.WithDescription(`List issues (hard-scoped to kind=issue), optionally narrowed by project_id/status and a free-text query over ID/title/body. Ordered priority ASC (tiebreak id ASC) by default. Pass sort_by (priority|status|updated_at|created_at) and sort_dir (asc|desc) to change order; an unrecognized value returns error.code=arg_invalid.
Merges the former torque_issue_search into this one tool (ADR-0004 §3) — pass "query" for the old search behavior; omit it for a pure filtered list. Limit is now always pushed to the DB layer (no more full-fetch-then-truncate).
Cursor pagination: pass the previous call's meta.next_cursor back as cursor to fetch the next page; meta.next_cursor is null once exhausted. A cursor is only valid for the exact sort_by/sort_dir it was issued under — pass a different sort_by/sort_dir without dropping cursor and you get error.code=arg_invalid.
Response shape: data = {items: [<briefTask or TaskRecord>...], meta: {truncated, returned, limit, has_more, next_cursor}}.
Example: {"project_id":"PRJ-...","status":"backlog","query":"login","limit":"50","sort_by":"updated_at","sort_dir":"desc"}`),
		mcp.WithString("project_id", mcp.Description("Optional project ID filter")),
		mcp.WithString("status", mcp.Description("Optional status filter (e.g. backlog, todo, doing, done)")),
		mcp.WithString("query", mcp.Description("Optional free-text search over ID, title, and body/description")),
		mcp.WithString("limit", mcp.Description("Max results (integer, default 50, max 200)")),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
		mcp.WithString("sort_by", mcp.Description("Sort field: priority|status|updated_at|created_at (default priority)")),
		mcp.WithString("sort_dir", mcp.Description("Sort direction: asc|desc (default asc)")),
		mcp.WithString("cursor", mcp.Description("Opaque pagination cursor from a previous call's meta.next_cursor; omit for the first page. Must match this call's sort_by/sort_dir.")),
	), a.handleIssueList)

	a.addTool(mcp.NewTool("torque_issue_bulk_update",
		mcp.WithDescription(`Apply the same partial update to many issues in one call; per-issue failures are collected, not fatal. Same field set and presence-in-payload semantics as torque_issue_update — only keys you actually pass change; omit a key to leave that field untouched on every issue. Each id must already be a kind=issue task row; a non-issue id fails that item with error.code=arg_invalid, field=kind.
Use for batch issue edits (e.g. re-home a cohort to a new project); prefer torque_issue_update for a single issue and torque_issue_bulk_transition for status-only batch moves.
Response shape: data = {succeeded: [id...], failed: [{id, error: {code, message, field}}...]} — partial success is not an error; ok=true even when some ids fail.
Example: {"ids":"[\"CW-1\",\"CW-2\"]","project_id":"PRJ-..."}`),
		mcp.WithString("ids", mcp.Required(), mcp.Description("JSON array of issue task IDs")),
		mcp.WithString("title", mcp.Description("New issue title")),
		mcp.WithString("body", mcp.Description("New issue body")),
		mcp.WithString("context", mcp.Description("Alias for body")),
		mcp.WithString("issue", mcp.Description("Alias for body")),
		mcp.WithString("details", mcp.Description("Alias for body")),
		mcp.WithString("project_id", mcp.Description("New project ID (requires features.projects)")),
	), a.handleIssueBulkUpdate)

	a.addTool(mcp.NewTool("torque_issue_bulk_transition",
		mcp.WithDescription(`Transition many issues to the same status in one call; per-issue validation errors are collected, not fatal. Issues share Task's lifecycle FSM (todo -> doing -> review -> done, or -> blocked/abandoned) via TaskService.BulkTransition — reused as-is, not reimplemented here.
Use for batch issue status changes; torque_issue_update for field edits. Note: a freshly created issue starts at status=backlog, which is outside Task's FSM (no transitions are defined from it) — move it to todo first via torque_task_update or torque_task_transition with force=true before it can traverse the shared FSM.
Response shape: data = {succeeded: [id...], failed: [{id, error: {code, message, field}}...]} — partial success is not an error; ok=true even when some ids fail.
Example: {"ids":"[\"CW-1\",\"CW-2\"]","status":"done"}`),
		mcp.WithString("ids", mcp.Required(), mcp.Description("JSON array of issue task IDs")),
		mcp.WithString("status", mcp.Required(), mcp.Description("Target status applied to every id")),
	), a.handleIssueBulkTransition)

	a.addTool(mcp.NewTool("torque_issue_update",
		mcp.WithDescription(`Update the minimal issue fields. Only supplied keys change; body/context/issue/details are aliases.
Response shape: data = {<TaskRecord fields>, Body, Tags[]}.
This refuses non-issue IDs so generic task rows cannot be edited through the issue surface.
Example: {"id":"CW-...","details":"New reproduction steps"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Issue task ID")),
		mcp.WithString("title", mcp.Description("New issue title")),
		mcp.WithString("body", mcp.Description("New issue body")),
		mcp.WithString("context", mcp.Description("Alias for body")),
		mcp.WithString("issue", mcp.Description("Alias for body")),
		mcp.WithString("details", mcp.Description("Alias for body")),
		mcp.WithString("project_id", mcp.Description("New project ID (requires features.projects)")),
	), a.handleIssueUpdate)
}

type issueWithTags struct {
	*sqlstore.TaskRecord
	Body string               `json:"Body"`
	Tags []sqlstore.TagRecord `json:"Tags"`
}

func (a *Adapter) issueResult(task *sqlstore.TaskRecord) (*mcp.CallToolResult, error) {
	tags, err := a.svc.Task.ListTags(task.ID)
	if err != nil {
		return errFromService(err)
	}
	if tags == nil {
		tags = []sqlstore.TagRecord{}
	}
	return okResult(issueWithTags{TaskRecord: task, Body: task.Description, Tags: tags})
}

func reqIssueBody(req mcp.CallToolRequest) string {
	for _, key := range []string{"body", "context", "issue", "details"} {
		if v := reqStr(req, key); v != "" {
			return v
		}
	}
	return ""
}

func reqIssueBodyUpdate(req mcp.CallToolRequest) *string {
	for _, key := range []string{"body", "context", "issue", "details"} {
		if reqHasArg(req, key) {
			v := reqStr(req, key)
			return &v
		}
	}
	return nil
}

func (a *Adapter) handleIssueCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	issue, err := a.svc.Issue.Create(service.IssueCreateInput{
		Title:     reqStr(req, "title"),
		Body:      reqIssueBody(req),
		ProjectID: reqStr(req, "project_id"),
	})
	if err != nil {
		return errFromService(err)
	}
	return a.issueResult(issue)
}

func (a *Adapter) handleIssueGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	issue, err := a.svc.Issue.Get(reqStr(req, "id"))
	if err != nil {
		return errFromService(err)
	}
	return a.issueResult(issue)
}

func (a *Adapter) handleIssueDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")
	if err := a.svc.Issue.Delete(id); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{"id": id, "deleted": true})
}

// handleIssueList is the merged list/search handler (ADR-0004 §3): the
// former torque_issue_list and torque_issue_search collapsed into one tool.
// It follows torque_task_list's PRIM-001/PRIM-002 reference pattern
// (internal/mcpadapter/task_tools.go's handleTaskList) — issues are Task
// rows sharing the same sort columns/allow-list, so this reuses
// taskSortAllowList/taskSortDefaultBy/taskSortDefaultDir/taskSortValue
// directly rather than duplicating them.
func (a *Adapter) handleIssueList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := clampLimit(reqInt(req, "limit"), 50, maxTaskListLimit)
	verbose := reqStrBool(req, "verbose")

	sortBy, sortDir, afterSortValue, afterID, errRes := resolveSortAndCursor(req, taskSortDefaultBy, taskSortDefaultDir, taskSortAllowList...)
	if errRes != nil {
		return errRes, nil
	}

	issues, err := a.svc.Issue.List(service.IssueListInput{
		ProjectID: reqStr(req, "project_id"),
		Status:    reqStr(req, "status"),
		Query:     reqStr(req, "query"),
		// Fetch one extra row beyond limit so has_more can be determined
		// without a separate COUNT(*) query, matching handleTaskList.
		Limit:          limit + 1,
		SortBy:         sortBy,
		SortDir:        sortDir,
		AfterSortValue: afterSortValue,
		AfterID:        afterID,
	})
	if err != nil {
		return errFromService(err)
	}

	hasMoreFromQuery := len(issues) > limit
	if hasMoreFromQuery {
		issues = issues[:limit]
	}
	return a.issueListCursorEnvelope(issues, limit, verbose, sortBy, sortDir, hasMoreFromQuery)
}

// issueListCursorEnvelope is torque_issue_list's {items, meta} cursor-
// pagination response, mirroring taskListCursorEnvelope. issues must
// already be trimmed to at most `limit` records. Verbose items use
// issueWithTags (Body+Tags, no DependsOn — matching issueResult's singleton
// shape); brief items reuse toBriefTask since issue rows are TaskRecord.
func (a *Adapter) issueListCursorEnvelope(issues []sqlstore.TaskRecord, limit int, verbose bool, sortBy, sortDir string, hasMoreFromQuery bool) (*mcp.CallToolResult, error) {
	items := make([]any, 0, len(issues))
	for _, t := range issues {
		if verbose {
			tags, err := a.svc.Task.ListTags(t.ID)
			if err != nil {
				return errFromService(err)
			}
			if tags == nil {
				tags = []sqlstore.TagRecord{}
			}
			rec := t
			items = append(items, issueWithTags{TaskRecord: &rec, Body: rec.Description, Tags: tags})
		} else {
			items = append(items, toBriefTask(t, briefTagSlugs(a.svc, t.ID)))
		}
	}

	cursorAt := func(i int) (sortValue, id string) {
		return taskSortValue(issues[i], sortBy), issues[i].ID
	}
	return cappedCursorJSONResult(items, limit, sortBy, sortDir, hasMoreFromQuery, cursorAt)
}

// buildIssueUpdateInput turns a torque_issue_update/torque_issue_bulk_update
// request's arguments into a service.IssueUpdateInput using the same
// presence-in-payload semantics as buildTaskUpdateInput: a key only changes
// its field when the caller actually sent it. Shared by both handlers so
// single- and bulk-update can never drift apart on semantics (PRIM-003).
func buildIssueUpdateInput(req mcp.CallToolRequest) service.IssueUpdateInput {
	input := service.IssueUpdateInput{Body: reqIssueBodyUpdate(req)}
	if reqHasArg(req, "title") {
		v := reqStr(req, "title")
		input.Title = &v
	}
	if reqHasArg(req, "project_id") {
		v := reqStr(req, "project_id")
		input.ProjectID = &v
	}
	return input
}

func (a *Adapter) handleIssueUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")
	if err := a.svc.Issue.Update(id, buildIssueUpdateInput(req)); err != nil {
		return errFromService(err)
	}
	issue, err := a.svc.Issue.Get(id)
	if err != nil {
		return errFromService(err)
	}
	return a.issueResult(issue)
}

func (a *Adapter) handleIssueBulkUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ids, errRes := reqIDs(req)
	if errRes != nil {
		return errRes, nil
	}
	succeeded, failed := a.svc.Issue.BulkUpdate(ids, buildIssueUpdateInput(req))
	return bulkResult(succeeded, failed)
}

// handleIssueBulkTransition reuses TaskService.BulkTransition directly
// (ADR-0004 §3: bulk_transition is kept as its own verb, not folded into
// bulk_update, "only where a real FSM exists to protect — Task, and Issue
// since it shares Task's FSM"). This mirrors handleTaskBulkTransition's
// shape exactly, including ENT-TASK's fix routing BulkTransition through
// the shared bulkResult envelope (see task_tools.go) instead of the old
// bespoke {success, failed, errors} shape. TaskService.BulkTransition
// itself is reused as-is, not reimplemented (out of scope per ENT-ISSUE).
func (a *Adapter) handleIssueBulkTransition(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ids, errRes := reqIDs(req)
	if errRes != nil {
		return errRes, nil
	}
	status := reqStr(req, "status")
	succeeded, failed := a.svc.Task.BulkTransition(ctx, ids, status)
	return bulkResult(succeeded, failed)
}
