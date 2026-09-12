package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/hollis-labs/torque/internal/runtime/steering"
)

// statusPollInterval is the cadence at which runLongLived re-reads the
// worker's task.status to detect a self-transition out of "doing". 5s is
// the operator-tunable knob; faster polling buys nothing on minute-scale
// liveness budgets and would just stress the SQLite WAL. Exposed as a var
// (not const) so the existing test path can shrink it for fast-test runs.
var statusPollInterval = 5 * time.Second

// workerVerifyTimeout bounds the engine-side completion-verification git
// invocation so scheduler shutdown / per-task cancel can tear the work
// down. Generous (30s) because a healthy `git rev-list --count` is
// sub-second but a worktree on a slow filesystem or with index-lock
// contention can stretch to seconds; we want to absorb that without
// punishing every healthy run with a tighter bound.
const workerVerifyTimeout = 30 * time.Second

// runLongLived dispatches a kind=agent worker as a long-lived agent.Boot
// session, then drives the worker's completion via three signals: an
// explicit self-transition out of "doing", an in-memory inactivity timer
// that resets on every stream event, and an absolute hard ceiling.
//
// CW-20260519-0095. The ModeOneShot default historically truncated multi-
// turn kind=agent tickets at end_of_turn; this path replaces it.
//
// Completion contract (in order of precedence):
//
//  1. Worker self-transitions task out of "doing" via the loopback
//     (torque_task_review / torque_task_blocked / torque_task_summary +
//     wrapper). The new task.Status drives the run result:
//     review/done → ExecutionResult.Status=done; blocked → blocked; other
//     terminal → failed.
//
//  2. No executor event arrives within the inactivity threshold (default
//     30m, override via task metadata inactivity_threshold_seconds). The
//     run completes as blocked with a "worker idle past <threshold>" reason
//     so the lifecycle manager parks the task for human follow-up rather
//     than discarding the work.
//
//  3. Wall-clock hard ceiling elapses (default 12h, override hard_ceiling_
//     seconds). The run completes as failed; this is the "forgotten worker"
//     safety net, not a budget.
//
//  4. Caller ctx cancellation (scheduler shutdown / external transition):
//     the run completes as failed with reason="execution canceled".
//
// In all four cases the live session is stopped via Manager.Stop before
// runLongLived returns, so the next dispatch sees a clean slot.
func (e *Executor) runLongLived(ctx context.Context, profile config.AgentProfile, opts Options, cb executor.EventCallback) (*executor.ExecutionResult, error) {
	if opts.TaskID == "" {
		return &executor.ExecutionResult{Status: "failed", Reason: "long-lived dispatch requires opts.TaskID"}, nil
	}
	if e.deps == nil || e.deps.Store == nil {
		return &executor.ExecutionResult{Status: "failed", Reason: "long-lived dispatch requires deps.Store"}, nil
	}
	if e.deps.Sessions == nil {
		return &executor.ExecutionResult{Status: "failed", Reason: "long-lived dispatch requires deps.Sessions"}, nil
	}

	const fanoutDepth = 64
	fanout := make(chan llmtypes.StreamEvent, fanoutDepth)
	result := &executor.ExecutionResult{}

	// activityCh is signaled non-blockingly on every executor event the
	// stream-fanout drain forwards. Buffered=1 with select-default-drop so a
	// burst of events between wait-loop iterations collapses to one signal —
	// we only care about "did anything arrive since the last timer fire?",
	// not the count.
	activityCh := make(chan struct{}, 1)
	terminalFailureCh := make(chan string, 1)

	// turnDoneCh signals the reminder goroutine on every EventDone the
	// drain observes — the turn-boundary signal for streaming-stdio
	// workers (the substrate's primary kind=agent runtime today). A
	// modest buffer absorbs back-to-back turns while the reminder
	// goroutine is running its check + SendTurn; if the buffer fills
	// (operator floods + slow consumer), we drop additional signals —
	// the same turn boundary is still observable on the next non-dropped
	// event so no envelope is silently stranded. JsonRpc-stdio support
	// is a follow-up: that runtime's `turn/completed` notification is
	// not currently fanned into the stream, so codex long-lived workers
	// receive no reminder pass (they would silently see no nudges, not
	// a regression — they had none before).
	const turnDoneBuf = 8
	turnDoneCh := make(chan struct{}, turnDoneBuf)

	// streamErr captures the first turn-terminal EventError so the run
	// result can surface it in the Reason when the wait loop ends on a
	// non-completion branch (idle reap / hard ceiling). Mirrors the OneShot
	// path's streamErr handling.
	var (
		streamErr     error
		streamErrOnce sync.Once
		fanoutWG      sync.WaitGroup
	)
	fanoutWG.Add(1)
	go func() {
		defer fanoutWG.Done()
		for ev := range fanout {
			translateStreamEvent(ev, result, func(out executor.ExecutionEvent) {
				if cb != nil {
					cb(out)
				}
				select {
				case activityCh <- struct{}{}:
				default:
				}
			}, func(s string) {
				streamErrOnce.Do(func() { streamErr = errors.New(s) })
			})
			// EventDone is the turn-boundary signal — non-blocking
			// publish so the reminder goroutine can act on it. A full
			// channel just drops the extra signal (see turnDoneCh
			// comment above).
			if ev.Type == llmtypes.EventDone {
				select {
				case turnDoneCh <- struct{}{}:
				default:
				}
			}
		}
	}()

	opts = opts.withEventFanout(fanout).withTerminalFailure(terminalFailureCh)
	sess, bootErr := Boot(ctx, e.deps, opts)
	if bootErr != nil {
		// Boot failed before Start succeeded — the lib's terminal-state
		// teardown didn't fire (no live session to tear down), and our
		// fanout drain has no more events to consume.
		close(fanout)
		fanoutWG.Wait()
		reason := bootErr.Error()
		if streamErr != nil {
			reason = streamErr.Error() + " | " + reason
		}
		return &executor.ExecutionResult{Status: "failed", Reason: reason}, nil
	}
	if sess == nil {
		close(fanout)
		fanoutWG.Wait()
		return &executor.ExecutionResult{Status: "failed", Reason: "agent.Boot returned nil session without error"}, nil
	}

	inactivityThreshold := resolveInactivityThreshold(opts)
	hardCeiling := resolveHardCeiling(opts)

	// Turn-boundary reminder pump (CW-20260519-0065). Starts here so
	// sess.ID and opts.TaskID are bound; the drain goroutine has been
	// signaling turnDoneCh since Boot's first EventDone, with the
	// buffered channel absorbing anything that fired before we got here.
	// Survives the wait loop's exit and is joined below after Stop has
	// drained the fanout.
	var reminderWG sync.WaitGroup
	reminderWG.Add(1)
	go runReminderPump(ctx, managerTurnSender{mgr: e.deps.Sessions}, e.deps.Reminder, opts.TaskID, sess, turnDoneCh, &reminderWG)

	outcome := awaitLongLivedCompletion(ctx, e.deps, opts.TaskID, sess.ID, activityCh, terminalFailureCh, inactivityThreshold, hardCeiling)
	if outcome.Kind == outcomeTerminalFailure {
		if err := e.deps.Sessions.protectTerminalFailure(context.Background(), sess.ID); err != nil {
			log.Printf("agent: runLongLived protect terminal-failed session %s failed: %v", sess.ID, err)
		}
	}

	// Stop the live session. teardownSession (registered as the lib's
	// terminal-state hook) closes the stream fanout, which drains any
	// pending events into our local fanout chan; once Stop returns we can
	// close our chan and join the drain goroutine.
	//
	// stopGraceWindow is the package-shared 5s bound on Stop calls; reuse
	// it here so the long-lived path uses the same Stop-grace semantics
	// as session_lifecycle_hook's manager-side teardown.
	stopCtx, cancel := context.WithTimeout(context.Background(), stopGraceWindow)
	if err := e.deps.Sessions.Stop(stopCtx, sess.ID); err != nil && !errors.Is(err, ErrSessionNotRunning) {
		log.Printf("agent: runLongLived stop session %s failed: %v", sess.ID, err)
	}
	cancel()
	if outcome.Kind == outcomeTerminalFailure {
		waitCtx, waitCancel := context.WithTimeout(context.Background(), stopGraceWindow)
		if _, err := e.deps.Sessions.Wait(waitCtx, sess.ID); err != nil && !errors.Is(err, ErrSessionNotRunning) && !errors.Is(err, context.DeadlineExceeded) {
			log.Printf("agent: runLongLived wait terminal-failed session %s failed: %v", sess.ID, err)
		}
		waitCancel()
	}
	close(fanout)
	fanoutWG.Wait()

	// Drain order is load-bearing: the drain goroutine is the sole
	// writer to turnDoneCh, so closing it here (after fanoutWG.Wait)
	// guarantees the reminder goroutine sees a clean close and exits.
	close(turnDoneCh)
	reminderWG.Wait()

	// Per-task registry cleanup: a re-dispatch of this task creates a
	// fresh long-lived run with no inherited dismissals, matching the
	// per-process semantics already implicit in steering.Bridge (which
	// marks each delivered envelope `consumed`, so the same envelope id
	// is never re-injected anyway). Forget is nil-safe; calling it
	// unconditionally keeps the cleanup path uniform.
	e.deps.Reminder.Forget(opts.TaskID)

	res := outcome.toExecutionResult(result, streamErr)

	// Phase 3 — engine-side completion verification. Only meaningful when
	// the worker self-transitioned its task out of doing (we have a real
	// completion to verify); idle reap / hard ceiling / ctx cancellation
	// already produce concrete blocked/failed results that need no
	// engine-side check. We run verification for ANY transition outcome
	// (review/done/blocked/...) so the histogram lands on the run_completed
	// event uniformly — but ApplyTo only overrides on the failure verdicts,
	// so a worker who self-transitioned straight to blocked stays blocked.
	if outcome.Kind == outcomeTransition {
		// Bound the git invocation so a stuck repo (lock, NFS hang)
		// cannot wedge verification forever. workerVerifyTimeout is
		// generous — `git rev-list --count` is sub-second on a healthy
		// repo, but we'd rather not race the scheduler shutdown.
		verifyCtx, verifyCancel := context.WithTimeout(context.Background(), workerVerifyTimeout)
		verdict := scheduler.VerifyWorkerCompletion(verifyCtx, opts.RepoRoot, opts.Workdir, filepath.Join(sess.WorkspaceDir, "logs"), "", sess.RuntimeKind)
		verifyCancel()
		res.ToolUseHistogram = verdict.ToolUseHistogram
		res.CommitsOnRunBranch = verdict.CommitCount
		res.VerificationSkipReason = verdict.SkipReason
		// VerificationRan disambiguates "engine counted 0 commits" from
		// "engine never ran" on the downstream run_completed event. Set
		// true whenever we surfaced a verdict at all (including skip);
		// the SSE/run_completed emitter keys off this flag (not off
		// CommitsOnRunBranch>0) so 0-commit verifications still produce
		// commits_on_run_branch=0 in the payload — the failure-mode
		// signal monitors most want to see.
		res.VerificationRan = true
		// Only override the success path. A worker that self-transitioned
		// to blocked/failed has already explained why; the engine's view
		// is supplemental, not authoritative.
		if outcome.TaskStatus == "review" || outcome.TaskStatus == "done" {
			res.Status, res.Reason = verdict.ApplyTo(res.Status, res.Reason)
		}
	}

	return res, nil
}

