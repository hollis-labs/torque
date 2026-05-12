package bootstrap

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/agent"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

// CheckpointResponseDispatcher (sprint α.4, CW-20260512-0062) implements
// service.CheckpointResponseDispatcher by driving the in-process
// agent.Manager.ResumeSession + agent.Manager.SendInput pair so an
// operator's checkpoint response lands as a user-turn input on the resumed
// session's transcript. Reframes CW-20260510-0122's "fresh-boot with the
// response appended to the boot prompt" path: per sprint-α D3, the operator
// answer is delivered to the live agent via send_input on a resumed
// session, NOT a redispatched boot prompt.
//
// Capability transparency. The caller code is uniform across providers
// because agent.Manager.ResumeSession encapsulates the SupportsResume
// branch internally (claude/codex thread `--resume <session-id>`, the
// fresh-boot providers gemini/copilot/opencode rebuild from
// AgentProfile/Workdir/TaskID without `--resume`). Per sprint-α D4 the
// dispatcher does NOT runtime-probe — the declared capability is the
// single decision point.
//
// Resume path:
//
//  1. Lookup the most-recent sessions row for taskID (ListSessions newest-
//     first, limit 1). Empty result → ErrNoLiveSessionForTask so
//     CheckpointService.Respond falls back to its legacy review → todo
//     path and the scheduler fresh-boots on the next tick. This is the
//     correct branch for one-shot executor tasks that never carried a
//     long-lived session row.
//  2. ResumeSession(ctx, prev.ID, ResumeOptions{}). For SupportsResume=true
//     the persisted resume_hint is threaded into the adapter argv; for
//     SupportsResume=false ResumeSession does fresh-boot — same surface,
//     different internal branch.
//  3. SendInput(newSess.ID, responseJSON). The bytes flow through the lib's
//     PTY/adapter runtime as a user turn. The agent's transcript shows
//     the operator answer the same way an interactive operator-typed turn
//     would appear — that's why this shape supersedes the
//     boot-prompt-prepended-response approach.
//  4. Emit a run_events breadcrumb so postmortem queries can correlate the
//     respond → resume → send_input chain (originating session, new
//     session, used_resume capability, correlation_id, path="alpha4").
//
// Failure modes. ResumeSession or SendInput errors are returned to the
// service layer, which logs and falls back to the legacy todo redispatch
// so the task progresses. A breadcrumb still fires (with the error
// recorded) so the partial-failure path is visible in the audit log.
type CheckpointResponseDispatcher struct {
	store    *sqlstore.Store
	sessions *agent.Manager
}

// NewCheckpointResponseDispatcher constructs a dispatcher bound to the
// daemon's store + agent.Manager. Both must be non-nil for the dispatcher
// to be wired; bootstrap.Reactor skips wiring (and the service falls back
// to its legacy path) when either is nil.
func NewCheckpointResponseDispatcher(store *sqlstore.Store, sessions *agent.Manager) *CheckpointResponseDispatcher {
	return &CheckpointResponseDispatcher{store: store, sessions: sessions}
}

// DispatchResponse implements service.CheckpointResponseDispatcher. See the
// type doc for the full flow and failure-mode contract.
func (d *CheckpointResponseDispatcher) DispatchResponse(ctx context.Context, in service.CheckpointResponseDispatch) error {
	if d == nil || d.store == nil || d.sessions == nil {
		return service.ErrNoLiveSessionForTask
	}
	prev, err := d.latestSessionForTask(in.TaskID)
	if err != nil {
		return err
	}
	if prev == nil {
		return service.ErrNoLiveSessionForTask
	}

	newSess, err := d.sessions.ResumeSession(ctx, prev.ID, agent.ResumeOptions{})
	if err != nil {
		d.recordBreadcrumb(in, prev, nil, fmt.Errorf("resume_session: %w", err))
		return fmt.Errorf("α.4 HITL resume: %w", err)
	}

	// Operator response is a user-turn input. send_input expects a complete
	// turn payload — the lib's PTY/adapter runtime handles newline framing.
	// The response_json blob is passed through as-is so the agent sees the
	// exact JSON shape the operator submitted (the prompt.go
	// checkpointRedispatchPrompt instructs agents to read
	// task.metadata.checkpoint_responses[corr] for structured access; the
	// raw user turn is a redundant visibility surface so the answer is
	// also in the transcript).
	if err := d.sessions.SendInput(newSess.ID, []byte(in.ResponseJSON)); err != nil {
		d.recordBreadcrumb(in, prev, newSess, fmt.Errorf("send_input: %w", err))
		return fmt.Errorf("α.4 HITL send_input: %w", err)
	}
	d.recordBreadcrumb(in, prev, newSess, nil)
	return nil
}

