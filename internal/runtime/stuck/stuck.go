// Package stuck implements the agentic-execution stuck-task recovery probe
// (CW-20260512-0063, sprint α.5).
//
// When an upstream stuck signal fires for a live session (today: no signal
// is wired — see "Trigger wireup" below; the probe API + state machine is
// scoped per sprint-α D2), Probe runs the deterministic three-state
// recovery loop:
//
//  1. PROBE   — SendInput a user-turn message asking the agent to emit a
//               status_update envelope describing its state.
//  2. WAIT    — Subscribe to the broker filtered to MsgKindStatusUpdate and
//               wait up to WaitTimeout for an envelope tagged with
//               metadata.task_id == TaskID. The first match wins.
//  3a. RESPOND — On envelope receipt: hand the envelope to the reactor
//               Dispatcher (the same surface α.3 wired). status_update:
//               blocked routes through to pause-and-block; non-blocked
//               status_updates are noop+log per α.3 D2 contract — no
//               duplicate routing in the probe.
//  3b. RESUME  — On WaitTimeout silence: Checkpoint the session (for audit
//               + linkage), then ResumeSession with ResumeOptions.
//               DiagnosticNote set to the silence-framing prepend so the
//               resumed transcript's first turn sees the operator-style
//               "you went silent, here's what to do" framing.
//
// Composition with α.2 / α.3 / α.4:
//
//   - α.2 (Manager.ResumeSession + ResumeOptions.DiagnosticNote): the
//     RESUME branch invokes this primitive directly. Capability transparency
//     is preserved — ResumeSession internally branches on
//     ProviderCapabilities(provider).SupportsResume; the probe does NOT
//     re-check the capability.
//   - α.3 (reactor.Dispatcher): the RESPOND branch routes the received
//     status_update envelope through the same dispatcher α.3 wires into
//     production. status_update:blocked is the only status_update sub-state
//     that mutates state today; non-blocked status_updates noop+log. The
//     probe is NOT a parallel dispatch path.
//   - α.4 (CheckpointResponseDispatcher): same primitives (ResumeSession +
//     SendInput) but a different trigger — α.4 is operator-driven (HITL
//     checkpoint response), α.5 is autonomous (harness-driven probe).
//     The two share no code today; both compose on Manager.ResumeSession.
//
// Trigger wireup status (per sprint-α D2):
//
// As of α.5, there is NO existing "stuck signal" in the runtime. The
// pid_poller (internal/runtime/agent/pid_poller.go) writes LastActivity
// but no consumer reads that timestamp to decide "stuck". This package
// defines the probe state machine + tests; wiring a no-progress timer
// (or equivalent) to call Probe is tracked as a separate follow-up. See
// the implementer report for the suggested ticket title.
package stuck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	gomsg "github.com/hollis-labs/go-messaging"

	"github.com/hollis-labs/clockwork-manifold/internal/broker"
)

// DefaultWaitTimeout is the WAIT-phase timeout used when ProbeInput.
// WaitTimeout is zero. 90s is the midpoint of the 60–120s range called out
// in the sprint-α α.5 design — long enough that a slow-but-progressing
// agent can deliver a status_update envelope, short enough that a hung
// session doesn't park a recovery loop for minutes. Tunable via config
// (config.StuckProbeWaitSeconds) or per-call via ProbeInput.WaitTimeout.
const DefaultWaitTimeout = 90 * time.Second

// DefaultProbeMessage is the user-turn payload sent to the live session
// in the PROBE phase when ProbeInput.ProbeMessage is empty. Per sprint-α
// D2 the wording is intentionally terse and operator-style: it asks the
// agent to emit a status_update envelope so the WAIT phase has a
// matchable surface.
const DefaultProbeMessage = "Are you stuck? Respond with a status_update envelope " +
	"(broker tool: clockwork_broker_send, kind=status_update). " +
	"Set state to one of: working, blocked, idle. " +
	"Include a brief note describing your current step or block."