// longLivedOutcome captures the wait-loop's verdict on why a long-lived
// worker exited its "doing" phase. The four cases map 1:1 onto distinct
// ExecutionResult shapes — see toExecutionResult.
type longLivedOutcome struct {
	// Kind names which exit branch fired. Single field rather than a sum-
	// type because Go (and a single switch in toExecutionResult is more
	// legible than four constructors).
	Kind longLivedOutcomeKind

	// TaskStatus is the post-transition task.Status when Kind=transition.
	// Zero otherwise.
	TaskStatus string

	// BlockedReason is the post-transition task.BlockedReason. Forwarded
	// to ExecutionResult.Reason for status=blocked.
	BlockedReason string

	// IdleFor is the threshold that fired for Kind=idle.
	IdleFor time.Duration

	// CeilingAt is the threshold that fired for Kind=hardCeiling.
	CeilingAt time.Duration

	// CauseErr captures the ctx error for Kind=ctxCanceled. Populated
	// from ctx.Err() (not context.Cause(ctx)) because the callers that
	// cancel us today — pool shutdown, scheduler stop, dispatchCtx
	// transition cancel — do not thread a cause-bearing ctx.WithCancel
	// Cause variant. If a future caller wants causal errors here, swap
	// the populating call to context.Cause(ctx); the consumer
	// (toExecutionResult) just renders the Error() string and is
	// agnostic to either source.
	CauseErr error

	TerminalFailure string
}

