package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/hollis-labs/torque/internal/hitl"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// ErrNoLiveSessionForTask is the sentinel a CheckpointResponseDispatcher
// returns when it can't find an existing session to resume for the parked
// task. Respond treats this as "fall through to the legacy fresh-boot path":
// transition the parked task review → todo so the scheduler picks it up on
// the next tick. Any other dispatcher error is logged + the task still goes
// review → todo (no-op fallback) so an operator-side observability problem
// can't strand a task in review.
var ErrNoLiveSessionForTask = errors.New("checkpoint dispatcher: no resumable session bound to task")

// CheckpointResponseDispatcher is the narrow surface CheckpointService.Respond
// calls when a parked task with OnCheckpointResponse="resume" receives a
// response. Production wiring (bootstrap.Reactor) supplies an adapter that
// looks up the task's most recent session and drives
// agent.Manager.ResumeSession + agent.Manager.SendInput so the operator
// answer lands as a USER turn on the resumed transcript (sprint α.4, D3
// reframe of CW-20260510-0122).
//
// The dispatcher OWNS the task's lifecycle transition when it takes
// ownership: on success Respond transitions review → doing (because the
// resumed session is the new live worker for this task, mirroring the
// scheduler's todo→doing dispatch). On ErrNoLiveSessionForTask Respond
// falls back to review → todo so the scheduler's fresh-boot path picks up
// the task — the same path the pre-α.4 redispatch used. Per sprint-α D4
// the dispatcher does NOT inspect provider capability itself; it always
// calls ResumeSession which encapsulates the SupportsResume branch
// internally (resume for claude/codex, fresh-boot for gemini/copilot/opencode).
//
// nil dispatcher (default; legacy callers): Respond keeps the pre-α.4 path
// — review → todo for "resume" mode, leaving redispatch to the scheduler.
type CheckpointResponseDispatcher interface {
	DispatchResponse(ctx context.Context, in CheckpointResponseDispatch) error
}

// CheckpointResponseDispatch is the payload CheckpointService.Respond hands
// to the dispatcher. Everything the resume path needs is here: the parked
// task ID, the original checkpoint correlation, and the operator's response
// JSON to deliver as a user-turn input on the resumed session.
type CheckpointResponseDispatch struct {
	TaskID        string
	CorrelationID string
	ResponseJSON  string
}

// OrchestratorRedispatcher is the hook CheckpointService.Respond calls after
// a checkpoint is responded so a paused/exited Orchestrator session walking
// the task's parent plan gets redispatched (CW-20260518 orchestrator
// checkpoint-redispatch fix).
//
// Why this is a SEPARATE hook from CheckpointResponseDispatcher: the α.4
// CheckpointResponseDispatcher resumes the *checkpoint task's own* session.
// That is the right behavior when the checkpoint task is the live worker
// (an executor parked mid-run). But a `pr_review` checkpoint emitted on a
// CHILD task by the reviewer end-agent is a different shape entirely — the
// child's worker is long gone, and the session that must wake up is the
// Orchestrator running on the *parent plan*, a different task with a
// different session. The α.4 dispatcher cannot see that plan; this hook
// owns the task → plan-ancestor walk and the orchestrator redispatch.
//
// RedispatchForCheckpointResponse is called for EVERY responded checkpoint
// (not gated on the checkpoint task being parked) because a child task that
// is already in `review` when its checkpoint is emitted is never re-parked
// by Emit's ParkTaskOnCheckpoint (that UPDATE only fires on status=doing).
// The implementation is responsible for being a cheap no-op when the task
// has no plan ancestor with an orchestrator session.
//
// nil redispatcher (default; legacy/test callers): Respond skips the
// orchestrator-redispatch step entirely — pre-fix behavior.
type OrchestratorRedispatcher interface {
	RedispatchForCheckpointResponse(ctx context.Context, in OrchestratorRedispatch) error
}

// OrchestratorRedispatch is the payload handed to an OrchestratorRedispatcher.
// TaskID is the checkpoint's task (the child, in the bug case); the
// implementation walks up to the kind=plan ancestor itself.
type OrchestratorRedispatch struct {
	TaskID        string
	CorrelationID string
	ResponseJSON  string
}