// DefaultDiagnosticNote is the system-prompt-prepend used when
// ProbeInput.DiagnosticNote is empty. Lands at the top of the resumed
// session's composed system prompt via ResumeOptions.DiagnosticNote.
//
// The %s placeholder is the WaitTimeout duration (e.g. "90s") so the
// resumed agent sees the actual silence window it produced. The resume
// path Sprintfs this in BuildSilenceNote so callers passing
// ProbeInput.DiagnosticNote=verbatim don't need to know about the
// formatting verb.
const DefaultDiagnosticNote = "The previous turn went silent for %s after a stuck-probe. " +
	"The substrate has resumed your session. " +
	"Review your transcript, identify what blocked you, and emit a status_update envelope " +
	"with state (working|blocked|other) and a brief explanation. " +
	"If you were waiting on a tool result that never returned, log it and proceed with the next step."

// Outcome enumerates the terminal results of a Probe call. Returned in
// Result.Outcome for observability + test assertions.
type Outcome string

const (
	// OutcomeResponded — the agent replied with a status_update envelope
	// before WaitTimeout. The envelope was handed to the reactor
	// Dispatcher; Result.Dispatch captures whatever the dispatcher did.
	OutcomeResponded Outcome = "responded"

	// OutcomeResumed — WaitTimeout fired with no status_update from the
	// agent. The session was checkpointed and ResumeSession was invoked
	// with DiagnosticNote. Result.ResumedSessionID names the new session.
	OutcomeResumed Outcome = "resumed"

	// OutcomeFailed — a primitive (SendInput / Subscribe / Checkpoint /
	// ResumeSession) returned an error and the probe could not complete.
	// Result.Err carries the underlying cause.
	OutcomeFailed Outcome = "failed"
)

// Result is the terminal value of a Probe call. Side effects (envelope
// dispatch, checkpoint write, resume boot) have already happened by the
// time the caller sees the result; Result is for logging + tests.
type Result struct {
	Outcome           Outcome
	TaskID            string
	OriginalSessionID string

	// ResumedSessionID is populated only on OutcomeResumed — the new
	// session-id returned by ResumeSession. Callers can use this to wire
	// the next iteration's monitoring (the resumed session may itself go
	// stuck, which would be a separate Probe invocation by the upstream
	// signal).
	ResumedSessionID string

	// CheckpointID is populated only on OutcomeResumed — the audit
	// checkpoint persisted before ResumeSession.
	CheckpointID string

	// EnvelopeID is populated only on OutcomeResponded — the
	// status_update envelope ID the agent emitted.
	EnvelopeID string

	// Dispatch is populated only on OutcomeResponded — whatever
	// the reactor.Dispatcher's DispatchResult was for the received
	// envelope. Surfaced verbatim so callers can correlate the probe's
	// response branch with the V0 dispatch table's action.
	//
	// The field type is `any` to avoid an import cycle with reactor;
	// callers that want to introspect can type-assert to
	// reactor.DispatchResult.
	Dispatch any

	// Err is the underlying error that produced OutcomeFailed. Nil
	// otherwise.
	Err error
}

// ResumeManager is the narrow surface Probe needs from
// agent.Manager.ResumeSession. Defined here as an interface so tests
// can inject a fake without standing up the full agent.Manager.
type ResumeManager interface {
	// ResumeSession is the α.2 primitive. Returns the new session-id on
	// success. Implementations are expected to bridge agent.Manager.
	// ResumeSession(ctx, sessionID, ResumeOptions{DiagnosticNote: ...})
	// and return newSession.ID (or "" on error).
	ResumeSession(ctx context.Context, sessionID, diagnosticNote string) (newSessionID string, err error)
}

// InputSender is the narrow surface Probe needs from
// agent.Manager.SendInput. Same shape; defined as an interface for
// test seam parity with ResumeManager.
type InputSender interface {
	SendInput(sessionID string, payload []byte) error
}

// CheckpointMaker is the narrow surface Probe needs to persist a
// session checkpoint before resume. Bridges agent.Manager.Checkpoint(
// CheckpointRequest{SessionID, Payload, Note}) — returns the new
// checkpoint-id.
//
// The payload is a small JSON blob the probe constructs (reason +
// elapsed-silence + probe-message) so postmortem queries can correlate
// the audit checkpoint with the silence window.
type CheckpointMaker interface {
	Checkpoint(sessionID, payload, note string) (checkpointID string, err error)
}

