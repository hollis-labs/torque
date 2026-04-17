package mcpadapter

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerTaskTools() {
	a.server.AddTool(mcp.NewTool("clockwork_task_create",
		mcp.WithDescription("Create a new task"),
		mcp.WithString("title", mcp.Required(), mcp.Description("Task title")),
		mcp.WithString("description", mcp.Required(), mcp.Description("Task description")),
		mcp.WithNumber("priority", mcp.Description("Priority 1-5 (default 2)")),
		mcp.WithString("tags", mcp.Description("JSON array of tag strings")),
		mcp.WithString("executor", mcp.Description("Executor type (default cli)")),
		mcp.WithString("agent_profile", mcp.Description("Agent profile name")),
		mcp.WithString("working_dir", mcp.Description("Working directory")),
		mcp.WithString("system_prompt", mcp.Description("System prompt override")),
		mcp.WithString("agent_file", mcp.Description("Absolute or working_dir-relative path to a YAML agent spec; loaded at dispatch")),
		mcp.WithString("on_done", mcp.Description("Hook on done")),
		mcp.WithString("on_fail", mcp.Description("Hook on fail")),
		mcp.WithString("on_done_merge", mcp.Description("Merge hook on done")),
		mcp.WithString("depends_on", mcp.Description("JSON array of dependency task IDs")),
		mcp.WithBoolean("manual", mcp.Description("Whether the task is manual")),
		mcp.WithString("sprint_id", mcp.Description("Sprint ID to associate this task with (requires features.sprints)")),
		mcp.WithString("project_id", mcp.Description("Project ID to associate this task with (requires features.projects)")),
		mcp.WithString("epic_id", mcp.Description("Epic ID to associate this task with (requires features.epics)")),
		mcp.WithString("kind", mcp.Description("agent|external|wait|decision|parent (default agent)")),
		mcp.WithString("source_type", mcp.Description("agent|user|api|system|webhook|import (default user)")),
		mcp.WithString("source_ref", mcp.Description("Originating slug/id (free-form)")),
		mcp.WithString("trust", mcp.Description("trusted|normal|untrusted (defaulted by source_type)")),
		mcp.WithString("checkpoint_mode", mcp.Description("none|blocking|non_blocking (default none)")),
		mcp.WithString("on_checkpoint_response", mcp.Description("resume|review|custom (default resume)")),
		mcp.WithString("metadata", mcp.Description("JSON object: freeform metadata, including metadata.wait for kind=wait")),
		mcp.WithString("parent_id", mcp.Description("Optional parent task ID (migration 013). Empty = top of lineage.")),
	), a.handleTaskCreate)

	a.server.AddTool(mcp.NewTool("clockwork_task_get",
		mcp.WithDescription("Get a task by ID"),
		mcp.WithString("id", mcp.Required(), mcp.Description("Task ID")),
	), a.handleTaskGet)

	a.server.AddTool(mcp.NewTool("clockwork_task_list",
		mcp.WithDescription("List tasks with optional filters"),
		mcp.WithString("status", mcp.Description("Filter by status")),
		mcp.WithNumber("priority", mcp.Description("Filter by priority")),
		mcp.WithString("executor", mcp.Description("Filter by executor")),
		mcp.WithString("kind", mcp.Description("Filter by kind (agent|external|wait|decision|parent)")),
		mcp.WithString("source_type", mcp.Description("Filter by source_type")),
		mcp.WithString("source_ref", mcp.Description("Filter by source_ref")),
		mcp.WithString("trust", mcp.Description("Filter by trust (trusted|normal|untrusted)")),
		mcp.WithString("checkpoint_mode", mcp.Description("Filter by checkpoint_mode")),
		mcp.WithString("parent_id", mcp.Description("Filter by parent_id; pass 'null' to return root tasks")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 50)")),
	), a.handleTaskList)

	a.server.AddTool(mcp.NewTool("clockwork_task_update",
		mcp.WithDescription("Update a task"),
		mcp.WithString("id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("title", mcp.Description("New title")),
		mcp.WithString("description", mcp.Description("New description")),
		mcp.WithNumber("priority", mcp.Description("New priority")),
		mcp.WithBoolean("manual", mcp.Description("Manual flag")),
		mcp.WithString("executor", mcp.Description("Executor type")),
		mcp.WithString("agent_profile", mcp.Description("Agent profile name")),
		mcp.WithString("working_dir", mcp.Description("Working directory")),
		mcp.WithString("system_prompt", mcp.Description("System prompt override")),
		mcp.WithString("agent_file", mcp.Description("Absolute or working_dir-relative path to a YAML agent spec; pass empty string to clear")),
		mcp.WithString("on_done", mcp.Description("Hook on done (close|review|notify)")),
		mcp.WithString("on_fail", mcp.Description("Hook on fail (retry|block|escalate|notify)")),
		mcp.WithString("on_review", mcp.Description("Hook on review (pause|notify|auto-approve)")),
		mcp.WithString("on_done_merge", mcp.Description("Merge hook on done (none|auto|pr|auto-resolve)")),
		mcp.WithString("deliverable_preset", mcp.Description("Deliverable preset name")),
		mcp.WithString("blocked_reason", mcp.Description("Blocked reason")),
		mcp.WithNumber("cost_budget", mcp.Description("Cost budget (-1 unlimited, 0 none, or positive)")),
		mcp.WithNumber("max_retries", mcp.Description("Max retries (non-negative integer)")),
		mcp.WithNumber("max_duration_ms", mcp.Description("Max duration in ms (-1 unlimited or positive)")),
		mcp.WithNumber("token_budget", mcp.Description("Token budget (-1 unlimited or positive)")),
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
		mcp.WithString("kind", mcp.Description("New kind (agent|external|wait|decision|parent)")),
		mcp.WithString("source_type", mcp.Description("New source_type")),
		mcp.WithString("source_ref", mcp.Description("New source_ref (empty string clears)")),
		mcp.WithString("trust", mcp.Description("New trust (trusted|normal|untrusted)")),
		mcp.WithString("checkpoint_mode", mcp.Description("New checkpoint_mode (none|blocking|non_blocking)")),
		mcp.WithString("on_checkpoint_response", mcp.Description("New on_checkpoint_response (resume|review|custom)")),
		mcp.WithString("parent_id", mcp.Description("New parent_id (migration 013; empty string clears the parent)")),
	), a.handleTaskUpdate)

	a.server.AddTool(mcp.NewTool("clockwork_task_delete",
		mcp.WithDescription("Delete a task"),
		mcp.WithString("id", mcp.Required(), mcp.Description("Task ID")),
	), a.handleTaskDelete)

	a.server.AddTool(mcp.NewTool("clockwork_task_transition",
		mcp.WithDescription("Transition a task to a new status"),
		mcp.WithString("id", mcp.Required(), mcp.Description("Task ID")),
		mcp.WithString("status", mcp.Required(), mcp.Description("Target status")),
	), a.handleTaskTransition)

	a.server.AddTool(mcp.NewTool("clockwork_task_search",
		mcp.WithDescription("Search tasks by text query"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
	), a.handleTaskSearch)

	a.server.AddTool(mcp.NewTool("clockwork_task_bulk_transition",
		mcp.WithDescription("Transition multiple tasks to a new status"),
		mcp.WithString("ids", mcp.Required(), mcp.Description("JSON array of task IDs")),
		mcp.WithString("status", mcp.Required(), mcp.Description("Target status")),
	), a.handleTaskBulkTransition)
}

// taskWithTags is an MCP result shape that flattens a TaskRecord's fields
// and appends a Tags key alongside them (via Go's embedded-struct JSON
// marshaling). The MCP adapter marshals TaskRecord with Go's default
// capitalization (no json tags on TaskRecord), so this struct keeps Tags
// capitalized to stay consistent with the surrounding fields — this is
// intentionally different from the HTTP API, which lowercases everything
// via an explicit taskJSON builder.
type taskWithTags struct {
	*sqlstore.TaskRecord
	Tags []sqlstore.TagRecord `json:"Tags"`
}

// taskResult loads the linked tags for a task and returns a combined MCP result.
func (a *Adapter) taskResult(task *sqlstore.TaskRecord) (*mcp.CallToolResult, error) {
	tags, err := a.svc.Task.ListTags(task.ID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if tags == nil {
		tags = []sqlstore.TagRecord{}
	}
	return jsonResult(taskWithTags{TaskRecord: task, Tags: tags})
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
		log.Printf("clockwork_task_create: forcing manual=true on %q (caller passed manual=false) — safety override per CW-20260417-0133", title)
		manual = true
	}

	input := service.TaskCreateInput{
		Title:                title,
		Description:          reqStr(req, "description"),
		Priority:             reqInt(req, "priority"),
		Executor:             reqStr(req, "executor"),
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

		ParentID: reqStr(req, "parent_id"),
	}

	if raw := reqStr(req, "tags"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &input.Tags); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid tags JSON: %v", err)), nil
		}
	}
	if raw := reqStr(req, "depends_on"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &input.DependsOn); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid depends_on JSON: %v", err)), nil
		}
	}
	if raw := reqStr(req, "metadata"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &input.Metadata); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid metadata JSON: %v", err)), nil
		}
	}

	task, err := a.svc.Task.Create(input)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return a.taskResult(task)
}