type longLivedOutcomeKind int

const (
	outcomeUnknown longLivedOutcomeKind = iota
	outcomeTransition
	outcomeIdle
	outcomeHardCeiling
	outcomeCtxCanceled
	outcomeTerminalFailure
)

// awaitLongLivedCompletion polls the worker's task.Status and watches the
// activity channel + hard ceiling + ctx. Returns when any of the four
// completion signals fires. Polling cadence is statusPollInterval (5s by
// default); the inactivity timer is in-memory and reset on every event.
func awaitLongLivedCompletion(ctx context.Context, deps *Dependencies, taskID, sessID string, activityCh <-chan struct{}, terminalFailureCh <-chan string, inactivityThreshold, hardCeiling time.Duration) longLivedOutcome {
	statusTicker := time.NewTicker(statusPollInterval)
	defer statusTicker.Stop()

	inactivityTimer := time.NewTimer(inactivityThreshold)
	defer inactivityTimer.Stop()

	hardTimer := time.NewTimer(hardCeiling)
	defer hardTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			// ctx cancellation has three plausible causes:
			//   (a) worker self-transitioned via the loopback —
			//       scheduler.go's global TaskTransitionHook fires
			//       synchronously and cancels the per-task dispatch ctx.
			//       runCtx (which we get) is merged from pool+dispatch, so
			//       the cancel reaches us BEFORE our status poll could
			//       observe the new task.Status.
			//   (b) external operator transitioned the task while doing
			//       (manual cancel, manual block).
			//   (c) scheduler shutdown.
			// Disambiguate via a final task read. emitTaskTransition runs
			// AFTER the row update commits, so by the time we see Done()
			// the new status is already persisted.
			if rec, err := deps.Store.GetTask(taskID); err == nil && rec != nil && rec.Status != "doing" {
				return longLivedOutcome{
					Kind:          outcomeTransition,
					TaskStatus:    rec.Status,
					BlockedReason: rec.BlockedReason,
				}
			}
			return longLivedOutcome{Kind: outcomeCtxCanceled, CauseErr: ctx.Err()}

		case <-activityCh:
			// Reset the inactivity timer. The drain-before-reset dance is
			// the canonical Go pattern for Timer.Reset (the doc explicitly
			// warns: Reset on a fired-but-undrained timer is a bug).
			if !inactivityTimer.Stop() {
				select {
				case <-inactivityTimer.C:
				default:
				}
			}
			inactivityTimer.Reset(inactivityThreshold)

		case msg := <-terminalFailureCh:
			return longLivedOutcome{Kind: outcomeTerminalFailure, TerminalFailure: msg}

		case <-inactivityTimer.C:
			return longLivedOutcome{Kind: outcomeIdle, IdleFor: inactivityThreshold}

		case <-hardTimer.C:
			return longLivedOutcome{Kind: outcomeHardCeiling, CeilingAt: hardCeiling}

		case <-statusTicker.C:
			rec, err := deps.Store.GetTask(taskID)
			if err != nil {
				log.Printf("agent: runLongLived status poll for task %s session %s failed: %v", taskID, sessID, err)
				continue
			}
			if rec == nil {
				log.Printf("agent: runLongLived status poll for task %s session %s returned nil record; bailing as failed", taskID, sessID)
				return longLivedOutcome{Kind: outcomeTransition, TaskStatus: "failed", BlockedReason: "task record disappeared mid-dispatch"}
			}
			if rec.Status == "doing" {
				continue
			}
			return longLivedOutcome{
				Kind:          outcomeTransition,
				TaskStatus:    rec.Status,
				BlockedReason: rec.BlockedReason,
			}
		}
	}
}