// EnvelopeDispatcher is the narrow surface Probe uses to route a
// received status_update envelope through the α.3 dispatch table.
// Production wires reactor.Dispatcher.Dispatch; the returned `any`
// is the dispatcher's DispatchResult (kept type-erased here to avoid
// an import cycle on reactor).
type EnvelopeDispatcher interface {
	Dispatch(ctx context.Context, env gomsg.Envelope) any
}

// EnvelopeSource is the narrow surface Probe needs to receive
// envelopes during the WAIT phase. Production wires
// broker.Broker.Subscribe; the channel must close (or stop delivering)
// when ctx is canceled so the WAIT-phase goroutine exits cleanly.
//
// The filter is set by Probe to MsgKindStatusUpdate so the WAIT loop
// doesn't have to sift through unrelated kinds; task_id correlation is
// done by Probe itself reading env.Metadata["task_id"] because Filter
// has no metadata predicate today.
type EnvelopeSource interface {
	Subscribe(ctx context.Context, to gomsg.Address, f gomsg.Filter) (<-chan gomsg.Envelope, error)
}

// ProbeInput bundles the per-call parameters Probe needs. SessionID +
// TaskID + the four surfaces are required; everything else has a
// documented default.
type ProbeInput struct {
	// SessionID — the live session under suspicion. The PROBE phase
	// SendInputs to this id; the RESUME branch passes it to
	// ResumeSession; the WAIT branch correlates incoming status_update
	// envelopes by metadata.task_id (NOT by session_id — task_id is the
	// stable correlation key across the resume boundary).
	SessionID string

	// TaskID — the task the session is executing. Used to filter
	// incoming envelopes during WAIT and as the breadcrumb correlation
	// key for the audit checkpoint.
	TaskID string

	// ProbeMessage — the user-turn payload sent to the live session in
	// the PROBE phase. Empty falls back to DefaultProbeMessage.
	ProbeMessage string

	// WaitTimeout — how long to wait for a status_update envelope
	// matching TaskID. Zero falls back to DefaultWaitTimeout.
	WaitTimeout time.Duration

	// DiagnosticNote — the literal system-prompt prepend passed to
	// ResumeOptions.DiagnosticNote on the silence branch. Empty falls
	// back to the DefaultDiagnosticNote template Sprintf'd with the
	// actual WaitTimeout. Callers that want a custom framing pass the
	// fully-rendered string here.
	DiagnosticNote string

	// Sender / Source / Dispatch / Resume / Checkpoint — the four
	// in-process primitives. All required; nil triggers an
	// OutcomeFailed with a descriptive error so partial wireup doesn't
	// silently degrade.
	Sender     InputSender
	Source     EnvelopeSource
	Dispatch   EnvelopeDispatcher
	Resume     ResumeManager
	Checkpoint CheckpointMaker
}