// CheckpointService owns the emit/respond/cancel/list flows for checkpoints.
// Scheduler and lifecycle wiring (parking on blocking emit, resume on respond)
// live in their own packages and consume CheckpointService.
//
// responseDispatcher (sprint α.4): optional; when wired the Respond path
// drives an in-process resume+send_input dispatch instead of the legacy
// review→todo handoff to the scheduler. See CheckpointResponseDispatcher.
type CheckpointService struct {
	store                    *sqlstore.Store
	responseDispatcher       CheckpointResponseDispatcher
	orchestratorRedispatcher OrchestratorRedispatcher
}

// WithResponseDispatcher wires a CheckpointResponseDispatcher into the
// service. Returns s so the bootstrap composition root can chain. Calling
// with nil clears the dispatcher (and reverts to the legacy behavior).
// Not goroutine-safe with concurrent Respond calls; wire once at startup.
func (s *CheckpointService) WithResponseDispatcher(d CheckpointResponseDispatcher) *CheckpointService {
	s.responseDispatcher = d
	return s
}

// WithOrchestratorRedispatcher wires an OrchestratorRedispatcher into the
// service. Returns s so the bootstrap composition root can chain. Calling
// with nil clears the hook. Not goroutine-safe with concurrent Respond
// calls; wire once at startup.
func (s *CheckpointService) WithOrchestratorRedispatcher(d OrchestratorRedispatcher) *CheckpointService {
	s.orchestratorRedispatcher = d
	return s
}

// CheckpointEmitInput is the service-level input for recording a new
// checkpoint. RunID is optional (nullable on the row); TimeoutAt is optional.
type CheckpointEmitInput struct {
	TaskID            string
	RunID             *int64
	Type              string
	PayloadJSON       string
	EmitterSourceType string
	EmitterSourceRef  string
	TimeoutAt         *time.Time
}

// CheckpointEmitOutput returns the generated correlation_id + row id so the
// caller (scheduler, MCP tool) can reference the checkpoint.
type CheckpointEmitOutput struct {
	CorrelationID string
	ID            int64
	Status        string
}

// CheckpointRespondInput records a response + responder identity for a
// pending checkpoint.
type CheckpointRespondInput struct {
	CorrelationID       string
	ResponseJSON        string
	ResponderSourceType string
	ResponderSourceRef  string
}

// CheckpointCancelInput cancels a pending checkpoint with an attached reason.
// Reuses the responder columns to record the canceler.
type CheckpointCancelInput struct {
	CorrelationID      string
	Reason             string
	CancelerSourceType string
	CancelerSourceRef  string
}