// toExecutionResult maps a longLivedOutcome onto the ExecutionResult shape
// the scheduler's lifecycle manager expects. result is the accumulator the
// fanout drain populated with token usage; we overwrite Status / Reason /
// ExitCode and return it intact so token counts survive.
func (o longLivedOutcome) toExecutionResult(result *executor.ExecutionResult, streamErr error) *executor.ExecutionResult {
	if result == nil {
		result = &executor.ExecutionResult{}
	}
	switch o.Kind {
	case outcomeTransition:
		// The worker self-transitioned its task out of "doing" via the
		// loopback — bubble the new task.Status up to the lifecycle
		// manager verbatim. Critical because lifecycle.handleDone /
		// case "review" / case "blocked" each have distinct downstream
		// behaviors (e.g. case "review" + kind=agent is what enqueues
		// the reviewer end-agent via shouldEnqueueEndAgent); flattening
		// these onto a single Status would break the auto-review
		// pipeline.
		switch o.TaskStatus {
		case "review":
			// The reviewer end-agent fires from lifecycle.transition's
			// shouldEnqueueEndAgent hook when case "review" runs, so
			// passing Status="review" up is the correct route. The hook
			// is idempotent for review→review (the worker's svc.Task.
			// Transition fired the FSM hook but NOT enqueueEndAgent —
			// only lifecycle.transition does the enqueue).
			result.Status = "review"
			zero := 0
			result.ExitCode = &zero
		case "done":
			// on_done=close or operator side-channel closed the task.
			// lifecycle.HandleResult's terminal-status guard (isTerminal
			// TaskStatus) will short-circuit to markRunSuperseded — the
			// run row gets superseded status and the task stands. We
			// still return Status="done" so the deferred run.finished
			// SSE event carries a coherent shape.
			result.Status = "done"
			zero := 0
			result.ExitCode = &zero
		case "blocked":
			result.Status = "blocked"
			result.Reason = o.BlockedReason
			if result.Reason == "" {
				result.Reason = "worker self-transitioned to blocked"
			}
		case "cancelled", "canceled":
			result.Status = "canceled"
			if o.BlockedReason != "" {
				result.Reason = o.BlockedReason
			}
		case "failed":
			result.Status = "failed"
			result.Reason = o.BlockedReason
			if result.Reason == "" {
				result.Reason = "worker self-transitioned to failed"
			}
		default:
			// Some other non-doing status (todo/paused/etc). Treat as
			// failed-with-detail so the lifecycle has something concrete.
			result.Status = "failed"
			result.Reason = fmt.Sprintf("worker transitioned to unexpected status %q", o.TaskStatus)
		}
	case outcomeIdle:
		result.Status = "blocked"
		result.Reason = fmt.Sprintf("worker idle past %s — no executor events received within the inactivity threshold", o.IdleFor)
	case outcomeHardCeiling:
		result.Status = "failed"
		result.Reason = fmt.Sprintf("worker exceeded hard ceiling of %s", o.CeilingAt)
	case outcomeCtxCanceled:
		result.Status = "failed"
		if o.CauseErr != nil {
			result.Reason = "execution canceled: " + o.CauseErr.Error()
		} else {
			result.Reason = "execution canceled"
		}
	case outcomeTerminalFailure:
		result.Status = "blocked"
		result.Reason = o.TerminalFailure
		if result.Reason == "" {
			result.Reason = "terminal provider turn failed"
		}
	default:
		result.Status = "failed"
		result.Reason = "unknown long-lived outcome"
	}
	if streamErr != nil && result.Reason == streamErr.Error() {
		return result
	}
	if streamErr != nil && result.Reason != "" {
		result.Reason = streamErr.Error() + " | " + result.Reason
	} else if streamErr != nil {
		result.Reason = streamErr.Error()
	}
	return result
}