// Probe runs the stuck-task recovery state machine. See the package doc
// for the three-state design (PROBE → WAIT → RESPOND|RESUME). Returns
// a Result describing the terminal outcome; side effects have already
// fired by the time Probe returns.
//
// Probe is synchronous — it blocks the calling goroutine for up to
// WaitTimeout plus the resume boot wall-clock. Callers running multiple
// probes concurrently are expected to launch their own goroutines; the
// state machine itself holds no shared state and is safe for
// per-session concurrent calls.
//
// ctx cancellation aborts the WAIT phase (returning OutcomeFailed with
// ctx.Err()); a cancellation during RESUME propagates to ResumeSession
// and surfaces as OutcomeFailed. The PROBE-phase SendInput is best-
// effort (no per-call deadline beyond the underlying SendInput's own
// timing); a send error halts the probe with OutcomeFailed before WAIT
// is entered.
func Probe(ctx context.Context, in ProbeInput) Result {
	res := Result{
		TaskID:            in.TaskID,
		OriginalSessionID: in.SessionID,
		Outcome:           OutcomeFailed,
	}

	if err := validateInput(in); err != nil {
		res.Err = err
		return res
	}

	probeMsg := in.ProbeMessage
	if probeMsg == "" {
		probeMsg = DefaultProbeMessage
	}

	waitTimeout := in.WaitTimeout
	if waitTimeout <= 0 {
		waitTimeout = DefaultWaitTimeout
	}

	// Build the diagnostic note up-front (cheap, deterministic) so the
	// resume branch doesn't have to re-derive it if WaitTimeout fires.
	diagNote := in.DiagnosticNote
	if diagNote == "" {
		diagNote = BuildSilenceNote(waitTimeout)
	}

	// Subscribe BEFORE SendInput — otherwise a hyper-responsive agent
	// could emit a status_update envelope between SendInput's return and
	// our Subscribe call, and the WAIT loop would miss it. Per α.3 D2
	// the broker Subscribe is system-wide (zero Address); we filter by
	// kind here and correlate by metadata.task_id inside the loop.
	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()

	ch, err := in.Source.Subscribe(subCtx, gomsg.Address{}, gomsg.Filter{
		Kind: []gomsg.Kind{gomsg.MsgKindStatusUpdate},
	})
	if err != nil {
		res.Err = fmt.Errorf("stuck.Probe subscribe: %w", err)
		return res
	}

	// PROBE phase — SendInput. Failure here halts the probe; we have no
	// signal to wait on if the input never reached the agent.
	if err := in.Sender.SendInput(in.SessionID, []byte(probeMsg)); err != nil {
		res.Err = fmt.Errorf("stuck.Probe send_input: %w", err)
		return res
	}

	// WAIT phase — block on (envelope channel | timeout | ctx). The
	// envelope channel may deliver status_updates for OTHER tasks (we
	// subscribed system-wide); filter inside the loop by task_id so
	// noisy cross-task traffic doesn't break the probe. Drain non-match
	// envelopes silently — those are routed by the production reactor
	// Loop separately; the probe does NOT re-route them.
	timer := time.NewTimer(waitTimeout)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			res.Err = fmt.Errorf("stuck.Probe ctx: %w", ctx.Err())
			return res

		case <-timer.C:
			// Silence branch — checkpoint + resume.
			return runResumeBranch(ctx, in, diagNote, res)

		case env, ok := <-ch:
			if !ok {
				// Channel closed before timeout — the broker / source
				// went away. Treat as silence so the recovery loop
				// still runs; the alternative (returning OutcomeFailed)
				// would leave the stuck session stuck.
				log.Printf("[stuck] envelope source closed for task=%s before timeout; falling through to resume", in.TaskID)
				return runResumeBranch(ctx, in, diagNote, res)
			}

			if !envelopeMatchesTask(env, in.TaskID) {
				// Not our task — keep waiting. This is the noisy
				// cross-task case; the production reactor.Loop is
				// separately routing this envelope. Do not double-route.
				continue
			}

			// Match — route through α.3 dispatcher and return.
			dispatchRes := in.Dispatch.Dispatch(ctx, env)
			res.Outcome = OutcomeResponded
			res.EnvelopeID = env.ID
			res.Dispatch = dispatchRes
			res.Err = nil
			return res
		}
	}
}