// Emit creates a pending checkpoint. Generates a ULID correlation_id, applies
// source defaults, persists the row, and — when the task is actively running
// (status=doing) and its checkpoint_mode is "blocking" — parks it in review
// with BlockedReason="awaiting checkpoint <corr>". This is the sole code path
// for checkpoint emission (the legacy in-run TORQUE_CHECKPOINT stdout
// signal protocol was retired in Phase E / CW-20260427-0043; spec §4.2).
//
// The park is conditional at the SQL level via store.ParkTaskOnCheckpoint:
// a single UPDATE with "WHERE status='doing' AND checkpoint_mode='blocking'",
// treating 0 rows affected as a no-op. This closes the TOCTOU window that a
// Go-level status snapshot would leave open — a concurrent transition
// (another Emit on this task, scheduler tick, manual review flip) can't get
// clobbered by a stale read.
//
// Non-blocking tasks, already-parked tasks, and tasks that haven't started
// (todo / any terminal state) are left alone — Emit records the checkpoint
// row but doesn't manufacture a transition. Idempotency: if the task is
// already in review on a prior correlation, BlockedReason is preserved so
// respond/cancel still match the original correlation_id.
func (s *CheckpointService) Emit(in CheckpointEmitInput) (*CheckpointEmitOutput, error) {
	if in.TaskID == "" {
		return nil, &ValidationError{Field: "task_id", Message: "task_id required"}
	}
	if in.Type == "" {
		return nil, &ValidationError{Field: "type", Message: "type required"}
	}
	if in.EmitterSourceType == "" {
		in.EmitterSourceType = "system"
	}
	if !validSourceTypes[in.EmitterSourceType] {
		return nil, &ValidationError{Field: "emitter_source_type", Message: "invalid emitter_source_type"}
	}

	// Task-existence validation. A missing task here becomes a 422 instead
	// of a store-level FK violation a few lines later.
	if _, err := s.store.GetTask(in.TaskID); err != nil {
		if errors.Is(err, sqlstore.ErrTaskNotFound) {
			return nil, &ValidationError{Field: "task_id", Message: "task not found"}
		}
		return nil, err
	}

	corr := ulid.Make().String()

	cp := &sqlstore.CheckpointRecord{
		TaskID:            in.TaskID,
		CorrelationID:     corr,
		Type:              in.Type,
		PayloadJSON:       in.PayloadJSON,
		EmitterSourceType: in.EmitterSourceType,
		Status:            "pending",
	}
	if in.RunID != nil {
		cp.RunID.Int64 = *in.RunID
		cp.RunID.Valid = true
	}
	if in.EmitterSourceRef != "" {
		cp.EmitterSourceRef.String = in.EmitterSourceRef
		cp.EmitterSourceRef.Valid = true
	}
	if in.TimeoutAt != nil {
		cp.TimeoutAt.Time = *in.TimeoutAt
		cp.TimeoutAt.Valid = true
	}
	if err := s.store.CreateCheckpoint(cp); err != nil {
		return nil, fmt.Errorf("create checkpoint: %w", err)
	}

	// Atomic park: no-op unless the task is still eligible when the UPDATE
	// actually runs. parked=false is fine here — the checkpoint row is
	// recorded either way, and callers who care can inspect task state
	// after the fact.
	reason := "awaiting checkpoint " + corr
	if _, err := s.store.ParkTaskOnCheckpoint(in.TaskID, reason); err != nil {
		return nil, fmt.Errorf("park task on checkpoint: %w", err)
	}

	return &CheckpointEmitOutput{CorrelationID: corr, ID: cp.ID, Status: cp.Status}, nil
}

// Respond writes a response to a pending checkpoint. Returns ConflictError
// if the checkpoint is already terminal (responded/canceled/timed_out).
// Downstream task resume (on_checkpoint_response) is the lifecycle
// manager's responsibility (B7).
func (s *CheckpointService) Respond(in CheckpointRespondInput) error {
	if in.CorrelationID == "" {
		return &ValidationError{Field: "correlation_id", Message: "correlation_id required"}
	}
	if in.ResponderSourceType == "" {
		return &ValidationError{Field: "responder_source_type", Message: "responder_source_type required"}
	}
	if !validSourceTypes[in.ResponderSourceType] {
		return &ValidationError{Field: "responder_source_type", Message: "invalid responder_source_type"}
	}
	cp, err := s.store.GetCheckpointByCorrelation(in.CorrelationID)
	if err != nil {
		return err
	}
	if cp.Status != "pending" {
		return &ConflictError{Message: "checkpoint " + cp.Status}
	}
	task, err := s.store.GetTask(cp.TaskID)
	if err != nil {
		return err
	}
	if err := validateRequiredWorkflowResponse(task, cp, in.ResponderSourceType); err != nil {
		return err
	}
	if err := s.store.RespondCheckpoint(
		in.CorrelationID, in.ResponseJSON,
		in.ResponderSourceType, in.ResponderSourceRef,
		time.Now().UTC(),
	); err != nil {
		return err
	}
	return s.applyOnCheckpointResponse(context.Background(), cp, in.ResponseJSON)
}

func validateRequiredWorkflowResponse(task *sqlstore.TaskRecord, cp *sqlstore.CheckpointRecord, responderSourceType string) error {
	policy, ok, err := requiredWorkflowPolicyForTask(task)
	if err != nil {
		return err
	}
	if !ok || policy.EnforcementMode != hitl.EnforcementRequired {
		return nil
	}

	parked := task.Status == "review" && strings.Contains(task.BlockedReason, cp.CorrelationID)
	if cp.Type != policy.WorkflowType && !parked {
		return nil
	}

	eval := hitl.CheckpointSatisfiesRequiredWorkflow(policy, hitl.CheckpointState{
		Type:                cp.Type,
		Status:              "responded",
		ResponderSourceType: responderSourceType,
	})
	if eval.Satisfied {
		return nil
	}
	msg := strings.Join(eval.Reasons, "; ")
	if msg == "" {
		msg = "checkpoint response does not satisfy required workflow"
	}
	return &ValidationError{Field: "required_workflow", Message: msg}
}

