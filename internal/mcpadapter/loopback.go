package mcpadapter

import (
	"context"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewLoopback returns an Adapter pre-bound to taskID with only the
// interpretive tool subset registered. Designed for the per-task ephemeral
// MCP loopback used by the executor wrapper (CW-20260427-0016): the agent
// connects via stdio pipes paired to the spawning subprocess, and every tool
// call implicitly targets the task being executed — agents don't pass IDs
// and can't act on other tasks.
//
// Tool catalog (loopback subset):
//   - torque_task_summary(text)        — agent's prose summary at done
//   - torque_task_blocked(reason)      — flag blocked + set reason
//   - torque_task_review(reason)       — flag review + comment
//   - torque_artifact_create           — task_id implicit
//   - torque_comment_add               — task_id + author implicit
//   - torque_task_subtodo_add          — task_id implicit
//   - torque_task_subtodo_done         — task_id implicit
//
// Deterministic lifecycle (started, exited, timeout) is NOT in this catalog
// — the executor wrapper drives those off go-runner events.
// torque_task_transition is also excluded; agents do not drive their own
// FSM. Discovery / read tools (list, get, search) are excluded — the agent
// already has its task body in the prompt.
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
