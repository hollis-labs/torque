package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/executor"
)

// statusPollInterval is the cadence at which runLongLived re-reads the
// worker's task.status to detect a self-transition out of "doing". 5s is
// the operator-tunable knob; faster polling buys nothing on minute-scale
// liveness budgets and would just stress the SQLite WAL. Exposed as a var
// (not const) so the existing test path can shrink it for fast-test runs.
var statusPollInterval = 5 * time.Second

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
		}
	}()

	opts = opts.withEventFanout(fanout)
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

	outcome := awaitLongLivedCompletion(ctx, e.deps, opts.TaskID, sess.ID, activityCh, inactivityThreshold, hardCeiling)

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
	close(fanout)
	fanoutWG.Wait()

	return outcome.toExecutionResult(result, streamErr), nil
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
}

type longLivedOutcomeKind int

const (
	outcomeUnknown longLivedOutcomeKind = iota
	outcomeTransition
	outcomeIdle
	outcomeHardCeiling
	outcomeCtxCanceled
)

// awaitLongLivedCompletion polls the worker's task.Status and watches the
// activity channel + hard ceiling + ctx. Returns when any of the four
// completion signals fires. Polling cadence is statusPollInterval (5s by
// default); the inactivity timer is in-memory and reset on every event.
func awaitLongLivedCompletion(ctx context.Context, deps *Dependencies, taskID, sessID string, activityCh <-chan struct{}, inactivityThreshold, hardCeiling time.Duration) longLivedOutcome {
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
	default:
		result.Status = "failed"
		result.Reason = "unknown long-lived outcome"
	}
	if streamErr != nil && result.Reason != "" {
		result.Reason = streamErr.Error() + " | " + result.Reason
	} else if streamErr != nil {
		result.Reason = streamErr.Error()
	}
	return result
}