func requiredWorkflowPolicyForTask(task *sqlstore.TaskRecord) (hitl.RequiredWorkflowPolicy, bool, error) {
	if !task.Metadata.Valid || task.Metadata.String == "" {
		return hitl.RequiredWorkflowPolicy{}, false, nil
	}
	md := map[string]any{}
	if err := unmarshalJSON([]byte(task.Metadata.String), &md); err != nil {
		return hitl.RequiredWorkflowPolicy{}, false, &ValidationError{
			Field:   "metadata",
			Message: "invalid task metadata: " + err.Error(),
		}
	}
	policy, ok, err := hitl.ParseRequiredWorkflowFromMetadata(md)
	if err != nil {
		return hitl.RequiredWorkflowPolicy{}, false, &ValidationError{
			Field:   "metadata.hitl.required_workflow",
			Message: err.Error(),
		}
	}
	return policy, ok, nil
}

// applyOnCheckpointResponse enforces the task's on_checkpoint_response rule
// after a successful Respond. If the task is parked on this correlation_id
// (status=review AND blocked_reason mentions it):
//   - resume: sprint α.4 — if a CheckpointResponseDispatcher is wired, hand
//     off (taskID, corr, responseJSON) to it; on success the dispatcher has
//     called agent.Manager.ResumeSession + agent.Manager.SendInput, the
//     resumed session owns the work, and the task transitions review →
//     doing (mirroring the scheduler's todo→doing dispatch). On
//     ErrNoLiveSessionForTask (or any other dispatcher error) fall back to
//     the legacy path: transition review → todo and let the scheduler
//     fresh-boot on the next tick. If no dispatcher is wired (legacy
//     callers / test harnesses), the legacy review → todo path runs
//     unconditionally.
//   - review: stay in review, keep BlockedReason, attach response
//   - custom: plugin hook hand-off (MVP: attach response + stay)
//
// If the task isn't parked on this correlation (e.g. non_blocking mode, or a
// responder racing the executor), only the metadata is attached.
//
// Orchestrator redispatch (CW-20260518): independent of the parked check
// and of on_checkpoint_response, every responded checkpoint is offered to
// the OrchestratorRedispatcher. This is the fix for the live bug where a
// `pr_review` checkpoint emitted on a CHILD task was responded but the
// Orchestrator paused on the parent PLAN never woke up. The child is never
// re-parked (Emit's ParkTaskOnCheckpoint only fires on status=doing, and
// the child is already in `review` when its checkpoint is emitted), so the
// parked-gated dispatch path above can't be relied on to redispatch the
// orchestrator. The redispatcher walks task → plan ancestor itself and is
// a cheap no-op when there is no orchestrated plan above the task.
func (s *CheckpointService) applyOnCheckpointResponse(ctx context.Context, cp *sqlstore.CheckpointRecord, responseJSON string) error {
	task, err := s.store.GetTask(cp.TaskID)
	if err != nil {
		return err
	}
	if err := s.attachCheckpointResponseToMetadata(task, cp.CorrelationID, responseJSON); err != nil {
		return err
	}

	parked := task.Status == "review" && strings.Contains(task.BlockedReason, cp.CorrelationID)
	if parked {
		switch task.OnCheckpointResponse {
		case "resume":
			if err := s.dispatchResumeOrFallback(ctx, cp, responseJSON); err != nil {
				return err
			}
		case "review", "custom":
			// Stay parked. Human (or plugin hook) resolves.
		}
	}

	// Orchestrator redispatch runs whether or not the checkpoint task was
	// parked: a checkpoint emitted on a child task that walked a plan needs
	// to wake the Orchestrator regardless of the child's own lifecycle.
	s.redispatchOrchestrator(ctx, cp, responseJSON)
	return nil
}

