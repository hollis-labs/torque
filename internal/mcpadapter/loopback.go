package mcpadapter

import (
	"context"
	"fmt"
	"time"

	"github.com/hollis-labs/torque/internal/hitl"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// timeParseRFC3339 is a thin wrapper around time.Parse so the loopback
// handler can mirror the cross-task checkpoint tool's timeout_at parsing
// behavior without duplicating the format string.
func timeParseRFC3339(s string) (time.Time, error) { return time.Parse(time.RFC3339, s) }

// NewLoopback returns an Adapter pre-bound to taskID with only the
// interpretive tool subset registered. Designed for the per-task ephemeral
// MCP loopback used by the executor wrapper (CW-20260427-0016): the agent
// connects via stdio pipes paired to the spawning subprocess, and every tool
// call implicitly targets the task being executed — agents don't pass IDs
// and can't act on other tasks.
//
// Tool catalog (loopback subset):
//   - torque_task_summary(text)              — agent's prose summary at done
//   - torque_task_blocked(reason)            — flag blocked + set reason
//   - torque_task_review(reason)             — flag review + comment
//   - torque_task_get(id?)                   — read own task (CW-20260519-0095 Phase 2)
//   - torque_artifact_create                 — task_id implicit
//   - torque_comment_add                     — task_id + author implicit
//   - torque_task_subtodo_add                — task_id implicit
//   - torque_task_subtodo_done               — task_id implicit
//   - torque_task_checkpoint_emit            — ask-for-help (CW-20260519-0095 Phase 2)
//   - torque_task_checkpoint_respond         — respond to own checkpoint
//
// Deterministic lifecycle (started, exited, timeout) is NOT in this catalog
// — the executor wrapper drives those off go-runner events.
// Generic torque_task_transition is excluded; agents drive their own FSM
// only through the curated lifecycle tools above (review / blocked /
// summary). Discovery / list / search are excluded; the agent already has
// its task body in the prompt and uses torque_task_get to read its own
// metadata (notably metadata.checkpoint_responses) on each redispatch.
//
// Phase 2 of the worker-substrate rebuild added the get + checkpoint
// tools so workers can (a) read their checkpoint_responses on
// redispatch — the existing checkpoint-redispatch prompt depended on
// torque_task_get being reachable from the worker side, but that tool
// wasn't in the loopback catalog until this change — and (b) ask for
// help via a typed HITL checkpoint instead of giving up silently.
// Without (a) the redispatch prompt was undeliverable; without (b) the
// help-asking protocol in the worker boot template had no actual
// primitive.
//
// Panics if taskID is empty: a loopback adapter without a bound task is a
// programmer error, never a runtime condition.
func NewLoopback(svc *service.Service, taskID string) *Adapter {
	if taskID == "" {
		panic("mcpadapter: NewLoopback requires non-empty taskID")
	}
	s := server.NewMCPServer(
		"Torque Loopback",
		"0.1.0",
		server.WithToolCapabilities(true),
	)
	a := &Adapter{
		svc:            svc,
		sched:          nil,
		server:         s,
		loopbackTaskID: taskID,
	}
	a.registerLoopbackTools()
	return a
}

func (a *Adapter) registerLoopbackTools() {
	a.addTool(mcp.NewTool("torque_task_summary",
		mcp.WithDescription(`Capture an agent-written summary of what was accomplished this turn. Persisted as a comment with author="agent" so the audit trail stays queryable via torque_comment_search.
Use exactly once per turn, near the end, before the wrapper signals done. The wrapper drives the FSM transition; this is interpretive signal only.
Response shape: data = {<CommentRecord fields>} — singleton.
Example: {"text":"Refactored streamparser per CW-..., added 3 tests, all green."}`),
		mcp.WithString("text", mcp.Required(), mcp.Description("Prose summary of work accomplished")),
	), a.handleLoopbackSummary)

	a.addTool(mcp.NewTool("torque_task_blocked",
		mcp.WithDescription(`Flag the current task as blocked with an explanation. Sets blocked_reason and transitions the task to blocked. Use only when the agent has determined the task cannot proceed without external action; lifecycle (started, exited, timeout) is wrapper-driven and should not be signaled here.
Response shape: data = {task_id, status:"blocked", blocked_reason}.
Example: {"reason":"Need credentials for the staging API; ENV var not set."}`),
		mcp.WithString("reason", mcp.Required(), mcp.Description("Why the task is blocked (persisted as blocked_reason)")),
	), a.handleLoopbackBlocked)

	a.addTool(mcp.NewTool("torque_task_review",
		mcp.WithDescription(`Flag the current task as needing review. The reason is posted as a comment with author="agent" for the reviewer. Transitions the task to review.
Use when the agent has completed enough to warrant review but cannot self-determine done.
Response shape: data = {task_id, status:"review"}.
Example: {"reason":"Implementation done but the test for edge case X is not feasible without prod data."}`),
		mcp.WithString("reason", mcp.Description("Optional context for the reviewer (persisted as a comment)")),
	), a.handleLoopbackReview)

	a.addTool(mcp.NewTool("torque_artifact_create",
		mcp.WithDescription(`Create an artifact (file pointer, URL, or inline content) attached to the current task; returns the persisted ArtifactRecord.
The current task is implicit (loopback context) — agents cannot create artifacts for other tasks via this server.
Response shape: data = {<ArtifactRecord fields>} — singleton.
Example: {"type":"file","file_path":"/tmp/report.md"}`),
		mcp.WithString("type", mcp.Required(), mcp.Description("Artifact type (file|url|inline|diff|...)")),
		mcp.WithString("content", mcp.Description("Inline content (for type=inline)")),
		mcp.WithString("url", mcp.Description("URL (for type=url)")),
		mcp.WithString("file_path", mcp.Description("Filesystem path (for type=file)")),
	), a.handleLoopbackArtifactCreate)

	a.addTool(mcp.NewTool("torque_comment_add",
		mcp.WithDescription(`Append a comment (freeform prose) to the current task; returns the persisted CommentRecord.
The current task is implicit (loopback context). Author defaults to "agent". Use for agent-to-user channel; for the canonical end-of-turn summary use torque_task_summary instead.
Response shape: data = {<CommentRecord fields>} — singleton.
Example: {"content":"Investigating the auth flow; see file X for context."}`),
		mcp.WithString("content", mcp.Required(), mcp.Description("Comment body (prose)")),
	), a.handleLoopbackCommentAdd)

	a.addTool(mcp.NewTool("torque_task_subtodo_add",
		mcp.WithDescription(`Append a subtodo item to the current task's checklist. required=true means the task cannot transition past review until the item is ticked off.
The current task is implicit (loopback context).
Response shape: data = [<Subtodo>...] — returns the updated full checklist.
Example: {"id":"check-1","text":"write regression test","required":true}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Item id (unique per task)")),
		mcp.WithString("text", mcp.Required(), mcp.Description("Human-readable description")),
		mcp.WithBoolean("required", mcp.Description("If true, blocks done until ticked off (default false)")),
	), a.handleLoopbackSubtodoAdd)

	a.addTool(mcp.NewTool("torque_task_subtodo_done",
		mcp.WithDescription(`Mark a subtodo done with an evidence string (artifact id, commit SHA, URL, or note). Operates on the current task's checklist.
Response shape: data = [<Subtodo>...] — returns the updated full checklist.
Example: {"id":"check-1","evidence":"abc123 / PR #42"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Item id to mark done")),
		mcp.WithString("evidence", mcp.Description("Optional evidence pointer (artifact id, commit, URL, or note)")),
	), a.handleLoopbackSubtodoDone)

	// Phase 2 of the worker-substrate rebuild (CW-20260519-0095): read +
	// help-asking primitives on the worker's own task surface. Without
	// these the existing checkpoint-redispatch protocol in the boot prompt
	// (read task.metadata.checkpoint_responses on each turn) could not be
	// followed by workers, and there was no way for a stuck worker to ask
	// for help short of going silent.

	a.addTool(mcp.NewTool("torque_task_get",
		mcp.WithDescription(`Fetch your own task record (the loopback's bound task), including metadata.checkpoint_responses. The id argument is accepted for symmetry with the cross-task tool but is validated against the loopback's bound task — passing a different id returns an error.
Use at the start of each turn to inspect any checkpoint responses that arrived since your last action; the substrate redispatches you when a response lands.
Response shape: data = {<TaskRecord fields>, Tags[]} — singleton, PascalCase keys.
Example: {} or {"id":"<your-own-task-id>"}`),
		mcp.WithString("id", mcp.Description("Optional — must match the loopback's bound task when supplied")),
	), a.handleLoopbackTaskGet)

	a.addTool(mcp.NewTool("torque_task_checkpoint_emit",
		mcp.WithDescription(fmt.Sprintf(`Emit a typed HITL checkpoint on your own task — the help-asking primitive. The task_id is implicit (loopback context).
Use when blocked, when scope is unclear, or when a decision needs a human. Workers MUST NOT exit silently — emit a checkpoint and the substrate will redispatch you with the operator response. Cancel with torque_task_checkpoint_respond once the answer is consumed.
Canonical HITL types: %s, %s, %s. Payload contracts: pr_review={pr_url,title?,summary?,branch?,checklist?}; approval={title,prompt,context?,options?}; message={subject?,message,severity?,context?}.
Response shape: data = {<CheckpointRecord fields>} — singleton with correlation_id, status="pending".
Example: {"type":"%s","payload_json":"{\"title\":\"Need DB creds\",\"prompt\":\"Should I block until provided or skip the migration step?\"}"}`,
			hitl.TypePRReview, hitl.TypeApproval, hitl.TypeMessage, hitl.TypeApproval)),
		mcp.WithString("type", mcp.Required(), mcp.Description("Workflow type. Canonical: pr_review|approval|message")),
		mcp.WithString("payload_json", mcp.Required(), mcp.Description("JSON payload matching the type's HITL workflow schema")),
		mcp.WithString("timeout_at", mcp.Description("Optional RFC3339 timestamp for the timeout sweeper")),
	), a.handleLoopbackCheckpointEmit)

	a.addTool(mcp.NewTool("torque_task_checkpoint_respond",
		mcp.WithDescription(`Resolve a pending checkpoint on your own task with a JSON response. Only checkpoints whose task_id matches the loopback's bound task are accepted — passing a correlation_id for another task's checkpoint returns an error.
Use to consume a checkpoint response after you have acted on it (so a later redispatch does not re-process the same response). Most workers do NOT need this — the substrate parks responses in task.metadata.checkpoint_responses[corr] and the redispatch contract documents how to handle them; respond is for workers that explicitly close the loop.
Typed response contracts: pr_review={decision:"approve|request_changes|comment",summary?,comments?,required_changes?}; approval={decision:"approved|rejected|needs_info",comment?}; message={acknowledged:boolean,reply?}.
Response shape: data = {<CheckpointRecord fields>} — singleton, status="responded".
Example: {"correlation_id":"01HK...","response_json":"{\"decision\":\"approved\",\"comment\":\"Proceeding.\"}"}`),
		mcp.WithString("correlation_id", mcp.Required(), mcp.Description("Checkpoint correlation_id (ULID)")),
		mcp.WithString("response_json", mcp.Required(), mcp.Description("JSON response matching the checkpoint type's HITL response schema")),
	), a.handleLoopbackCheckpointRespond)
}

// ---- handlers --------------------------------------------------------------

func (a *Adapter) handleLoopbackSummary(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	text := reqStr(req, "text")
	if text == "" {
		return errResult(ErrCodeArgInvalid, "text is required", "text")
	}
	comment, err := a.svc.Comment.AddForTask(a.loopbackTaskID, "agent", "Summary: "+text)
	if err != nil {
		return errFromService(err)
	}
	return okResult(comment)
}

func (a *Adapter) handleLoopbackBlocked(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	reason := reqStr(req, "reason")
	if reason == "" {
		return errResult(ErrCodeArgInvalid, "reason is required", "reason")
	}

	// Set blocked_reason via Update before transitioning so the FSM observer
	// sees the reason at the moment of state change.
	if err := a.svc.Task.Update(a.loopbackTaskID, service.TaskUpdateInput{
		TaskUpdate: sqlstore.TaskUpdate{BlockedReason: &reason},
	}); err != nil {
		return errFromService(err)
	}
	if err := a.svc.Task.Transition(a.loopbackTaskID, "blocked"); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]interface{}{
		"task_id":        a.loopbackTaskID,
		"status":         "blocked",
		"blocked_reason": reason,
	})
}

func (a *Adapter) handleLoopbackReview(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	reason := reqStr(req, "reason")
	if reason != "" {
		if _, err := a.svc.Comment.AddForTask(a.loopbackTaskID, "agent", "Review note: "+reason); err != nil {
			return errFromService(err)
		}
	}
	if err := a.svc.Task.Transition(a.loopbackTaskID, "review"); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]interface{}{
		"task_id": a.loopbackTaskID,
		"status":  "review",
	})
}

func (a *Adapter) handleLoopbackArtifactCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rec := &sqlstore.ArtifactRecord{
		TaskID:   a.loopbackTaskID,
		Type:     reqStr(req, "type"),
		Content:  reqStr(req, "content"),
		URL:      reqStr(req, "url"),
		FilePath: reqStr(req, "file_path"),
	}
	if rec.Type == "" {
		return errResult(ErrCodeArgInvalid, "type is required", "type")
	}
	if err := a.svc.Artifact.Create(rec); err != nil {
		return errFromService(err)
	}
	return okResult(rec)
}

func (a *Adapter) handleLoopbackCommentAdd(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	content := reqStr(req, "content")
	if content == "" {
		return errResult(ErrCodeArgInvalid, "content is required", "content")
	}
	comment, err := a.svc.Comment.AddForTask(a.loopbackTaskID, "agent", content)
	if err != nil {
		return errFromService(err)
	}
	return okResult(comment)
}

func (a *Adapter) handleLoopbackSubtodoAdd(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")
	text := reqStr(req, "text")
	if id == "" {
		return errResult(ErrCodeArgInvalid, "id is required", "id")
	}
	if text == "" {
		return errResult(ErrCodeArgInvalid, "text is required", "text")
	}
	items, err := a.svc.Task.AddSubtodo(a.loopbackTaskID, sqlstore.Subtodo{
		ID:       id,
		Text:     text,
		Required: reqBool(req, "required"),
	})
	if err != nil {
		return errFromService(err)
	}
	return okResult(items)
}