// runResumeBranch fires the silence-path side effects: checkpoint the
// session for audit linkage, then ResumeSession with the diagnostic
// note. Errors from either primitive flip the outcome to OutcomeFailed
// with the wrapped cause.
//
// The checkpoint is "no-operator-response-needed" by design — α.4's
// MVP uses Manager.Checkpoint(CheckpointRequest{...}) which writes a
// session_checkpoints row but does NOT emit a checkpoint_request
// envelope (that's the HITL flow). The probe's checkpoint is purely
// audit; the postmortem can join `session_checkpoints` rows on the
// `note` column carrying "stuck-probe-silence" to find recovery
// events.
func runResumeBranch(ctx context.Context, in ProbeInput, diagNote string, res Result) Result {
	payload, marshalErr := json.Marshal(silenceCheckpointPayload{
		Reason:        "stuck-probe-silence",
		TaskID:        in.TaskID,
		WaitTimeoutMs: int(durationOrDefault(in.WaitTimeout, DefaultWaitTimeout) / time.Millisecond),
		ProbeMessage:  probeMessageOrDefault(in.ProbeMessage),
	})
	if marshalErr != nil {
		// json.Marshal of a fixed struct shouldn't fail; if it does the
		// audit checkpoint is best-effort, so fall back to a stable
		// human-readable payload rather than aborting the resume.
		payload = []byte(`{"reason":"stuck-probe-silence","payload_marshal_err":true}`)
	}

	cpID, err := in.Checkpoint.Checkpoint(in.SessionID, string(payload), "stuck-probe-silence")
	if err != nil {
		res.Err = fmt.Errorf("stuck.Probe checkpoint: %w", err)
		return res
	}
	res.CheckpointID = cpID

	newSessID, err := in.Resume.ResumeSession(ctx, in.SessionID, diagNote)
	if err != nil {
		res.Err = fmt.Errorf("stuck.Probe resume_session: %w", err)
		return res
	}
	res.Outcome = OutcomeResumed
	res.ResumedSessionID = newSessID
	res.Err = nil
	return res
}

// BuildSilenceNote renders the DefaultDiagnosticNote template with the
// supplied WaitTimeout. Exported so callers can preview the framing or
// override it for their own DiagnosticNote (e.g. an operator runbook
// hosting the same template plus a project-specific suffix).
func BuildSilenceNote(waitTimeout time.Duration) string {
	d := durationOrDefault(waitTimeout, DefaultWaitTimeout)
	// time.Duration.String() prints "1m30s" / "90s" — terse and stable.
	return fmt.Sprintf(DefaultDiagnosticNote, d.String())
}

// silenceCheckpointPayload is the JSON shape the audit checkpoint
// stores on the silence branch. Keeps the payload small and queryable;
// the resumed session's first turn already sees the framing via
// DiagnosticNote, so this is operator-side breadcrumb data only.
type silenceCheckpointPayload struct {
	Reason        string `json:"reason"`
	TaskID        string `json:"task_id,omitempty"`
	WaitTimeoutMs int    `json:"wait_timeout_ms"`
	ProbeMessage  string `json:"probe_message,omitempty"`
}

// envelopeMatchesTask reports whether env's metadata.task_id matches
// taskID. Empty taskID never matches (defensive: callers should not
// pass empty TaskID but validateInput catches that earlier; this is
// belt-and-suspenders for the WAIT loop).
func envelopeMatchesTask(env gomsg.Envelope, taskID string) bool {
	if taskID == "" || env.Metadata == nil {
		return false
	}
	return env.Metadata["task_id"] == taskID
}

// validateInput enforces the required-field contract documented on
// ProbeInput. Returns nil when the input is complete; otherwise returns
// a descriptive error that Probe surfaces as Result.Err.
func validateInput(in ProbeInput) error {
	if in.SessionID == "" {
		return errors.New("stuck.Probe: SessionID required")
	}
	if in.TaskID == "" {
		return errors.New("stuck.Probe: TaskID required")
	}
	if in.Sender == nil {
		return errors.New("stuck.Probe: Sender required")
	}
	if in.Source == nil {
		return errors.New("stuck.Probe: Source required")
	}
	if in.Dispatch == nil {
		return errors.New("stuck.Probe: Dispatch required")
	}
	if in.Resume == nil {
		return errors.New("stuck.Probe: Resume required")
	}
	if in.Checkpoint == nil {
		return errors.New("stuck.Probe: Checkpoint required")
	}
	return nil
}

func durationOrDefault(d, fallback time.Duration) time.Duration {
	if d <= 0 {
		return fallback
	}
	return d
}

func probeMessageOrDefault(s string) string {
	if s == "" {
		return DefaultProbeMessage
	}
	return s
}

// Compile-time guard that broker.StatusPayload's relevant fields are
// stable. The probe doesn't decode the envelope itself (the α.3
// Dispatcher does), but a downstream caller introspecting Result.
// Dispatch may need to. Pinning the import here keeps the type
// dependency explicit even when no other symbol from broker is used.
var _ = broker.StatusPayload{}
