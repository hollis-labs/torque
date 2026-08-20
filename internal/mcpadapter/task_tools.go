package mcpadapter

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/service/pagination"
	"github.com/mark3labs/mcp-go/mcp"
)

// taskSortAllowList is torque_task_list's sort_by allow-list (PRIM-002).
// Order here also drives the arg_invalid error message via
// pagination.ValidateSortBy.
var taskSortAllowList = []string{"priority", "status", "updated_at", "created_at"}

// taskSortDefaultBy/taskSortDefaultDir are torque_task_list's default
// sort_by/sort_dir when the caller omits both — chosen to preserve
// FIX-004's locked default order ("ordered priority ASC, created_at ASC")
// as closely as the cursor mechanism allows.
//
// DEC-001's cursor design requires a single sort column tiebroken on `id`
// (not FIX-004's original two-column priority+created_at order). CW task
// IDs are date-prefixed and assigned monotonically per day (see
// NextTaskID), so `id ASC` and `created_at ASC` produce the same practical
// ordering for same-priority ties — switching the tiebreak from created_at
// to id doesn't change the observable default view an agent sees, it only
// changes which column backs the tiebreak internally.
const (
	taskSortDefaultBy  = "priority"
	taskSortDefaultDir = "asc"
)

// parseManualFilter translates the `manual` MCP string param to a *bool for
// TaskFilter.Manual. Input is lowercased before matching so callers sending
// "Manual", "TRUE", etc. behave identically to their lowercase equivalents.
// Accepted vocabularies:
//   - UI:   "both" or "" or absent → nil (no filter)
//   - UI:   "manual"              → true
//   - UI:   "auto"               → false
//   - HTTP: "true" / "1"         → true
//   - HTTP: "false" / "0"        → false
//
// Unknown values return nil (no filter), matching HTTP handler behavior.
func parseManualFilter(v string) *bool {
	t := true
	f := false
	switch strings.ToLower(v) {
	case "manual", "true", "1":
		return &t
	case "auto", "false", "0":
		return &f
	default:
		return nil // "both", "", or unknown → no filter
	}
}

// parseTaskDateFilter parses an RFC3339 timestamp (torque_task_list's
// created_after/created_before/updated_after/updated_before args) into the
// SQLiteDatetimeLayout-formatted UTC text sqlstore.TaskFilter's range
// filters compare against — the same shape every created_at/updated_at
// write already uses (see sqlstore.updatedAtNow's doc comment) and the same
// shape taskSortValue/taskCursorArg use for PRIM-001 cursors on these same
// two columns. Empty input returns ("", nil), which the caller treats as
// "no bound set".
func parseTaskDateFilter(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return "", err
	}
	return t.UTC().Format(sqlstore.SQLiteDatetimeLayout), nil
}

