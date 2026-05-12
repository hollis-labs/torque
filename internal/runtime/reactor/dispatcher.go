// Package reactor implements Clockwork's deterministic envelope-to-action
// dispatcher (CW-20260512-0061, sprint α.3). It pattern-matches incoming
// broker envelopes by Kind and dispatches each to a deterministic
// in-process action.
//
// Scope (V0, sprint α): four envelope kinds route to actions; everything
// else is noop+log. Per sprint-α D5, the dispatch table is a struct +
// switch — not a registry, not pluggable yet. V1 may extract once shapes
// settle.
//
// Layering: this package sits ON TOP of internal/broker (which owns
// transport + typing) and BESIDE internal/runtime/scheduler (which owns
// task lifecycle). It does NOT redefine envelope types — it consumes them.
// In-process action surfaces are passed in as narrow interfaces so the
// dispatcher remains testable without dragging the full daemon wiring.
package reactor

import (
	"context"
	"encoding/json"
	"log"
	"time"

	gomsg "github.com/hollis-labs/go-messaging"

	"github.com/hollis-labs/clockwork-manifold/internal/broker"
)

// Action is the symbolic result of a Dispatch call. Returned for
// observability and test assertions; the dispatcher already performed the
// concrete side effect by the time the caller sees the result.
type Action string

const (
	// ActionNoop is returned when an envelope kind has no V0 routing
	// (everything outside the four enumerated kinds) or when a routed
	// envelope's payload doesn't match the V0 sub-state (e.g. a
	// status_update whose state is not "blocked"). Side effect: a log
	// line. No state mutation.
	ActionNoop Action = "noop"

	// ActionEscalationCheckpoint fires for kind=escalation: a HITL
	// checkpoint is emitted via the configured CheckpointEmitter.
	ActionEscalationCheckpoint Action = "escalation_checkpoint"

	// ActionPauseAndBlock fires for kind=status_update with state=blocked:
	// the task is transitioned to "blocked" (with a reason), its owning
	// session is stopped, and the operator is notified via the event bus.
	ActionPauseAndBlock Action = "pause_and_block"

	// ActionFanout fires for kind=request: the envelope is forwarded to
	// the addressed peer via the broker's Send pipeline. V0 keeps the
	// fanout 1:1 (request envelopes are already addressed); future
	// versions may broadcast to a set.
	ActionFanout Action = "fanout"

	// ActionReassign fires for kind=handoff: the addressed task's
	// AgentProfile is updated to the handoff target so the scheduler's
	// next pick routes to the new assignee.
	ActionReassign Action = "reassign"
)

// DispatchResult is the outcome of a single Dispatch call. The fields are
// for observability + test assertion — side effects have already happened
// by the time the caller sees the result.
//
// Err is non-nil only when the action FIRED but the underlying in-process
// surface returned an error. Unknown kinds, malformed payloads, and
// status_updates with non-"blocked" state are not errors — they return
// Action=ActionNoop and Err=nil. The intent is that callers loop the
// dispatcher without aborting on per-envelope routing misses.
type DispatchResult struct {
	Action        Action
	EnvelopeID    string
	EnvelopeKind  gomsg.Kind
	TaskID        string // task target when the action carries one; empty otherwise
	CorrelationID string // checkpoint correlation_id for escalation; empty otherwise
	Err           error
}

// CheckpointEmitter is the narrow surface the dispatcher needs to convert
// a kind=escalation envelope into a HITL checkpoint. Production wires
// service.CheckpointService.Emit through a tiny adapter; tests pass a
// fake that records calls.
//
// Returning the correlation_id lets the dispatcher echo it in
// DispatchResult so test + log paths can correlate the envelope to the
// checkpoint it produced.
type CheckpointEmitter interface {
	Emit(taskID, payloadJSON string) (correlationID string, err error)
}

// TaskBlocker is the narrow surface the dispatcher needs to mark a task
// blocked with a reason. Production wires
// sqlstore.Store.TransitionTaskWithReason.
type TaskBlocker interface {
	TransitionTaskWithReason(taskID, newStatus, reason string) error
}

// SessionStopper is the narrow surface the dispatcher needs to pause
// (stop, in Clockwork terms) a session whose task just blocked.
// Production wires agent.Manager.Stop.
type SessionStopper interface {
	Stop(ctx context.Context, sessionID string) error
}

// PeerFanout is the narrow surface the dispatcher needs to forward a
// kind=request envelope to its addressed peer. Production wires
// broker.Broker.Send.
type PeerFanout interface {
	Send(ctx context.Context, env gomsg.Envelope) (gomsg.Envelope, error)
}