func (a *Adapter) handleLoopbackSubtodoDone(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")
	if id == "" {
		return errResult(ErrCodeArgInvalid, "id is required", "id")
	}
	items, err := a.svc.Task.MarkSubtodoDone(a.loopbackTaskID, id, reqStr(req, "evidence"))
	if err != nil {
		return errFromService(err)
	}
	return okResult(items)
}

// handleLoopbackTaskGet returns the worker's own TaskRecord. The id argument
// is accepted for symmetry with the cross-task tool but is REQUIRED to
// match the bound task — letting a worker pass an arbitrary id would
// silently widen the worker's read surface beyond the self-task subset
// the loopback's contract promises. Passing no id (`{}`) is the canonical
// shape; the worker's id is implicit.
func (a *Adapter) handleLoopbackTaskGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if requested := reqStr(req, "id"); requested != "" && requested != a.loopbackTaskID {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("loopback task_get is pinned to %s; cannot read task %s", a.loopbackTaskID, requested), "id")
	}
	task, err := a.svc.Task.Get(a.loopbackTaskID)
	if err != nil {
		return errFromService(err)
	}
	return a.taskResult(task)
}

// handleLoopbackCheckpointEmit emits a typed HITL checkpoint on the worker's
// own task. task_id is hardcoded to the loopback's bound task; the worker
// cannot emit checkpoints on other tasks. EmitterSourceType is forced to
// "agent" so dashboards see the right provenance without the worker having
// to set it correctly (the cross-task tool defaulted to "system" when
// omitted, which would mislabel worker-emitted checkpoints).
func (a *Adapter) handleLoopbackCheckpointEmit(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	in := service.CheckpointEmitInput{
		TaskID:            a.loopbackTaskID,
		Type:              reqStr(req, "type"),
		PayloadJSON:       reqStr(req, "payload_json"),
		EmitterSourceType: "agent",
	}
	if raw := reqStr(req, "timeout_at"); raw != "" {
		// Mirror the cross-task tool's RFC3339 parse path. Bad timestamps
		// are a worker mistake worth surfacing instead of silently
		// dropping — a checkpoint that never times out and never gets
		// answered is a way to wedge a task.
		t, err := timeParseRFC3339(raw)
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

// handleLoopbackCheckpointRespond resolves a checkpoint on the worker's own
// task. Validates that the correlation_id points at a checkpoint whose
// task_id matches the loopback's bound task — without this guard a worker
// could respond to (i.e. consume + close) checkpoints on any task, which
// would let a worker accidentally close another worker's pending question
// to the user. ResponderSourceType is forced to "agent" (same rationale as
// emit's EmitterSourceType — workers always speak as agent).
func (a *Adapter) handleLoopbackCheckpointRespond(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	corr := reqStr(req, "correlation_id")
	if corr == "" {
		return errResult(ErrCodeArgInvalid, "correlation_id is required", "correlation_id")
	}
	cp, err := a.svc.Checkpoint.Get(corr)
	if err != nil {
		return errFromService(err)
	}
	if cp.TaskID != a.loopbackTaskID {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("loopback checkpoint_respond is pinned to %s; correlation_id %s belongs to task %s", a.loopbackTaskID, corr, cp.TaskID), "correlation_id")
	}
	if err := a.svc.Checkpoint.Respond(service.CheckpointRespondInput{
		CorrelationID:       corr,
		ResponseJSON:        reqStr(req, "response_json"),
		ResponderSourceType: "agent",
	}); err != nil {
		return errFromService(err)
	}
	return a.checkpointResultByCorr(corr)
}