// latestSessionForTask returns the most-recent sessions row bound to
// taskID, or (nil, nil) when no row exists. The session may be terminal
// (cancel_on_transition stops the worker when emit parks the task); that's
// fine — ResumeSession reads the persisted resume_hint + AgentProfile +
// Workdir + TaskID off the row regardless of state, and the provider's
// `--resume` flag is the bridge to the prior transcript context.
func (d *CheckpointResponseDispatcher) latestSessionForTask(taskID string) (*sqlstore.SessionRecord, error) {
	recs, err := d.store.ListSessions(sqlstore.SessionFilter{TaskID: taskID, Limit: 1})
	if err != nil {
		return nil, fmt.Errorf("list sessions for task %s: %w", taskID, err)
	}
	if len(recs) == 0 {
		return nil, nil
	}
	return recs[0], nil
}

// breadcrumbPayload is the JSON shape AppendRunEvent stores for sprint-α.4
// dispatches. Postmortem queries grep by type="checkpoint.response_dispatched"
// + payload.path="alpha4" to find the chain after the rewire. usedResume is
// derived from the provider capability (not a runtime probe) so the meta
// reflects the declared D4 decision, not after-the-fact behavior.
type breadcrumbPayload struct {
	Path                string `json:"path"`
	CorrelationID       string `json:"correlation_id"`
	OriginalSessionID   string `json:"original_session_id"`
	NewSessionID        string `json:"new_session_id,omitempty"`
	Provider            string `json:"provider"`
	UsedResume          bool   `json:"used_resume"`
	OperatorResponseLen int    `json:"operator_response_len"`
	Error               string `json:"error,omitempty"`
}

// recordBreadcrumb appends a single run_events row tagged
// "checkpoint.response_dispatched" so the respond → resume → send_input
// chain is queryable from the audit log. Failure to append is logged but
// NOT returned — observability must never block the dispatch.
func (d *CheckpointResponseDispatcher) recordBreadcrumb(
	in service.CheckpointResponseDispatch,
	prev *sqlstore.SessionRecord,
	newSess *agent.Session,
	dispatchErr error,
) {
	if d == nil || d.store == nil {
		return
	}
	payload := breadcrumbPayload{
		Path:                "alpha4",
		CorrelationID:       in.CorrelationID,
		OperatorResponseLen: len(in.ResponseJSON),
	}
	if prev != nil {
		payload.OriginalSessionID = prev.ID
		payload.Provider = prev.Provider
		payload.UsedResume = agent.ProviderCapabilities(prev.Provider).SupportsResume
	}
	if newSess != nil {
		payload.NewSessionID = newSess.ID
	}
	if dispatchErr != nil {
		payload.Error = dispatchErr.Error()
	}
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[checkpoint-dispatch] marshal breadcrumb for task=%s: %v", in.TaskID, err)
		return
	}
	evt := &sqlstore.RunEventRecord{
		TaskID:  in.TaskID,
		Type:    "checkpoint.response_dispatched",
		Payload: string(body),
	}
	// run_id is optional on the row; we don't have a meaningful one here
	// because the resumed session is a NEW session not yet attached to a
	// scheduler run. Leave RunID zero-value (NullInt64{Valid:false}).
	_ = sql.NullInt64{}
	if _, err := d.store.AppendRunEvent(evt); err != nil {
		log.Printf("[checkpoint-dispatch] append breadcrumb for task=%s: %v", in.TaskID, err)
	}
}