// AgentReassigner is the narrow surface the dispatcher needs to apply a
// kind=handoff envelope's reassignment to the target task's
// AgentProfile. Production wires service.TaskService.Update.
type AgentReassigner interface {
	ReassignTask(taskID, agentProfile string) error
}

// OperatorNotifier is the narrow surface the dispatcher uses to notify
// operators of a pause-and-block action. Production wires
// scheduler.EventBus.Publish; tests pass a recording fake.
type OperatorNotifier interface {
	Notify(eventType, taskID string, data map[string]any)
}

// Deps bundles the in-process surfaces the Dispatcher calls into. All
// fields are required for the routed kind they back; a nil surface
// degrades that one action to Action=ActionNoop with a log line — the
// dispatcher refuses to be partially wired in production but tolerates
// nils so unit tests can exercise individual rows of the table.
type Deps struct {
	Checkpoints CheckpointEmitter
	Blocker     TaskBlocker
	Sessions    SessionStopper
	Fanout      PeerFanout
	Reassign    AgentReassigner
	Notifier    OperatorNotifier
}

// Dispatcher is the data-driven envelope router. Construct via New and
// call Dispatch per envelope. Safe for concurrent use — the dispatcher
// holds no mutable state of its own; serialization is the caller's job
// when ordering matters (single-goroutine Loop is the V0 norm).
type Dispatcher struct {
	deps Deps
}

// New returns a Dispatcher wired to the given deps. Deps may be partially
// populated; per-row nil-tolerance is documented on Deps.
func New(deps Deps) *Dispatcher {
	return &Dispatcher{deps: deps}
}

// Dispatch routes one envelope. Returns a DispatchResult describing the
// action taken (or ActionNoop when the envelope didn't match any V0 row).
// Unknown kinds and payload-shape misses do NOT return errors — they log
// and return Action=ActionNoop so the Loop can keep consuming without
// special-casing.
//
// Errors are returned only when a routed action's underlying surface
// failed (e.g. CheckpointEmitter.Emit returned an error). The action
// fired; the caller decides whether to retry, escalate, or drop.
func (d *Dispatcher) Dispatch(ctx context.Context, env gomsg.Envelope) DispatchResult {
	res := DispatchResult{EnvelopeID: env.ID, EnvelopeKind: env.Kind, Action: ActionNoop}
	switch env.Kind {
	case gomsg.MsgKindEscalation:
		return d.handleEscalation(env, res)
	case gomsg.MsgKindStatusUpdate:
		return d.handleStatusUpdate(ctx, env, res)
	case gomsg.MsgKindRequest:
		return d.handleRequest(ctx, env, res)
	case gomsg.MsgKindHandoff:
		return d.handleHandoff(env, res)
	default:
		// V0 routes exactly four kinds; everything else (notice, response,
		// future kinds gomsg may add) lands here as a noop+log. Per sprint-α
		// D2: do not invent extras.
		logNoop(env, "unrouted envelope kind")
		return res
	}
}

// handleEscalation routes kind=escalation to a HITL checkpoint emit. The
// task_id binding is read from env.Metadata["task_id"]; a missing/empty
// value drops the envelope to noop+log because the existing CheckpointService
// requires a task_id. The full envelope payload (canonical EscalationPayload
// JSON) is passed through as the checkpoint payload so operator UIs see
// the original severity + reason verbatim.
func (d *Dispatcher) handleEscalation(env gomsg.Envelope, res DispatchResult) DispatchResult {
	taskID := metadataString(env, "task_id")
	if taskID == "" {
		logNoop(env, "escalation envelope missing metadata.task_id")
		return res
	}
	res.TaskID = taskID
	if d.deps.Checkpoints == nil {
		logNoop(env, "escalation surface (CheckpointEmitter) not wired")
		return res
	}
	corr, err := d.deps.Checkpoints.Emit(taskID, string(env.Payload))
	res.Action = ActionEscalationCheckpoint
	res.CorrelationID = corr
	res.Err = err
	return res
}