func (a *Adapter) handleTaskGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	task, err := a.svc.Task.Get(reqStr(req, "id"))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return a.taskResult(task)
}

func (a *Adapter) handleTaskList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := reqInt(req, "limit")
	if limit == 0 {
		limit = 50
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
		Limit:          limit,
	}
	if _, ok := req.GetArguments()["parent_id"]; ok {
		v := reqStr(req, "parent_id")
		if v == "" || v == "null" {
			filter.ParentIDNull = true
		} else {
			filter.ParentID = v
		}
	}
	tasks, err := a.svc.Task.List(filter)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(tasks)
}

func (a *Adapter) handleTaskUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")
	update := sqlstore.TaskUpdate{}
	args := req.GetArguments()

	if v := reqStr(req, "title"); v != "" {
		update.Title = &v
	}
	if v := reqStr(req, "description"); v != "" {
		update.Description = &v
	}
	if v := reqInt(req, "priority"); v != 0 {
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
	var scratchArr []any
	var scratchObj map[string]any
	for _, k := range []string{"tools", "files", "escalation_chain", "quality_gates", "deliverables", "depends_on"} {
		if err := unmarshalBlob(k, &scratchArr); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
	}
	for _, k := range []string{"permissions", "environment", "metadata"} {
		if err := unmarshalBlob(k, &scratchObj); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
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
	if ns := nullFromRaw("depends_on"); ns != nil {
		update.DependsOn = ns
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
	if raw := reqStr(req, "tags"); raw != "" {
		var slugs []string
		if err := json.Unmarshal([]byte(raw), &slugs); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid tags JSON: %v", err)), nil
		}
		input.Tags = &slugs
	}

	if err := a.svc.Task.Update(id, input); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	task, err := a.svc.Task.Get(id)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return a.taskResult(task)
}

func (a *Adapter) handleTaskDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := a.svc.Task.Delete(reqStr(req, "id")); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText("deleted"), nil
}

func (a *Adapter) handleTaskTransition(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := a.svc.Task.Transition(reqStr(req, "id"), reqStr(req, "status")); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	task, err := a.svc.Task.Get(reqStr(req, "id"))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return a.taskResult(task)
}

func (a *Adapter) handleTaskSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tasks, err := a.svc.Task.Search(reqStr(req, "query"))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(tasks)
}

func (a *Adapter) handleTaskBulkTransition(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw := reqStr(req, "ids")
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid ids JSON: %v", err)), nil
	}
	status := reqStr(req, "status")
	success, errs := a.svc.Task.BulkTransition(ids, status)

	var errMsgs []string
	for _, e := range errs {
		errMsgs = append(errMsgs, e.Error())
	}

	result := map[string]interface{}{
		"success": success,
		"failed":  len(errs),
	}
	if len(errMsgs) > 0 {
		result["errors"] = strings.Join(errMsgs, "; ")
	}
	return jsonResult(result)
}
