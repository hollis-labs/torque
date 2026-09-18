package mcpadapter

import (
	"context"
	"fmt"
	"time"

	gomcp "github.com/hollis-labs/go-mcp/server"
	"github.com/hollis-labs/torque/internal/hitl"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
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
//   - torque_comment_add                     — task_id implicit; author optional
//   - torque_task_subtodo_add                — task_id implicit
//   - torque_task_subtodo_done               — task_id implicit
//   - torque_task_checkpoint_emit            — ask-for-help (CW-20260519-0095 Phase 2)
//   - torque_task_checkpoint_respond         — respond to own checkpoint
//   - torque_aar_submit                      — file the run's After-Action
//     Report (CW-20260519-0088)
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
	s := gomcp.NewServer("Torque Loopback", "0.1.0")
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
	a.addTool(newTool("torque_task_summary",
		withDescription(`Capture an agent-written summary of what was accomplished this turn. Persisted as a comment with author="agent" so the audit trail stays queryable via torque_comment_search.
Use exactly once per turn, near the end, before the wrapper signals done. The wrapper drives the FSM transition; this is interpretive signal only.
Response shape: data = {<CommentRecord fields>} — singleton.
Example: {"text":"Refactored streamparser per CW-..., added 3 tests, all green."}`),
		withString("text", required(), desc("Prose summary of work accomplished")),
	), a.handleLoopbackSummary)

	a.addTool(newTool("torque_task_blocked",
		withDescription(`Flag the current task as blocked with an explanation. Sets blocked_reason and transitions the task to blocked. Use only when the agent has determined the task cannot proceed without external action; lifecycle (started, exited, timeout) is wrapper-driven and should not be signaled here.
Response shape: data = {task_id, status:"blocked", blocked_reason}.
Example: {"reason":"Need credentials for the staging API; ENV var not set."}`),
		withString("reason", required(), desc("Why the task is blocked (persisted as blocked_reason)")),
	), a.handleLoopbackBlocked)

	a.addTool(newTool("torque_task_review",
		withDescription(`Flag the current task as needing review. The reason is posted as a comment with author="agent" for the reviewer. Transitions the task to review.
Use when the agent has completed enough to warrant review but cannot self-determine done.
Response shape: data = {task_id, status:"review"}.
Example: {"reason":"Implementation done but the test for edge case X is not feasible without prod data."}`),
		withString("reason", desc("Optional context for the reviewer (persisted as a comment)")),
	), a.handleLoopbackReview)

	a.addTool(newTool("torque_artifact_create",
		withDescription(`Create an artifact (file pointer, URL, or inline content) attached to the current task; returns the persisted ArtifactRecord.
The current task is implicit (loopback context) — agents cannot create artifacts for other tasks via this server.
Response shape: data = {<ArtifactRecord fields>} — singleton.
Example: {"type":"file","file_path":"/tmp/report.md"}`),
		withString("type", required(), desc("Artifact type (file|url|inline|diff|...)")),
		withString("content", desc("Inline content (for type=inline)")),
		withString("url", desc("URL (for type=url)")),
		withString("file_path", desc("Filesystem path (for type=file)")),
	), a.handleLoopbackArtifactCreate)

	a.addTool(newTool("torque_comment_add",
		withDescription(`Append a comment (freeform prose) to the current task; returns the persisted CommentRecord.
The current task is implicit (loopback context). Optional author records caller attribution, matching the global comment tool; it defaults to "agent". Use for agent-to-user channel; for the canonical end-of-turn summary use torque_task_summary instead.
Response shape: data = {<CommentRecord fields>} — singleton.
Example: {"content":"Investigating the auth flow; see file X for context."}`),
		withString("content", required(), desc("Comment body (prose)")),
		withString("author", desc("Caller attribution; defaults to agent")),
	), a.handleLoopbackCommentAdd)

	a.addTool(newTool("torque_task_subtodo_add",
		withDescription(`Append a subtodo item to the current task's checklist. required=true means the task cannot transition past review until the item is ticked off.
The current task is implicit (loopback context).
Response shape: data = [<Subtodo>...] — returns the updated full checklist.
Example: {"id":"check-1","text":"write regression test","required":true}
Example, id omitted (server generates one): {"text":"write regression test","required":true}`),
		withString("id", desc("Item id (unique per task). Omit to auto-generate a server-assigned id; supply a meaningful slug (e.g. \"check-auth-flow\") to keep it deterministic across repeated emits")),
		withString("text", required(), desc("Human-readable description")),
		withBoolean("required", desc("If true, blocks done until ticked off (default false)")),
	), a.handleLoopbackSubtodoAdd)

	a.addTool(newTool("torque_task_subtodo_done",
		withDescription(`Mark a subtodo done with an evidence string (artifact id, commit SHA, URL, or note). Operates on the current task's checklist.
Response shape: data = [<Subtodo>...] — returns the updated full checklist.
Example: {"id":"check-1","evidence":"abc123 / PR #42"}`),
		withString("id", required(), desc("Item id to mark done")),
		withString("evidence", desc("Optional evidence pointer (artifact id, commit, URL, or note)")),
	), a.handleLoopbackSubtodoDone)

	// Phase 2 of the worker-substrate rebuild (CW-20260519-0095): read +
	// help-asking primitives on the worker's own task surface. Without
	// these the existing checkpoint-redispatch protocol in the boot prompt
	// (read task.metadata.checkpoint_responses on each turn) could not be
	// followed by workers, and there was no way for a stuck worker to ask
	// for help short of going silent.

	a.addTool(newTool("torque_task_get",
		withDescription(fmt.Sprintf(`Fetch your own task record (the loopback's bound task), including metadata.checkpoint_responses and BY DEFAULT the %d most recent comments on the task. The id argument is accepted for symmetry with the cross-task tool but is validated against the loopback's bound task — passing a different id returns an error.
Use at the start of each turn to inspect any checkpoint responses that arrived since your last action; the substrate redispatches you when a response lands.
READ THE COMMENTS. Your task's description is what someone wrote before the work started; corrections, scope changes and answers posted afterwards live in the comment thread, and acting on the description alone is how a worker executes a framing that has since been superseded (CW-20260910-0057). Pass comments="false" to opt out, or comments_limit to widen the window (max %d).
The window is the NEWEST comments_limit comments, presented oldest → newest so successive corrections read forward. CommentsMeta is ALWAYS present when comments were requested and states {returned, total, omitted, truncated} — including omitted=0 when nothing was cut, so you never have to infer completeness from array length. If omitted > 0 the hint names the call that retrieves the rest.
Response shape: data = {<TaskRecord fields>, Tags[], DependsOn[], Comments[], CommentsMeta} — singleton, PascalCase keys (Comments[] entries are lowercase: id, author, content, created_at, updated_at). Comments and CommentsMeta are absent entirely when comments="false".
Example: {} or {"id":"<your-own-task-id>"}
Example, record only: {"comments":"false"}`, defaultTaskGetCommentsLimit, maxTaskGetCommentsLimit)),
		withString("id", desc("Optional — must match the loopback's bound task when supplied")),
		withString("comments", desc("Include the comment tail (string 'true'/'false', default 'true'). Set 'false' for the bare record.")),
		withString("comments_limit", desc(fmt.Sprintf("How many of the newest comments to include (integer, default %d, max %d). Ignored when comments='false'.", defaultTaskGetCommentsLimit, maxTaskGetCommentsLimit))),
	), a.handleLoopbackTaskGet)

	a.addTool(newTool("torque_task_checkpoint_emit",
		withDescription(fmt.Sprintf(`Emit a typed HITL checkpoint on your own task — the help-asking primitive. The task_id is implicit (loopback context).
Use when blocked, when scope is unclear, or when a decision needs a human. Workers MUST NOT exit silently — emit a checkpoint and wait for the operator to respond (or cancel) from a dashboard. The substrate then redispatches you with the response in task.metadata.checkpoint_responses[correlation_id]; read it on your next turn with torque_task_get and incorporate it into your next action.
Lifecycle: emit (status=pending) → operator responds via the cross-task tool (status=responded) OR operator cancels (status=canceled) → substrate redispatches you with the response (if any) parked on your task metadata. The worker does NOT call torque_task_checkpoint_respond to acknowledge or close — that tool CREATES the response on a still-pending checkpoint and is intended for the dashboard / operator side.
Canonical HITL types: %s, %s, %s. Payload contracts: pr_review={pr_url,title?,summary?,branch?,checklist?}; approval={title,prompt,context?,options?}; message={subject?,message,severity?,context?}.
Response shape: data = {<CheckpointRecord fields>} — singleton with correlation_id, status="pending".
Example: {"type":"%s","payload_json":"{\"title\":\"Need DB creds\",\"prompt\":\"Should I block until provided or skip the migration step?\"}"}`,
			hitl.TypePRReview, hitl.TypeApproval, hitl.TypeMessage, hitl.TypeApproval)),
		withString("type", required(), desc("Workflow type. Canonical: pr_review|approval|message")),
		withString("payload_json", required(), desc("JSON payload matching the type's HITL workflow schema")),
		withString("timeout_at", desc("Optional RFC3339 timestamp for the timeout sweeper")),
	), a.handleLoopbackCheckpointEmit)

	// torque_steering_dismiss (CW-20260519-0065): the agent's "ack, I saw
	// these steering messages" signal. The substrate records every
	// successfully injected steering envelope and re-surfaces unaddressed
	// ones at the next turn boundary; calling this tool with the envelope
	// IDs the agent has handled (or consciously chosen to ignore) stops
	// the re-reminder for those envelopes. Idempotent and tolerant of
	// unknown ids — the response reports which IDs were actually present
	// in the registry so the agent knows what was acked vs. silently
	// dropped (e.g. a stale id from a prior process). Always registered
	// in the loopback subset; when no registry is wired (test paths) the
	// tool returns a "feature disabled" error rather than panicking.
	a.addTool(newTool("torque_steering_dismiss",
		withDescription(`Acknowledge ("dismiss") one or more injected steering envelopes so the substrate stops re-surfacing them at turn boundaries (CW-20260519-0065). Pass the envelope IDs the substrate listed in its "[steering reminder · N unaddressed messages]" turn — those IDs appear as "envelope=ENV-XXX" in the reminder body.
Dismissal is per-envelope and permanent for this task: a dismissed envelope is never re-surfaced even if the operator sends new steering messages. Dismissing is the agent's "I saw this and chose not to act" signal; an envelope you've already replied to or otherwise handled should also be dismissed to keep the registry clean.
Response shape: data = {dismissed: [<envelope-id>...], unknown: [<envelope-id>...]} — dismissed lists IDs that were actually present in the registry; unknown lists IDs that were absent (stale, already dismissed, or never injected).
Example: {"envelope_ids":["01HK...","01HJ..."]}`),
		withArray("envelope_ids",
			required(),
			desc("Envelope IDs to dismiss (as listed in the reminder body)"),
			items(map[string]any{"type": "string"}),
		),
	), a.handleLoopbackSteeringDismiss)

	a.registerLoopbackAARTool()

	a.addTool(newTool("torque_task_checkpoint_respond",
		withDescription(`CREATE a response on a still-pending checkpoint that lives on your own task. Transitions the checkpoint from status=pending → status=responded. Only checkpoints whose task_id matches the loopback's bound task are accepted — passing a correlation_id for another task's checkpoint returns an error. Responding to a non-pending checkpoint (already responded, canceled, or timed out) returns a conflict error.
This tool exists for narrow self-service cases where a worker decides on its own behalf — e.g. an automated approval flow where the worker handles both sides of the loop. The typical worker DOES NOT call this: when a human operator responds to your help-asking checkpoint from a dashboard, the substrate parks the response in task.metadata.checkpoint_responses[correlation_id] and redispatches you automatically. Read it from torque_task_get and act on it; do NOT call respond to "consume" or "acknowledge" — there is no acknowledge primitive, and calling respond on a checkpoint someone else already responded to will fail with conflict.
Typed response contracts: pr_review={decision:"approve|request_changes|comment",summary?,comments?,required_changes?}; approval={decision:"approved|rejected|needs_info",comment?}; message={acknowledged:boolean,reply?}.
Response shape: data = {<CheckpointRecord fields>} — singleton, status="responded".
Example: {"correlation_id":"01HK...","response_json":"{\"decision\":\"approved\",\"comment\":\"Proceeding.\"}"}`),
		withString("correlation_id", required(), desc("Checkpoint correlation_id (ULID); must point at a still-pending checkpoint on the loopback's bound task")),
		withString("response_json", required(), desc("JSON response matching the checkpoint type's HITL response schema")),
	), a.handleLoopbackCheckpointRespond)
}