// handleStatusUpdate routes kind=status_update — but ONLY when the
// payload's state field is "blocked". Other status states (running,
// progress heartbeats, etc.) are noop+log per sprint-α D2: V0 covers
// status_update:blocked specifically; the broader stream is intentionally
// noisy and shouldn't trigger side effects.
//
// On the blocked path: transition the task → blocked with the envelope's
// note as the reason, stop the owning session, and publish an
// "envelope.paused" event for operator UIs. Each step is best-effort; a
// failure on one does NOT abort the others (we want operators to see
// the notification even if the session-stop tripped a transient error).
// The first error encountered wins res.Err.
func (d *Dispatcher) handleStatusUpdate(ctx context.Context, env gomsg.Envelope, res DispatchResult) DispatchResult {
	var p broker.StatusPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		logNoop(env, "status_update payload not decodable: "+err.Error())
		return res
	}
	if p.State != "blocked" {
		// Non-blocked status_updates are intentionally inert in V0.
		return res
	}
	taskID := metadataString(env, "task_id")
	if taskID == "" {
		logNoop(env, "status_update:blocked envelope missing metadata.task_id")
		return res
	}
	res.TaskID = taskID

	// Per the Deps contract: a nil required surface degrades that one action
	// to ActionNoop. TaskBlocker is the load-bearing surface for pause+block
	// (the task transition is what "blocked" means at the substrate). Without
	// it, neither stopping the session nor notifying makes the task blocked,
	// so we degrade rather than mis-report.
	if d.deps.Blocker == nil {
		logNoop(env, "status_update:blocked surface (TaskBlocker) not wired")
		return res
	}
	res.Action = ActionPauseAndBlock

	reason := p.Note
	if reason == "" {
		reason = "agent reported blocked state via status_update envelope"
	}

	if err := d.deps.Blocker.TransitionTaskWithReason(taskID, "blocked", reason); err != nil && res.Err == nil {
		res.Err = err
	}

	if sessionID := metadataString(env, "session_id"); sessionID != "" && d.deps.Sessions != nil {
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := d.deps.Sessions.Stop(stopCtx, sessionID); err != nil && res.Err == nil {
			res.Err = err
		}
		cancel()
	}

	if d.deps.Notifier != nil {
		d.deps.Notifier.Notify("envelope.paused", taskID, map[string]any{
			"envelope_id": env.ID,
			"reason":      reason,
		})
	}

	return res
}

// handleRequest routes kind=request — broker fanout to the addressed peer.
// V0 forwarding is 1:1: the envelope is re-Sent through the broker so the
// addressed peer's Inbox/Subscribe stream receives it. The dispatcher does
// NOT block on a response; that's the Broker.Request caller's affair.
//
// Why re-Send rather than relying on the existing Send the producer
// already performed? Because in V0 the dispatcher's role is to formalize
// the "react to this envelope" surface — a peer agent may want the reactor
// to forward an internally-generated request that bypassed the broker
// (e.g. a stuck-task probe synthesized by α.5). The current pass-through
// keeps the surface honest: every request that flows through the reactor
// is observable as a broker.Send.
func (d *Dispatcher) handleRequest(ctx context.Context, env gomsg.Envelope, res DispatchResult) DispatchResult {
	if d.deps.Fanout == nil {
		logNoop(env, "request fanout surface (PeerFanout) not wired")
		return res
	}
	// Clear the ID so the underlying store assigns a fresh one; this is the
	// same pattern dispatcher.Request uses (see go-messaging dispatcher.go:37).
	forward := env
	forward.ID = ""
	if _, err := d.deps.Fanout.Send(ctx, forward); err != nil {
		res.Err = err
	}
	res.Action = ActionFanout
	return res
}

// handleHandoff routes kind=handoff — orchestrator reassignment. The
// envelope's HandoffPayload carries task_id + to (target agent profile);
// we apply both via the AgentReassigner surface.
//
// Missing payload fields drop to noop+log: a handoff without a task_id is
// either malformed or intended as a session-only courtesy notice, neither
// of which the V0 dispatcher acts on.
func (d *Dispatcher) handleHandoff(env gomsg.Envelope, res DispatchResult) DispatchResult {
	var p broker.HandoffPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		logNoop(env, "handoff payload not decodable: "+err.Error())
		return res
	}
	if p.TaskID == "" || p.To == "" {
		logNoop(env, "handoff payload missing task_id or to")
		return res
	}
	res.TaskID = p.TaskID
	if d.deps.Reassign == nil {
		logNoop(env, "handoff surface (AgentReassigner) not wired")
		return res
	}
	if err := d.deps.Reassign.ReassignTask(p.TaskID, p.To); err != nil {
		res.Err = err
	}
	res.Action = ActionReassign
	return res
}

// metadataString reads a string field from env.Metadata, treating nil
// maps and missing keys as the empty string.
func metadataString(env gomsg.Envelope, key string) string {
	if env.Metadata == nil {
		return ""
	}
	return env.Metadata[key]
}

// logNoop is the single log point for routing misses. Keeps the format
// consistent so operators can grep for `[reactor] noop` to spot envelope
// kinds that V0 doesn't route. Per sprint-α D2, those are deliberate.
func logNoop(env gomsg.Envelope, reason string) {
	log.Printf("[reactor] noop env=%s kind=%s: %s", env.ID, env.Kind, reason)
}