// redispatchOrchestrator offers the responded checkpoint to the wired
// OrchestratorRedispatcher. Errors are logged, never returned — a redispatch
// failure must not fail the operator's Respond call (the checkpoint row is
// already responded and the response is already attached to task metadata;
// a stuck orchestrator is recoverable by a manual plan re-start, a failed
// Respond is a worse operator experience). A nil redispatcher (legacy/test
// composition roots) makes this a no-op.
func (s *CheckpointService) redispatchOrchestrator(ctx context.Context, cp *sqlstore.CheckpointRecord, responseJSON string) {
	if s.orchestratorRedispatcher == nil {
		return
	}
	dispatchCtx, cancel := context.WithTimeout(ctx, checkpointDispatchTimeout)
	defer cancel()
	err := s.orchestratorRedispatcher.RedispatchForCheckpointResponse(dispatchCtx, OrchestratorRedispatch{
		TaskID:        cp.TaskID,
		CorrelationID: cp.CorrelationID,
		ResponseJSON:  responseJSON,
	})
	if err != nil {
		log.Printf("[checkpoint] orchestrator redispatch for task=%s corr=%s failed: %v",
			cp.TaskID, cp.CorrelationID, err)
	}
}

// checkpointDispatchTimeout bounds the resume+send_input dispatch path so a
// hung ResumeSession/SendInput can't keep the HTTP/MCP respond handler open
// indefinitely. On deadline the catch-all error branch below falls back to
// the legacy todo redispatch so the task progresses on the scheduler's next
// tick. 30s covers a Boot+Start on a cold adapter with margin; well-behaved
// resumes return in <2s.
const checkpointDispatchTimeout = 30 * time.Second

// dispatchResumeOrFallback is the sprint α.4 fork point: with a dispatcher
// wired, hand off to the in-process resume+send_input path; without one
// (or on ErrNoLiveSessionForTask), run the pre-α.4 legacy transition
// review → todo and let the scheduler's next tick fresh-boot the task.
//
// Dispatcher errors other than ErrNoLiveSessionForTask are also treated as
// "fall back to the legacy path" — a transient inject failure on the
// resumed-session side must not strand the task in review. The error is
// logged so postmortem queries see the path mismatch.
func (s *CheckpointService) dispatchResumeOrFallback(ctx context.Context, cp *sqlstore.CheckpointRecord, responseJSON string) error {
	if s.responseDispatcher == nil {
		// Legacy: hand off to the scheduler via the todo queue.
		return s.store.TransitionTaskWithReason(cp.TaskID, "todo", "")
	}
	dispatchCtx, cancel := context.WithTimeout(ctx, checkpointDispatchTimeout)
	defer cancel()
	err := s.responseDispatcher.DispatchResponse(dispatchCtx, CheckpointResponseDispatch{
		TaskID:        cp.TaskID,
		CorrelationID: cp.CorrelationID,
		ResponseJSON:  responseJSON,
	})
	if err == nil {
		// Dispatcher owns the resumed session; the task is now in flight
		// again on an in-process worker. Mirror the scheduler's
		// todo→doing transition so picker.go won't redispatch and the
		// task's blocked_reason clears.
		return s.store.TransitionTaskWithReason(cp.TaskID, "doing", "")
	}
	if errors.Is(err, ErrNoLiveSessionForTask) {
		// No session row to resume — fall through to the legacy fresh-boot
		// path via the scheduler. This is the expected branch for tasks
		// whose worker never registered with the long-lived session
		// manager (one-shot executor tasks, recovered-from-crash flows).
		return s.store.TransitionTaskWithReason(cp.TaskID, "todo", "")
	}
	// Any other error: log and fall back to the legacy path so the task
	// progresses. The dispatcher will have observability of its own
	// failures; Respond's job is to not strand the task.
	log.Printf("[checkpoint] respond dispatcher error for task=%s corr=%s: %v — falling back to legacy todo redispatch",
		cp.TaskID, cp.CorrelationID, err)
	return s.store.TransitionTaskWithReason(cp.TaskID, "todo", "")
}