func (a *Adapter) registerTaskTools() {
	a.addTool(mcp.NewTool("torque_task_create",
		mcp.WithDescription(`Create a new task in Torque; returns the full TaskRecord with its assigned ID.
Use for ad-hoc work items — prefer torque_task_create_from_template when a matching template exists, and torque_plan_create for multi-phase work. Safety override forces manual=true on every create (CW-20260417-0133), even when callers pass manual=false or omit the field: the task will NOT dispatch until you promote it with torque_task_update {"id":...,"manual":false}.
Response shape: data = {<TaskRecord fields>, Tags[], dispatch_notice} — singleton, PascalCase keys. dispatch_notice spells out the manual state and the exact promotion call.
Example: {"title":"Fix auth bug","description":"Login returns 500","priority":"2","tags":"[\"backend\"]"}`),
		mcp.WithString("title", mcp.Required(), mcp.Description("Task title")),
		mcp.WithString("description", mcp.Description("Task description")),
		mcp.WithString("priority", mcp.Description("Priority 1-5 (integer, default 2)")),
		mcp.WithString("tags", mcp.Description("JSON array of tag strings")),
		mcp.WithString("executor", mcp.Description("Executor type (default cli)")),
		mcp.WithString("launch_profile", mcp.Description("Torque launch_profile id (preferred). Drives the stable launch family at dispatch.")),
		mcp.WithString("agent_profile", mcp.Description("Legacy agent_profile name. Honored when launch_profile is empty.")),
		mcp.WithString("working_dir", mcp.Description("Working directory. Auto-inherits from parent_id when omitted (CW-20260508-0004); pass explicitly only to override the parent's value.")),
		mcp.WithString("system_prompt", mcp.Description("System prompt override")),
		mcp.WithString("agent_file", mcp.Description("Absolute or working_dir-relative path to a YAML agent spec; loaded at dispatch")),
		mcp.WithString("on_done", mcp.Description("Hook on done")),
		mcp.WithString("on_fail", mcp.Description("Hook on fail")),
		mcp.WithString("on_done_merge", mcp.Description("Merge hook on done")),
		mcp.WithString("depends_on", mcp.Description("JSON array of dependency task IDs")),
		mcp.WithBoolean("manual", mcp.Description("Requested manual flag on create. Tool-surface safety override currently coerces every create to manual=true (CW-20260417-0133); use torque_task_update manual=false later to queue reviewed work.")),
		mcp.WithString("sprint_id", mcp.Description("Sprint ID to associate this task with (requires features.sprints)")),
		mcp.WithString("project_id", mcp.Description("Project ID to associate this task with (requires features.projects)")),
		mcp.WithString("epic_id", mcp.Description("Epic ID to associate this task with (requires features.epics)")),
		mcp.WithString("kind", mcp.Description("agent|external|wait|decision|parent|plan|internal|issue (default agent)")),
		mcp.WithString("source_type", mcp.Description("agent|user|api|system|webhook|import (default user)")),
		mcp.WithString("source_ref", mcp.Description("Originating slug/id (free-form)")),
		mcp.WithString("trust", mcp.Description("trusted|normal|untrusted (defaulted by source_type)")),
		mcp.WithString("checkpoint_mode", mcp.Description("none|blocking|non_blocking (default none)")),
		mcp.WithString("on_checkpoint_response", mcp.Description("resume|review|custom (default resume)")),
		mcp.WithString("metadata", mcp.Description("JSON object: freeform metadata, including metadata.wait for kind=wait")),
		mcp.WithString("parent_id", mcp.Description("Optional parent task ID (migration 013). Empty = top of lineage.")),
		mcp.WithString("on_review", mcp.Description("Hook on review (pause|notify|auto-approve; default pause)")),
		mcp.WithString("blocked_reason", mcp.Description("Blocked reason (also useful as pure tracking metadata, e.g. \"waiting on legal\")")),
		mcp.WithString("tools", mcp.Description("JSON array of tool names")),
		mcp.WithString("files", mcp.Description("JSON array of file paths")),
		mcp.WithString("permissions", mcp.Description("JSON object of permissions")),
		mcp.WithString("environment", mcp.Description("JSON object of env vars")),
		mcp.WithString("cost_budget", mcp.Description("Cost budget (numeric; -1 unlimited, 0 none, or positive)")),
		mcp.WithString("max_retries", mcp.Description("Max retries (non-negative integer, default 3)")),
		mcp.WithString("max_duration_ms", mcp.Description("Max duration in ms (integer; -1 unlimited or positive)")),
		mcp.WithString("token_budget", mcp.Description("Token budget (integer; -1 unlimited or positive)")),
		mcp.WithString("escalation_chain", mcp.Description("JSON array of escalation-target names")),
		mcp.WithString("quality_gates", mcp.Description("JSON array of gate names")),
		mcp.WithString("deliverables", mcp.Description("JSON array of Deliverable objects: {type, required, description?}")),
		mcp.WithString("subtodos", mcp.Description(`JSON array of initial checklist items to seed atomically with the create — {id?, text, required?} per item; id auto-generates when omitted (same as torque_task_subtodo_add). Providing this (including "[]") disables description auto-extraction.`)),
	), a.handleTaskCreate)

	a.addTool(mcp.NewTool("torque_task_get",
		mcp.WithDescription(`Fetch the full TaskRecord for one task ID, including all facet/budget/hook columns and linked tags.
Use when you already have the ID; prefer torque_task_list when filtering a cohort, and torque_task_subtodo_list for checklist-only views.
Response shape: data = {<TaskRecord fields>, Tags[]} — singleton, PascalCase keys.
Example: {"id":"T-123"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Task ID")),
	), a.handleTaskGet)

	a.addTool(mcp.NewTool("torque_task_list",
		mcp.WithDescription(`List tasks with optional status/priority/facet filters; ordered priority ASC (tiebreak id ASC) by default. Pass sort_by (priority|status|updated_at|created_at) and sort_dir (asc|desc) to change order; an unrecognized value returns error.code=arg_invalid.
Use for browsing or filtered cohorts; pass search directly for free-text queries (no separate search tool — matches Issue's already-merged shape) and torque_task_get when you already know the ID. Default returns ~150B briefTask records (lowercase JSON) so large fan-outs fit under the 100KB cap; pass verbose="true" for full TaskRecord columns.
Cursor pagination: pass the previous call's meta.next_cursor back as cursor to fetch the next page; meta.next_cursor is null once exhausted. A cursor is only valid for the exact sort_by/sort_dir it was issued under — pass a different sort_by/sort_dir without dropping cursor and you get error.code=arg_invalid.
statuses[] OR-matches against status (takes precedence over status when both are set). created_after/created_before/updated_after/updated_before are RFC3339 timestamps, inclusive bounds. *_gte/*_lte budget/duration filters compare directly against the stored column — a task that never set that budget (NULL) never matches either bound, so unset-budget tasks are naturally excluded rather than needing a separate "has budget" filter.
Response shape: data = {items: [<briefTask or TaskRecord>...], meta: {truncated, returned, limit, has_more, next_cursor}}.
Example: {"status":"doing","limit":"50","sort_by":"updated_at","sort_dir":"desc"}
Example, over-budget cohort: {"cost_budget_gte":"50","updated_after":"2026-08-01T00:00:00Z"}`),
		mcp.WithString("status", mcp.Description("Filter by status")),
		mcp.WithString("statuses", mcp.Description("JSON array of statuses — OR-match; takes precedence over status when both are set")),
		mcp.WithString("priority", mcp.Description("Filter by priority (integer 1-5)")),
		mcp.WithString("executor", mcp.Description("Filter by executor")),
		mcp.WithString("kind", mcp.Description("Filter by kind (agent|external|wait|decision|parent|plan|internal|issue)")),
		mcp.WithString("source_type", mcp.Description("Filter by source_type")),
		mcp.WithString("source_ref", mcp.Description("Filter by source_ref")),
		mcp.WithString("trust", mcp.Description("Filter by trust (trusted|normal|untrusted)")),
		mcp.WithString("checkpoint_mode", mcp.Description("Filter by checkpoint_mode")),
		mcp.WithString("parent_id", mcp.Description("Filter by parent_id; pass 'null' to return root tasks")),
		mcp.WithString("project_id", mcp.Description("Filter by project ID (requires features.projects)")),
		mcp.WithString("sprint_id", mcp.Description("Filter by sprint ID (requires features.sprints)")),
		mcp.WithString("epic_id", mcp.Description("Filter by epic ID (requires features.epics)")),
		mcp.WithString("tags", mcp.Description("JSON array of tag slugs — AND-match; task must have all listed tags")),
		mcp.WithString("manual", mcp.Description("Filter by manual flag: 'manual'/'true'/'1' → manual only; 'auto'/'false'/'0' → scheduled only; 'both' or omit → no filter")),
		mcp.WithString("include_internal", mcp.Description("Include kind=internal automation tasks (Reviewer end-agents etc.). Default false: internal rows are suppressed unless kind='internal' is requested explicitly. Accepts 'true'/'1'/'yes'.")),
		mcp.WithString("search", mcp.Description("Substring match on title + description (case-insensitive)")),
		mcp.WithString("agent_profile", mcp.Description("Filter by agent_profile (exact match)")),
		mcp.WithString("launch_profile", mcp.Description("Filter by launch_profile (exact match)")),
		mcp.WithString("created_after", mcp.Description("Filter: created_at >= this RFC3339 timestamp")),
		mcp.WithString("created_before", mcp.Description("Filter: created_at <= this RFC3339 timestamp")),
		mcp.WithString("updated_after", mcp.Description("Filter: updated_at >= this RFC3339 timestamp")),
		mcp.WithString("updated_before", mcp.Description("Filter: updated_at <= this RFC3339 timestamp")),
		mcp.WithString("cost_budget_gte", mcp.Description("Filter: cost_budget >= this value (numeric; NULL cost_budget never matches)")),
		mcp.WithString("cost_budget_lte", mcp.Description("Filter: cost_budget <= this value (numeric; NULL cost_budget never matches)")),
		mcp.WithString("token_budget_gte", mcp.Description("Filter: token_budget >= this value (integer; NULL token_budget never matches)")),
		mcp.WithString("token_budget_lte", mcp.Description("Filter: token_budget <= this value (integer; NULL token_budget never matches)")),
		mcp.WithString("max_duration_ms_gte", mcp.Description("Filter: max_duration_ms >= this value (integer; NULL max_duration_ms never matches)")),
		mcp.WithString("max_duration_ms_lte", mcp.Description("Filter: max_duration_ms <= this value (integer; NULL max_duration_ms never matches)")),
		mcp.WithString("max_retries_gte", mcp.Description("Filter: max_retries >= this value (integer)")),
		mcp.WithString("max_retries_lte", mcp.Description("Filter: max_retries <= this value (integer)")),
		mcp.WithString("limit", mcp.Description("Max results (integer, default 50, max 200)")),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
		mcp.WithString("sort_by", mcp.Description("Sort field: priority|status|updated_at|created_at (default priority)")),
		mcp.WithString("sort_dir", mcp.Description("Sort direction: asc|desc (default asc)")),
		mcp.WithString("cursor", mcp.Description("Opaque pagination cursor from a previous call's meta.next_cursor; omit for the first page. Must match this call's sort_by/sort_dir.")),
	), a.handleTaskList)

	a.addTool(mcp.NewTool("torque_task_update",
		mcp.WithDescription(`Partial update of a task's fields; only provided keys change (empty string clears most nullable scalars). Returns the updated TaskRecord.
Use for field edits; prefer torque_task_transition for lifecycle moves and torque_task_bulk_transition for multi-id status changes. Numeric sentinel "-1" = unlimited for budget fields.
Response shape: data = {<TaskRecord fields>, Tags[]} — singleton, PascalCase keys.
Example: {"id":"T-123","priority":"1","tags":"[\"p0\",\"backend\"]"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Task ID")),
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
		mcp.WithString("tags", mcp.Description("JSON array of tag names/slugs — replaces the full linked tag set")),
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
	), a.handleTaskUpdate)

	a.addTool(mcp.NewTool("torque_task_delete",
		mcp.WithDescription(`Hard-delete a task row and its linkage (runs, artifacts, comments cascade).
Use sparingly — prefer torque_task_transition to "abandoned" for audit-preserving closure. For epics/sprints/projects use their respective *_delete tools.
Response shape: data = {id, deleted: true}.
Example: {"id":"T-123"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Task ID")),
	), a.handleTaskDelete)

	a.addTool(mcp.NewTool("torque_task_transition",
		mcp.WithDescription(`Move a task through the lifecycle FSM (todo -> doing -> review -> done, or -> blocked/abandoned). Returns the updated TaskRecord.
Use for single-task status changes; torque_task_bulk_transition for batches; torque_sprint_approve for sprint-scoped approvals. Invalid transitions return error.code=conflict — set force=true to bypass the FSM (user-initiated cleanup only; agents should respect the FSM).
Pass comment to post a comment atomically with the status change (one transaction, not a transition call followed by a separate torque_comment_add) — e.g. explaining why a task moved to blocked. comment_author is optional (empty = unattributed, same as torque_comment_add).
Response shape: data = {<TaskRecord fields>, Tags[]} — singleton.
Example: {"id":"T-123","status":"doing"}
Example with comment: {"id":"T-123","status":"blocked","comment":"waiting on upstream API key","comment_author":"reviewer"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("status", mcp.Required(), mcp.Description("Target status (todo|doing|review|done|blocked|abandoned)")),
		mcp.WithBoolean("force", mcp.Description("Bypass FSM rules; for user-initiated dispositioning only (default false)")),
		mcp.WithString("comment", mcp.Description("Optional comment body to post atomically with the status change (same transaction)")),
		mcp.WithString("comment_author", mcp.Description("Author slug/id for the comment (optional; only used when comment is set)")),
	), a.handleTaskTransition)

	a.addTool(mcp.NewTool("torque_task_bulk_transition",
		mcp.WithDescription(`Transition many tasks to the same status in one call; per-task validation errors are collected, not fatal.
Use for batch approvals or closures; prefer torque_sprint_approve for sprint-scoped approve-all. torque_task_transition for single-task moves.
Response shape: data = {succeeded: [id...], failed: [{id, error: {code, message, field}}...]} — same PRIM-003 bulk envelope as torque_task_bulk_update/_delete/_tag; partial success is not an error, ok=true even when some ids fail.
Example: {"ids":"[\"T-1\",\"T-2\",\"T-3\"]","status":"done"}`),
		mcp.WithString("ids", mcp.Required(), mcp.Description("JSON array of task IDs")),
		mcp.WithString("status", mcp.Required(), mcp.Description("Target status applied to every id")),
	), a.handleTaskBulkTransition)
}

// taskWithTags is an MCP result shape that flattens a TaskRecord's fields
// and appends Tags/DependsOn keys alongside them (via Go's embedded-struct
// JSON marshaling). The MCP adapter marshals TaskRecord with Go's default
// capitalization (no json tags on TaskRecord), so this struct keeps Tags and
// DependsOn capitalized to stay consistent with the surrounding fields —
// this is intentionally different from the HTTP API, which lowercases
// everything via an explicit taskJSON builder.
//
// DependsOn is populated from the task_dependencies join table (migration
// 027 / FK-003), not a TaskRecord column — same shape as Tags. It renders as
// a clean JSON array of task IDs here, an improvement over the pre-FK-003
// shape (a sql.NullString column marshaled as its raw {String,Valid}
// struct).
type taskWithTags struct {
	*sqlstore.TaskRecord
	Tags      []sqlstore.TagRecord `json:"Tags"`
	DependsOn []string             `json:"DependsOn"`
}

// taskResult loads the linked tags and dependency IDs for a task and
// returns a combined MCP result.
func (a *Adapter) taskResult(task *sqlstore.TaskRecord) (*mcp.CallToolResult, error) {
	tags, err := a.svc.Task.ListTags(task.ID)
	if err != nil {
		return errFromService(err)
	}
	if tags == nil {
		tags = []sqlstore.TagRecord{}
	}
	deps, err := a.svc.Task.ListDependencyIDs(task.ID)
	if err != nil {
		return errFromService(err)
	}
	if deps == nil {
		deps = []string{}
	}
	return okResult(taskWithTags{TaskRecord: task, Tags: tags, DependsOn: deps})
}

// createdTaskResult is the torque_task_create response shape. It is the
// normal taskWithTags record plus a dispatch_notice block that makes the
// manual-flag state — and how to clear it — discoverable inline.
//
// CW-20260517-0011 edge 8: torque_task_create force-sets manual=true (the
// CW-20260417-0133 safety override). The papercut is that a freshly created
// task silently never dispatches until someone remembers the separate
// torque_task_update manual=false promotion. Surfacing the state and the
// exact promotion call in the create response removes the "why isn't my
// task running?" dead end.
type createdTaskResult struct {
	*taskWithTags
	DispatchNotice dispatchNotice `json:"dispatch_notice"`
}

// dispatchNotice explains, in the create response, whether the new task is
// dispatch-eligible and — when it is not — exactly how to make it so.
type dispatchNotice struct {
	// Manual is the persisted manual flag of the created task.
	Manual bool `json:"manual"`
	// Dispatchable is true only when the scheduler can pick the task up
	// without further action (manual=false).
	Dispatchable bool `json:"dispatchable"`
	// Message is a human-readable explanation of the current state.
	Message string `json:"message"`
	// PromoteWith, when set, is the literal MCP call that flips the task to
	// dispatch-eligible. Empty when the task is already dispatchable.
	PromoteWith string `json:"promote_with,omitempty"`
}

// createdTaskResultFor builds the createdTaskResult envelope for a freshly
// created task, loading its tags and dependency IDs the same way taskResult
// does.
func (a *Adapter) createdTaskResultFor(task *sqlstore.TaskRecord) (*mcp.CallToolResult, error) {
	tags, err := a.svc.Task.ListTags(task.ID)
	if err != nil {
		return errFromService(err)
	}
	if tags == nil {
		tags = []sqlstore.TagRecord{}
	}
	deps, err := a.svc.Task.ListDependencyIDs(task.ID)
	if err != nil {
		return errFromService(err)
	}
	if deps == nil {
		deps = []string{}
	}
	notice := dispatchNotice{Manual: task.Manual, Dispatchable: !task.Manual}
	if task.Manual {
		notice.Message = "This task was created with manual=true (the CW-20260417-0133 safety default — every torque_task_create starts manual). " +
			"While manual, the scheduler will NEVER dispatch it; it stays in todo until you promote it. " +
			"To make it dispatch-eligible, run the promote_with call below; after that the picker will schedule it once status=todo, dependencies are done, and an agent_profile is set."
		notice.PromoteWith = fmt.Sprintf(`torque_task_update {"id":"%s","manual":false}`, task.ID)
	} else {
		notice.Message = "This task is manual=false and dispatch-eligible: the scheduler will pick it up once status=todo, dependencies are done, and (for agent tasks) an agent_profile is set."
	}
	return okResult(createdTaskResult{
		taskWithTags:   &taskWithTags{TaskRecord: task, Tags: tags, DependsOn: deps},
		DispatchNotice: notice,
	})
}

func (a *Adapter) handleTaskCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Safety override per CW-20260417-0133: force manual=true on every task
	// create until portfolio callers stop shipping manual=false (explicitly or
	// by default). See createTask in internal/httpserver/tasks.go for the
	// parallel HTTP-side override. Pairs with CW-20260417-0134 (removal) and
	// CW-20260417-0413 (permanent create-time validation). Update path is
	// UNCHANGED so reviewed tasks can still be promoted to manual=false.
	manual := reqBool(req, "manual")
	title := reqStr(req, "title")
	if !manual {
		log.Printf("torque_task_create: forcing manual=true on %q (caller passed manual=false) — safety override per CW-20260417-0133", title)
		manual = true
	}

	input := service.TaskCreateInput{
		Title:                title,
		Description:          reqStr(req, "description"),
		Priority:             reqInt(req, "priority"),
		Executor:             reqStr(req, "executor"),
		LaunchProfile:        reqStr(req, "launch_profile"),
		AgentProfile:         reqStr(req, "agent_profile"),
		WorkingDir:           reqStr(req, "working_dir"),
		SystemPrompt:         reqStr(req, "system_prompt"),
		AgentFile:            reqStr(req, "agent_file"),
		OnDone:               reqStr(req, "on_done"),
		OnFail:               reqStr(req, "on_fail"),
		OnDoneMerge:          reqStr(req, "on_done_merge"),
		Manual:               manual,
		SprintID:             reqStr(req, "sprint_id"),
		ProjectID:            reqStr(req, "project_id"),
		EpicID:               reqStr(req, "epic_id"),
		Kind:                 reqStr(req, "kind"),
		SourceType:           reqStr(req, "source_type"),
		SourceRef:            reqStr(req, "source_ref"),
		Trust:                reqStr(req, "trust"),
		CheckpointMode:       reqStr(req, "checkpoint_mode"),
		OnCheckpointResponse: reqStr(req, "on_checkpoint_response"),

		// ENT-TASK create-field expansion: plain-string fields the service
		// layer already accepted on TaskCreateInput but the MCP schema never
		// exposed. OnReview mirrors OnDone/OnFail above; BlockedReason is
		// useful as pure tracking metadata even outside the blocked-status
		// flow (ADR-0004 §4).
		OnReview:      reqStr(req, "on_review"),
		BlockedReason: reqStr(req, "blocked_reason"),

		ParentID: reqStr(req, "parent_id"),
	}

	if tags, err := reqStrSlice(req, "tags"); err != nil {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid tags JSON: %v", err), "tags")
	} else if tags != nil {
		input.Tags = tags
	}
	// depends_on: use reqStrSlice (like tags) rather than a raw json.Unmarshal
	// on reqStr. go-mcp-sanitize's Pattern 4 only special-cases the literal
	// "tags" key today, but reqStrSlice's []any tolerance is still cheap
	// insurance against any MCP client/library that hands the argument over
	// as a native array instead of a JSON-encoded string; a plain malformed
	// string (e.g. "not-json-at-all") still hits reqStrSlice's json.Unmarshal
	// fallback and reports the same arg_invalid/depends_on error as before.
	if deps, err := reqStrSlice(req, "depends_on"); err != nil {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid depends_on JSON: %v", err), "depends_on")
	} else if deps != nil {
		input.DependsOn = deps
	}
	if raw := reqStr(req, "metadata"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &input.Metadata); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid metadata JSON: %v", err), "metadata")
		}
	}

	// ENT-TASK create-field expansion continued: []string fields via
	// reqStrSlice (same tolerant-of-[]any-or-JSON-string handling as
	// tags/depends_on above).
	if tools, err := reqStrSlice(req, "tools"); err != nil {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid tools JSON: %v", err), "tools")
	} else if tools != nil {
		input.Tools = tools
	}
	if files, err := reqStrSlice(req, "files"); err != nil {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid files JSON: %v", err), "files")
	} else if files != nil {
		input.Files = files
	}
	if ec, err := reqStrSlice(req, "escalation_chain"); err != nil {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid escalation_chain JSON: %v", err), "escalation_chain")
	} else if ec != nil {
		input.EscalationChain = ec
	}
	if qg, err := reqStrSlice(req, "quality_gates"); err != nil {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid quality_gates JSON: %v", err), "quality_gates")
	} else if qg != nil {
		input.QualityGates = qg
	}

	// JSON-object/array fields with a non-[]string shape: unmarshal directly
	// into TaskCreateInput's typed field, matching buildTaskUpdateInput's
	// unmarshalBlob convention for the same field names on torque_task_update.
	if raw := reqStr(req, "permissions"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &input.Permissions); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid permissions JSON: %v", err), "permissions")
		}
	}
	if raw := reqStr(req, "environment"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &input.Environment); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid environment JSON: %v", err), "environment")
		}
	}
	if raw := reqStr(req, "deliverables"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &input.Deliverables); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid deliverables JSON: %v", err), "deliverables")
		}
	}

	// Numeric nullables: presence-gated like buildTaskUpdateInput's
	// cost_budget/max_retries/max_duration_ms/token_budget handling, so an
	// omitted key leaves the service-layer default (nil pointer) rather than
	// forcing an explicit 0.
	createArgs := req.GetArguments()
	if _, ok := createArgs["cost_budget"]; ok {
		v := reqFloat(req, "cost_budget")
		input.CostBudget = &v
	}
	if _, ok := createArgs["max_retries"]; ok {
		v := reqInt(req, "max_retries")
		input.MaxRetries = &v
	}
	if _, ok := createArgs["max_duration_ms"]; ok {
		v := int64(reqFloat(req, "max_duration_ms"))
		input.MaxDurationMs = &v
	}
	if _, ok := createArgs["token_budget"]; ok {
		v := int64(reqFloat(req, "token_budget"))
		input.TokenBudget = &v
	}

	// subtodos[] seed list (ENT-TASK): create the task and its initial
	// checklist in one call instead of create + N torque_task_subtodo_add
	// round-trips. A non-nil (including empty "[]") value here disables
	// Create's auto-extract-from-description fallback, matching the
	// existing TaskCreateInput.Subtodos contract (see its doc comment).
	// IDs are optional per item — Create auto-generates any that are
	// omitted and rejects duplicates, mirroring torque_task_subtodo_add's
	// single-item id auto-gen (FIX-002).
	if raw := reqStr(req, "subtodos"); raw != "" {
		var seeds []struct {
			ID       string `json:"id"`
			Text     string `json:"text"`
			Required bool   `json:"required"`
		}
		if err := json.Unmarshal([]byte(raw), &seeds); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid subtodos JSON: %v", err), "subtodos")
		}
		items := make([]sqlstore.Subtodo, 0, len(seeds))
		for i, sd := range seeds {
			if sd.Text == "" {
				return errResult(ErrCodeArgInvalid, fmt.Sprintf("subtodos[%d].text is required", i), "subtodos")
			}
			items = append(items, sqlstore.Subtodo{ID: sd.ID, Text: sd.Text, Required: sd.Required})
		}
		input.Subtodos = items
	}

	task, err := a.svc.Task.Create(input)
	if err != nil {
		return errFromService(err)
	}
	// Edge 8 (CW-20260517-0011): return the create-specific envelope so the
	// manual-flag state and the exact promotion call are discoverable inline.
	return a.createdTaskResultFor(task)
}

func (a *Adapter) handleTaskGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	task, err := a.svc.Task.Get(reqStr(req, "id"))
	if err != nil {
		return errFromService(err)
	}
	return a.taskResult(task)
}

func (a *Adapter) handleTaskList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := clampLimit(reqInt(req, "limit"), 50, maxTaskListLimit)
	verbose := reqStrBool(req, "verbose")

	// PRIM-002: sort_by/sort_dir, allow-list validated. Omitted values fall
	// back to FIX-004's locked default order (see taskSortDefaultBy/Dir's
	// doc comment for why the tiebreak column differs from FIX-004's
	// original wording without changing the observable order).
	sortBy := taskSortDefaultBy
	if raw := reqStr(req, "sort_by"); raw != "" {
		v, err := pagination.ValidateSortBy(raw, taskSortAllowList...)
		if err != nil {
			return errResult(ErrCodeArgInvalid, err.Error(), "sort_by")
		}
		sortBy = v
	}
	sortDir := taskSortDefaultDir
	if raw := reqStr(req, "sort_dir"); raw != "" {
		v, err := pagination.ValidateSortDir(raw)
		if err != nil {
			return errResult(ErrCodeArgInvalid, err.Error(), "sort_dir")
		}
		sortDir = v
	}

	// PRIM-001: decode + validate the incoming cursor, if any, against this
	// request's (now-resolved) sort_by/sort_dir. DEC-001: cursors aren't
	// portable across sort orders.
	var afterSortValue, afterID string
	if raw := reqStr(req, "cursor"); raw != "" {
		c, err := pagination.Decode(raw)
		if err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid cursor: %v", err), "cursor")
		}
		if err := c.Validate(sortBy, sortDir); err != nil {
			return errResult(ErrCodeArgInvalid, err.Error(), "cursor")
		}
		afterSortValue, afterID = c.SortValue, c.ID
	}

	filter := sqlstore.TaskFilter{
		Status:         reqStr(req, "status"),
		Priority:       reqInt(req, "priority"),
		Executor:       reqStr(req, "executor"),
		Kind:           reqStr(req, "kind"),
		SourceType:     reqStr(req, "source_type"),
		SourceRef:      reqStr(req, "source_ref"),
		Trust:          reqStr(req, "trust"),
		CheckpointMode: reqStr(req, "checkpoint_mode"),
		ProjectID:      reqStr(req, "project_id"),
		SprintID:       reqStr(req, "sprint_id"),
		EpicID:         reqStr(req, "epic_id"),
		Search:         reqStr(req, "search"),
		Manual:         parseManualFilter(reqStr(req, "manual")),
		AgentProfile:   reqStr(req, "agent_profile"),
		LaunchProfile:  reqStr(req, "launch_profile"),
		// Fetch one extra row beyond limit so has_more can be determined
		// without a separate COUNT(*) query (DEC-001's cheaper-default
		// choice). handleTaskList trims the extra row before building the
		// response envelope.
		Limit:          limit + 1,
		SortBy:         sortBy,
		SortDir:        sortDir,
		AfterSortValue: afterSortValue,
		AfterID:        afterID,
		// kind=internal default-exclude (CW-20260503-0011): user-facing
		// list calls hide internal automation tasks unless include_internal
		// is truthy. An explicit kind filter takes precedence at the SQL
		// layer, so this flip is safe to set unconditionally.
		ExcludeInternal: !reqStrBool(req, "include_internal"),
	}
	if _, ok := req.GetArguments()["parent_id"]; ok {
		v := reqStr(req, "parent_id")
		if v == "" || v == "null" {
			filter.ParentIDNull = true
		} else {
			filter.ParentID = v
		}
	}
	if tags, err := reqStrSlice(req, "tags"); err != nil {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid tags JSON: %v", err), "tags")
	} else if tags != nil {
		filter.TagSlugs = tags
	}
	// ENT-TASK: statuses[] OR-filter — already supported at the store layer
	// (TaskFilter.Statuses, ListTasks) and on HTTP; this is the MCP wiring.
	// ListTasks prefers Statuses over the single Status field when both are
	// set, so no extra precedence handling is needed here.
	if statuses, err := reqStrSlice(req, "statuses"); err != nil {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid statuses JSON: %v", err), "statuses")
	} else if statuses != nil {
		filter.Statuses = statuses
	}
	// created_at/updated_at range filters (ENT-TASK). Caller-facing input is
	// RFC3339 (matching every other timestamp param on this MCP surface,
	// e.g. checkpoint_tools.go's timeout_at); parseTaskDateFilter reformats
	// to the SQLiteDatetimeLayout text ListTasks compares against.
	if raw := reqStr(req, "created_after"); raw != "" {
		v, err := parseTaskDateFilter(raw)
		if err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid created_after: %v", err), "created_after")
		}
		filter.CreatedAfter = v
	}
	if raw := reqStr(req, "created_before"); raw != "" {
		v, err := parseTaskDateFilter(raw)
		if err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid created_before: %v", err), "created_before")
		}
		filter.CreatedBefore = v
	}
	if raw := reqStr(req, "updated_after"); raw != "" {
		v, err := parseTaskDateFilter(raw)
		if err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid updated_after: %v", err), "updated_after")
		}
		filter.UpdatedAfter = v
	}
	if raw := reqStr(req, "updated_before"); raw != "" {
		v, err := parseTaskDateFilter(raw)
		if err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid updated_before: %v", err), "updated_before")
		}
		filter.UpdatedBefore = v
	}
	// Budget/duration range filters (ENT-TASK) — presence-gated (like
	// buildTaskUpdateInput's numeric-nullable fields) so an omitted key
	// leaves the bound unset rather than defaulting to 0, which would
	// wrongly exclude every task whose budget is above/below zero.
	listArgs := req.GetArguments()
	if _, ok := listArgs["cost_budget_gte"]; ok {
		v := reqFloat(req, "cost_budget_gte")
		filter.CostBudgetGte = &v
	}
	if _, ok := listArgs["cost_budget_lte"]; ok {
		v := reqFloat(req, "cost_budget_lte")
		filter.CostBudgetLte = &v
	}
	if _, ok := listArgs["token_budget_gte"]; ok {
		v := int64(reqFloat(req, "token_budget_gte"))
		filter.TokenBudgetGte = &v
	}
	if _, ok := listArgs["token_budget_lte"]; ok {
		v := int64(reqFloat(req, "token_budget_lte"))
		filter.TokenBudgetLte = &v
	}
	if _, ok := listArgs["max_duration_ms_gte"]; ok {
		v := int64(reqFloat(req, "max_duration_ms_gte"))
		filter.MaxDurationMsGte = &v
	}
	if _, ok := listArgs["max_duration_ms_lte"]; ok {
		v := int64(reqFloat(req, "max_duration_ms_lte"))
		filter.MaxDurationMsLte = &v
	}
	if _, ok := listArgs["max_retries_gte"]; ok {
		v := reqInt(req, "max_retries_gte")
		filter.MaxRetriesGte = &v
	}
	if _, ok := listArgs["max_retries_lte"]; ok {
		v := reqInt(req, "max_retries_lte")
		filter.MaxRetriesLte = &v
	}
	tasks, err := a.svc.Task.List(filter)
	if err != nil {
		return errFromService(err)
	}

	hasMoreFromQuery := len(tasks) > limit
	if hasMoreFromQuery {
		tasks = tasks[:limit]
	}
	return a.taskListCursorEnvelope(tasks, limit, verbose, sortBy, sortDir, hasMoreFromQuery)
}

// taskSortValue formats a TaskRecord's sortBy column into the string
// encoding PRIM-001's cursor uses for meta.next_cursor (DEC-001's `sv`
// field). updated_at/created_at use sqlstore.SQLiteDatetimeLayout — the
// exact text shape SQLite's own CURRENT_TIMESTAMP writes and
// sqlstore.taskCursorArg parses/binds on the decode side — NOT
// time.RFC3339Nano; see sqlstore.updatedAtNow's doc comment for why the two
// diverge (RFC3339 with a 'T'/'Z' vs the actual "YYYY-MM-DD HH:MM:SS" stored
// shape) and why that mismatch would otherwise silently break the
// WHERE-clause comparison.
func taskSortValue(t sqlstore.TaskRecord, sortBy string) string {
	switch sortBy {
	case "priority":
		return strconv.Itoa(t.Priority)
	case "status":
		return t.Status
	case "updated_at":
		return t.UpdatedAt.UTC().Format(sqlstore.SQLiteDatetimeLayout)
	case "created_at":
		return t.CreatedAt.UTC().Format(sqlstore.SQLiteDatetimeLayout)
	default:
		return ""
	}
}

// taskListCursorEnvelope builds torque_task_list's {items, meta} cursor-
// pagination response (PRIM-001/PRIM-002 reference implementation). tasks
// must already be trimmed to at most `limit` records — handleTaskList
// over-fetches limit+1 to compute hasMoreFromQuery, then drops the extra row
// before calling this, so cappedCursorJSONResult's byte-size trim (if it
// triggers) is the only further truncation next_cursor needs to account for.
func (a *Adapter) taskListCursorEnvelope(tasks []sqlstore.TaskRecord, limit int, verbose bool, sortBy, sortDir string, hasMoreFromQuery bool) (*mcp.CallToolResult, error) {
	items := make([]any, 0, len(tasks))
	for _, t := range tasks {
		if verbose {
			tags, err := a.svc.Task.ListTags(t.ID)
			if err != nil {
				return errFromService(err)
			}
			if tags == nil {
				tags = []sqlstore.TagRecord{}
			}
			// Copy struct to take address of a fresh local rather than loop var.
			rec := t
			items = append(items, taskWithTags{TaskRecord: &rec, Tags: tags})
		} else {
			items = append(items, toBriefTask(t, briefTagSlugs(a.svc, t.ID)))
		}
	}

	cursorAt := func(i int) (sortValue, id string) {
		return taskSortValue(tasks[i], sortBy), tasks[i].ID
	}
	return cappedCursorJSONResult(items, limit, sortBy, sortDir, hasMoreFromQuery, cursorAt)
}

// buildTaskUpdateInput turns a torque_task_update-shaped request's
// arguments into a service.TaskUpdateInput using presence-in-payload
// semantics: a key only changes its field when the caller actually sent it
// (checked via args[key], not the decoded zero value) — so an explicit
// empty string clears a nullable scalar while an omitted key leaves the
// field untouched (FIX-001). This is the single source of truth for what
// "presence in payload" means for Task's update fields; handleTaskUpdate
// (single-id, below) and handleTaskBulkUpdate (task_bulk_tools.go,
// PRIM-003) both call it so single- and bulk-update can never drift apart
// on semantics.
//
// On a validation failure (malformed JSON in one of the blob fields), the
// second return value is non-nil and the caller must return it as-is
// without inspecting the (zero-value) TaskUpdateInput.
func buildTaskUpdateInput(req mcp.CallToolRequest) (service.TaskUpdateInput, *mcp.CallToolResult) {
	update := sqlstore.TaskUpdate{}
	args := req.GetArguments()

	// title/description/priority: presence-based detection, matching every
	// other field on this tool (see args["..."] pattern below). Value-based
	// detection (the prior `if v != ""` / `if v != 0` checks) meant an
	// explicit "" could never clear title/description, and an explicit
	// priority=0 was silently ignored as "not set" (FIX-001).
	if _, ok := args["title"]; ok {
		v := reqStr(req, "title")
		update.Title = &v
	}
	if _, ok := args["description"]; ok {
		v := reqStr(req, "description")
		update.Description = &v
	}
	if _, ok := args["priority"]; ok {
		v := reqInt(req, "priority")
		update.Priority = &v
	}

	// Scalar pass-through fields: presence in args is the trigger so callers
	// can also clear a column by providing an empty string. Lifecycle-enum
	// fields with an empty string are rejected downstream by
	// validateLifecycleEnum — intentional parity with the HTTP layer.
	if _, ok := args["manual"]; ok {
		v := reqBool(req, "manual")
		update.Manual = &v
	}
	if _, ok := args["executor"]; ok {
		v := reqStr(req, "executor")
		update.Executor = &v
	}
	if _, ok := args["launch_profile"]; ok {
		v := reqStr(req, "launch_profile")
		update.LaunchProfile = &v
	}
	if _, ok := args["agent_profile"]; ok {
		v := reqStr(req, "agent_profile")
		update.AgentProfile = &v
	}
	if _, ok := args["working_dir"]; ok {
		v := reqStr(req, "working_dir")
		update.WorkingDir = &v
	}
	if _, ok := args["system_prompt"]; ok {
		v := reqStr(req, "system_prompt")
		update.SystemPrompt = &v
	}
	if _, ok := args["agent_file"]; ok {
		v := reqStr(req, "agent_file")
		update.AgentFile = &v
	}
	if _, ok := args["on_done"]; ok {
		v := reqStr(req, "on_done")
		update.OnDone = &v
	}
	if _, ok := args["on_fail"]; ok {
		v := reqStr(req, "on_fail")
		update.OnFail = &v
	}
	if _, ok := args["on_review"]; ok {
		v := reqStr(req, "on_review")
		update.OnReview = &v
	}
	if _, ok := args["on_done_merge"]; ok {
		v := reqStr(req, "on_done_merge")
		update.OnDoneMerge = &v
	}
	if _, ok := args["deliverable_preset"]; ok {
		v := reqStr(req, "deliverable_preset")
		update.DeliverablePreset = &v
	}
	if _, ok := args["blocked_reason"]; ok {
		v := reqStr(req, "blocked_reason")
		update.BlockedReason = &v
	}

	// Numeric nullables wrap as *sql.NullFloat64 / *sql.NullInt64
	if _, ok := args["cost_budget"]; ok {
		v := reqFloat(req, "cost_budget")
		update.CostBudget = &sql.NullFloat64{Float64: v, Valid: true}
	}
	if _, ok := args["max_retries"]; ok {
		v := reqInt(req, "max_retries")
		update.MaxRetries = &v
	}
	if _, ok := args["max_duration_ms"]; ok {
		v := int64(reqFloat(req, "max_duration_ms"))
		update.MaxDurationMs = &sql.NullInt64{Int64: v, Valid: true}
	}
	if _, ok := args["token_budget"]; ok {
		v := int64(reqFloat(req, "token_budget"))
		update.TokenBudget = &sql.NullInt64{Int64: v, Valid: true}
	}

	// JSON-blob fields: callers pass JSON-encoded strings. Skip when empty
	// string to match the template-tool convention; pass "[]" or "{}" to
	// explicitly write an empty collection.
	unmarshalBlob := func(key string, out any) error {
		raw := reqStr(req, key)
		if raw == "" {
			return nil
		}
		if err := json.Unmarshal([]byte(raw), out); err != nil {
			return fmt.Errorf("invalid %s JSON: %v", key, err)
		}
		return nil
	}
	nullFromRaw := func(key string) *sql.NullString {
		raw := reqStr(req, key)
		if raw == "" {
			return nil
		}
		return &sql.NullString{String: raw, Valid: true}
	}
	// Validate shape before persisting (reject malformed JSON early).
	// depends_on is handled separately below via reqStrSlice (like tags),
	// which tolerates both the raw-JSON-string and post-sanitize []any
	// shapes — it is not a JSON-blob column since migration 027 / FK-003, so
	// it doesn't belong in this raw-string-only validation loop.
	var scratchArr []any
	var scratchObj map[string]any
	for _, k := range []string{"tools", "files", "escalation_chain", "quality_gates", "deliverables"} {
		if err := unmarshalBlob(k, &scratchArr); err != nil {
			res, _ := errResult(ErrCodeArgInvalid, err.Error(), k)
			return service.TaskUpdateInput{}, res
		}
	}
	for _, k := range []string{"permissions", "environment", "metadata"} {
		if err := unmarshalBlob(k, &scratchObj); err != nil {
			res, _ := errResult(ErrCodeArgInvalid, err.Error(), k)
			return service.TaskUpdateInput{}, res
		}
	}
	if ns := nullFromRaw("tools"); ns != nil {
		update.Tools = ns
	}
	if ns := nullFromRaw("files"); ns != nil {
		update.Files = ns
	}
	if ns := nullFromRaw("permissions"); ns != nil {
		update.Permissions = ns
	}
	if ns := nullFromRaw("environment"); ns != nil {
		update.Environment = ns
	}
	if ns := nullFromRaw("escalation_chain"); ns != nil {
		update.EscalationChain = ns
	}
	if ns := nullFromRaw("quality_gates"); ns != nil {
		update.QualityGates = ns
	}
	if ns := nullFromRaw("deliverables"); ns != nil {
		update.Deliverables = ns
	}
	if ns := nullFromRaw("metadata"); ns != nil {
		update.Metadata = ns
	}

	// Association fields — allow setting to empty string to unassign
	if _, ok := args["sprint_id"]; ok {
		v := reqStr(req, "sprint_id")
		ns := sql.NullString{String: v, Valid: v != ""}
		update.SprintID = &ns
	}
	if _, ok := args["project_id"]; ok {
		v := reqStr(req, "project_id")
		ns := sql.NullString{String: v, Valid: v != ""}
		update.ProjectID = &ns
	}
	if _, ok := args["epic_id"]; ok {
		v := reqStr(req, "epic_id")
		ns := sql.NullString{String: v, Valid: v != ""}
		update.EpicID = &ns
	}

	// Facet updates — any provided key triggers a change.
	if _, ok := args["kind"]; ok {
		v := reqStr(req, "kind")
		update.Kind = &v
	}
	if _, ok := args["source_type"]; ok {
		v := reqStr(req, "source_type")
		update.SourceType = &v
	}
	if _, ok := args["source_ref"]; ok {
		v := reqStr(req, "source_ref")
		ns := sql.NullString{String: v, Valid: v != ""}
		update.SourceRef = &ns
	}
	if _, ok := args["trust"]; ok {
		v := reqStr(req, "trust")
		update.Trust = &v
	}
	if _, ok := args["checkpoint_mode"]; ok {
		v := reqStr(req, "checkpoint_mode")
		update.CheckpointMode = &v
	}
	if _, ok := args["on_checkpoint_response"]; ok {
		v := reqStr(req, "on_checkpoint_response")
		update.OnCheckpointResponse = &v
	}
	// Parent linkage (migration 013). Empty string clears the parent.
	if _, ok := args["parent_id"]; ok {
		v := reqStr(req, "parent_id")
		ns := sql.NullString{String: v, Valid: v != ""}
		update.ParentID = &ns
	}

	input := service.TaskUpdateInput{TaskUpdate: update}
	// task_update treats tags presence-sensitively: only set Tags when the
	// caller actually supplied a value (post-sanitize: nil from absent or
	// empty-string; non-nil slice from a real value).
	if reqHasArg(req, "tags") {
		slugs, err := reqStrSlice(req, "tags")
		if err != nil {
			res, _ := errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid tags JSON: %v", err), "tags")
			return service.TaskUpdateInput{}, res
		}
		if slugs != nil {
			input.Tags = &slugs
		}
	}
	// depends_on (migration 029 / FK-003): same presence-sensitive shape as
	// tags — nil means "no change" (existing edges left alone), non-nil
	// slice replaces the full dependency set.
	if reqHasArg(req, "depends_on") {
		deps, err := reqStrSlice(req, "depends_on")
		if err != nil {
			res, _ := errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid depends_on JSON: %v", err), "depends_on")
			return service.TaskUpdateInput{}, res
		}
		if deps != nil {
			input.DependsOn = &deps
		}
	}

	return input, nil
}

func (a *Adapter) handleTaskUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")
	input, errRes := buildTaskUpdateInput(req)
	if errRes != nil {
		return errRes, nil
	}

	if err := a.svc.Task.Update(id, input); err != nil {
		return errFromService(err)
	}
	task, err := a.svc.Task.Get(id)
	if err != nil {
		return errFromService(err)
	}
	return a.taskResult(task)
}

func (a *Adapter) handleTaskDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")
	if err := a.svc.Task.Delete(id); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{"id": id, "deleted": true})
}

func (a *Adapter) handleTaskTransition(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")
	status := reqStr(req, "status")
	force := reqBool(req, "force")

	// ENT-TASK: comment posts atomically with the status change (same
	// transaction — TaskService.TransitionWithComment) instead of a
	// transition call followed by a separate torque_comment_add. The
	// no-comment path is untouched: it keeps calling Transition/
	// ForceTransition exactly as before.
	if comment := reqStr(req, "comment"); comment != "" {
		if err := a.svc.Task.TransitionWithComment(ctx, id, status, comment, reqStr(req, "comment_author"), force); err != nil {
			return errFromService(err)
		}
	} else {
		transition := a.svc.Task.Transition
		if force {
			transition = a.svc.Task.ForceTransition
		}
		if err := transition(ctx, id, status); err != nil {
			return errFromService(err)
		}
	}

	task, err := a.svc.Task.Get(id)
	if err != nil {
		return errFromService(err)
	}
	return a.taskResult(task)
}

// tasksToEnvelope converts a TaskRecord slice into the {items, meta} MCP
// response envelope. When verbose is false, each record is the ~150-byte
// briefTask shape with tag slugs only; when true, each record is the full
// taskWithTags structure (matching the taskResult() singleton-get shape).
func (a *Adapter) tasksToEnvelope(tasks []sqlstore.TaskRecord, limit int, verbose bool) (*mcp.CallToolResult, error) {
	items := make([]any, 0, len(tasks))
	for _, t := range tasks {
		if verbose {
			tags, err := a.svc.Task.ListTags(t.ID)
			if err != nil {
				return errFromService(err)
			}
			if tags == nil {
				tags = []sqlstore.TagRecord{}
			}
			deps, err := a.svc.Task.ListDependencyIDs(t.ID)
			if err != nil {
				return errFromService(err)
			}
			if deps == nil {
				deps = []string{}
			}
			// Copy struct to take address of a fresh local rather than loop var.
			rec := t
			items = append(items, taskWithTags{TaskRecord: &rec, Tags: tags, DependsOn: deps})
		} else {
			items = append(items, toBriefTask(t, briefTagSlugs(a.svc, t.ID)))
		}
	}
	return cappedJSONResult(items, limit)
}

func (a *Adapter) handleTaskBulkTransition(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw := reqStr(req, "ids")
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid ids JSON: %v", err), "ids")
	}
	status := reqStr(req, "status")
	// PRIM-003 (ENT-TASK): funnel through the same bulkResult envelope as
	// bulk_update/bulk_delete/bulk_tag — see BulkTransition's doc comment
	// (task.go) for why this used to be a bespoke {success, failed, errors}
	// shape.
	succeeded, failed := a.svc.Task.BulkTransition(ctx, ids, status)
	return bulkResult(succeeded, failed)
}