// ---- handlers --------------------------------------------------------------

func (a *Adapter) handleLoopbackSummary(ctx context.Context, req map[string]any) (any, error) {
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

func (a *Adapter) handleLoopbackBlocked(ctx context.Context, req map[string]any) (any, error) {
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
	if err := a.svc.Task.Transition(ctx, a.loopbackTaskID, "blocked"); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]interface{}{
		"task_id":        a.loopbackTaskID,
		"status":         "blocked",
		"blocked_reason": reason,
	})
}

func (a *Adapter) handleLoopbackReview(ctx context.Context, req map[string]any) (any, error) {
	reason := reqStr(req, "reason")
	if reason != "" {
		if _, err := a.svc.Comment.AddForTask(a.loopbackTaskID, "agent", "Review note: "+reason); err != nil {
			return errFromService(err)
		}
	}
	if err := a.svc.Task.Transition(ctx, a.loopbackTaskID, "review"); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]interface{}{
		"task_id": a.loopbackTaskID,
		"status":  "review",
	})
}

func (a *Adapter) handleLoopbackArtifactCreate(ctx context.Context, req map[string]any) (any, error) {
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

func (a *Adapter) handleLoopbackCommentAdd(ctx context.Context, req map[string]any) (any, error) {
	content := reqStr(req, "content")
	if content == "" {
		return errResult(ErrCodeArgInvalid, "content is required", "content")
	}
	author := reqStr(req, "author")
	if author == "" {
		author = "agent"
	}
	comment, err := a.svc.Comment.AddForTask(a.loopbackTaskID, author, content)
	if err != nil {
		return errFromService(err)
	}
	return okResult(comment)
}

func (a *Adapter) handleLoopbackSubtodoAdd(ctx context.Context, req map[string]any) (any, error) {
	id := reqStr(req, "id")
	text := reqStr(req, "text")
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

func (a *Adapter) handleLoopbackSubtodoDone(ctx context.Context, req map[string]any) (any, error) {
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
func (a *Adapter) handleLoopbackTaskGet(ctx context.Context, req map[string]any) (any, error) {
	if requested := reqStr(req, "id"); requested != "" && requested != a.loopbackTaskID {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("loopback task_get is pinned to %s; cannot read task %s", a.loopbackTaskID, requested), "id")
	}
	task, err := a.svc.Task.Get(a.loopbackTaskID)
	if err != nil {
		return errFromService(err)
	}
	// Same comment tail as the cross-task tool (CW-20260910-0057). This path
	// is the load-bearing one: the boot prompt already tells workers to read
	// checkpoint responses through torque_task_get, so it is the call a
	// dispatched worker actually makes, and the one where a stale
	// description does the most damage.
	return a.taskGetResult(
		task,
		reqStrBoolDefault(req, "comments", true),
		clampLimit(reqInt(req, "comments_limit"), defaultTaskGetCommentsLimit, maxTaskGetCommentsLimit),
	)
}

// handleLoopbackCheckpointEmit emits a typed HITL checkpoint on the worker's
// own task. task_id is hardcoded to the loopback's bound task; the worker
// cannot emit checkpoints on other tasks. EmitterSourceType is forced to
// "agent" so dashboards see the right provenance without the worker having
// to set it correctly (the cross-task tool defaulted to "system" when
// omitted, which would mislabel worker-emitted checkpoints).
func (a *Adapter) handleLoopbackCheckpointEmit(ctx context.Context, req map[string]any) (any, error) {
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
func (a *Adapter) handleLoopbackCheckpointRespond(ctx context.Context, req map[string]any) (any, error) {
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
	if err := a.svc.Checkpoint.Respond(ctx, service.CheckpointRespondInput{
		CorrelationID:       corr,
		ResponseJSON:        reqStr(req, "response_json"),
		ResponderSourceType: "agent",
	}); err != nil {
		return errFromService(err)
	}
	return a.checkpointResultByCorr(corr)
}

// handleLoopbackSteeringDismiss acks injected steering envelopes against
// the loopback's bound task (CW-20260519-0065). The handler returns
// {dismissed, unknown} so the agent gets a precise account of what
// landed vs. what was already gone — an unknown id is not an error
// (stale id from a prior process, already dismissed, etc.).
//
// Returns a "feature disabled" error when no ReminderRegistry was
// attached to this adapter — the dismiss only makes sense paired with a
// runtime that tracks injections. Mirrors the nil-broker contract.
func (a *Adapter) handleLoopbackSteeringDismiss(ctx context.Context, req map[string]any) (any, error) {
	if a.reminderRegistry == nil {
		return errResult(ErrCodeDomain, "steering reminder registry not configured on this MCP host", "")
	}
	envelopeIDs, err := reqStrSlice(req, "envelope_ids")
	if err != nil {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid envelope_ids: %v", err), "envelope_ids")
	}
	if len(envelopeIDs) == 0 {
		return errResult(ErrCodeArgInvalid, "envelope_ids is required and must not be empty", "envelope_ids")
	}
	dismissed := a.reminderRegistry.Dismiss(a.loopbackTaskID, envelopeIDs...)

	// Compute the unknown set as input \ dismissed (preserving caller
	// order). The agent uses this to distinguish "I acked successfully"
	// from "the substrate had no record (already dismissed or stale)".
	known := make(map[string]bool, len(dismissed))
	for _, id := range dismissed {
		known[id] = true
	}
	unknown := make([]string, 0, len(envelopeIDs))
	for _, id := range envelopeIDs {
		if !known[id] {
			unknown = append(unknown, id)
		}
	}

	return okResult(map[string]interface{}{
		"dismissed": dismissed,
		"unknown":   unknown,
	})
}