// attachCheckpointResponseToMetadata writes the response into
// metadata.checkpoint_responses[correlation_id]. If the response string is
// valid JSON it's stored structurally; otherwise the raw string is stored so
// downstream tooling still sees it.
func (s *CheckpointService) attachCheckpointResponseToMetadata(
	task *sqlstore.TaskRecord, correlationID, responseJSON string,
) error {
	md := map[string]any{}
	if task.Metadata.Valid && task.Metadata.String != "" {
		_ = unmarshalJSON([]byte(task.Metadata.String), &md)
	}
	responses, _ := md["checkpoint_responses"].(map[string]any)
	if responses == nil {
		responses = map[string]any{}
	}
	var parsed any
	if err := unmarshalJSON([]byte(responseJSON), &parsed); err != nil {
		parsed = responseJSON
	}
	responses[correlationID] = parsed
	md["checkpoint_responses"] = responses

	newMD := marshalJSON(md)
	return s.store.UpdateTask(task.ID, sqlstore.TaskUpdate{
		Metadata: &sql.NullString{String: newMD, Valid: true},
	})
}

// Cancel flips a pending checkpoint to canceled, recording the canceler as
// the responder. Returns ConflictError if the checkpoint is already
// terminal.
//
// CancelerSourceType defaults to "system" when empty (matching Emit's
// behaviour) and is validated against validSourceTypes so provenance is
// consistent across emit/respond/cancel.
//
// Per spec §4.5, if the task is parked on this correlation (status=review
// with BlockedReason mentioning the correlation_id), Cancel also updates
// the task's BlockedReason to "checkpoint <corr> canceled: <reason>" so
// the human driving the follow-up transition sees why it was canceled.
func (s *CheckpointService) Cancel(in CheckpointCancelInput) error {
	if in.CorrelationID == "" {
		return &ValidationError{Field: "correlation_id", Message: "correlation_id required"}
	}
	if in.CancelerSourceType == "" {
		in.CancelerSourceType = "system"
	}
	if !validSourceTypes[in.CancelerSourceType] {
		return &ValidationError{
			Field:   "canceler_source_type",
			Message: "invalid canceler_source_type",
		}
	}
	cp, err := s.store.GetCheckpointByCorrelation(in.CorrelationID)
	if err != nil {
		return err
	}
	if cp.Status != "pending" {
		return &ConflictError{Message: "checkpoint " + cp.Status}
	}
	if err := s.store.CancelCheckpoint(
		in.CorrelationID, in.Reason,
		in.CancelerSourceType, in.CancelerSourceRef,
		time.Now().UTC(),
	); err != nil {
		return err
	}
	return s.applyCancelToParkedTask(cp, in.Reason)
}

// applyCancelToParkedTask updates the parked task's BlockedReason to reflect
// the cancellation per spec §4.5. If the task is no longer parked on this
// correlation (already moved on, or non_blocking mode), this is a noop.
func (s *CheckpointService) applyCancelToParkedTask(cp *sqlstore.CheckpointRecord, reason string) error {
	task, err := s.store.GetTask(cp.TaskID)
	if err != nil {
		return err
	}
	parked := task.Status == "review" && strings.Contains(task.BlockedReason, cp.CorrelationID)
	if !parked {
		return nil
	}
	blockedReason := "checkpoint " + cp.CorrelationID + " canceled"
	if reason != "" {
		blockedReason += ": " + reason
	}
	return s.store.TransitionTaskWithReason(cp.TaskID, "review", blockedReason)
}

// Get returns a checkpoint by correlation_id.
func (s *CheckpointService) Get(correlationID string) (*sqlstore.CheckpointRecord, error) {
	return s.store.GetCheckpointByCorrelation(correlationID)
}

// ListForTask returns all checkpoints emitted against a task.
func (s *CheckpointService) ListForTask(taskID string) ([]sqlstore.CheckpointRecord, error) {
	return s.store.ListCheckpointsForTask(taskID)
}

// ListPending returns every pending checkpoint across all tasks, oldest
// first. Used by the scheduler's timeout sweeper and by the MCP
// "pending" tool.
func (s *CheckpointService) ListPending() ([]sqlstore.CheckpointRecord, error) {
	return s.store.ListPendingCheckpoints()
}

// SweepTimedOut moves pending checkpoints past their timeout_at to
// "timed_out" status and returns the number affected.
func (s *CheckpointService) SweepTimedOut(now time.Time) (int, error) {
	return s.store.SweepTimedOutCheckpoints(now)
}