// turnSender is the narrow surface runReminderPump needs from the agent
// Manager — just SendTurn, the same primitive the steering bridge uses.
// Defined here so the pump can be unit-tested with a fake without
// standing up a real session manager.
type turnSender interface {
	SendTurn(ctx context.Context, sess *Session, text string) error
}

// managerTurnSender adapts *Manager to turnSender. Trivial wrapper kept
// as a named type so production wiring reads as
// runReminderPump(..., managerTurnSender{mgr: e.deps.Sessions}, ...).
type managerTurnSender struct{ mgr *Manager }

func (m managerTurnSender) SendTurn(ctx context.Context, sess *Session, text string) error {
	if m.mgr == nil {
		return errors.New("agent: turn sender has no manager")
	}
	return m.mgr.SendTurn(ctx, sess, text)
}

// runReminderPump consumes turn-boundary signals from the stream-fanout
// drain and re-surfaces unaddressed steering envelopes to the agent via
// SendTurn (CW-20260519-0065). It exits when turnDoneCh is closed by the
// caller (after the fanout has drained).
//
// Nil-safety: when reminder is nil OR sender is nil OR taskID is empty
// OR sess is nil, the loop drains turnDoneCh without ever building a
// reminder — the runtime degrades cleanly to the prior fire-and-forget
// behavior. We do drain rather than return early because a stuck
// channel writer (the drain goroutine) would block on its non-blocking
// publish forever if nothing read; the drain's select-default-drop
// guard already covers that, but the explicit drain keeps semantics
// symmetric with the active path.
//
// Per-turn behavior: for every signal,
//  1. Ask the registry for any unaddressed envelopes injected since the
//     last turn boundary.
//  2. If pending and the turn actually saw an injection, render a
//     reminder turn body and SendTurn it back into the live session.
//  3. Mark the listed envelopes as reminded so the next turn boundary
//     doesn't re-nag them. (Dismissal is a separate signal from the
//     torque_steering_dismiss MCP tool.)
//
// A SendTurn error is logged but does NOT mark the envelopes reminded —
// the next turn that sees an injection will retry the surface so a
// transient session-pipe glitch doesn't strand a reminder.
//
// ctx cancellation is observed cooperatively: the drain goroutine will
// also stop signaling soon after, and the close of turnDoneCh remains
// the authoritative exit.
func runReminderPump(
	ctx context.Context,
	sender turnSender,
	reminder *steering.ReminderRegistry,
	taskID string,
	sess *Session,
	turnDoneCh <-chan struct{},
	wg *sync.WaitGroup,
) {
	defer wg.Done()

	for {
		select {
		case <-ctx.Done():
			// Drain anything still in the channel so the writer
			// (drain goroutine) doesn't have to retry select-default-
			// drop on every event after ctx death; the close below
			// will eventually exit us cleanly.
			for {
				select {
				case _, ok := <-turnDoneCh:
					if !ok {
						return
					}
				default:
					return
				}
			}
		case _, ok := <-turnDoneCh:
			if !ok {
				return
			}
		}

		// Cheap guards: nil reminder/sender, empty taskID, or nil
		// session means we have nothing to do this turn. We still
		// wanted to consume the signal to keep the channel drained.
		if reminder == nil || sender == nil || taskID == "" || sess == nil {
			continue
		}

		pending, hadInjection := reminder.PendingForTurnBoundary(taskID)
		if !hadInjection || len(pending) == 0 {
			continue
		}

		text := steering.RenderReminder(pending, time.Now())
		if text == "" {
			continue
		}

		if err := sender.SendTurn(ctx, sess, text); err != nil {
			log.Printf("agent: turn-boundary reminder SendTurn failed task=%s session=%s: %v (will retry next turn boundary)", taskID, sess.ID, err)
			continue
		}

		ids := make([]string, 0, len(pending))
		for _, pe := range pending {
			ids = append(ids, pe.EnvelopeID)
		}
		reminder.MarkReminded(taskID, ids...)
	}
}
