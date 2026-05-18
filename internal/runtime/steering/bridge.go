// Package steering implements Torque's steering bridge (CW-20260518-0041,
// messaging epic A) — the link between the broker/envelope model and a
// running agent's turn loop.
//
// The problem it solves: the envelope model (internal/broker, the SQLite
// messaging Store) and live-session turn delivery (agent.Manager.SendTurn)
// were disconnected. A user could persist an envelope addressed to a
// running orchestrator, but nothing carried it into that orchestrator's
// loop. The steering bridge closes that gap.
//
// Design decision #1 (LOCKED 2026-05-18, torque-messaging-design.md):
// the default delivery mode is INJECT-AT-TURN-BOUNDARY — an inbound
// envelope addressed to a live session is delivered as that agent's next
// turn via SendTurn. An interrupt that pre-empts the current turn is
// deferred; opt-in mid-run inbox polling is a separate task
// (CW-20260518-0042). This package implements only the inject-at-turn
// default.
//
// Layering: steering sits ON TOP of internal/broker (envelope transport)
// and BESIDE internal/runtime/reactor (the kind-driven action router).
// The two subscribe to the broker independently and handle a strictly
// disjoint set of envelopes: the reactor acts on escalation /
// status_update / request / handoff (HITL checkpoint, pause+block, peer
// fanout, reassignment); the steering bridge acts on notice / response
// addressed to a live agent/session (turn injection). An envelope is
// therefore never double-handled — see Steerable.
package steering

import (
	"context"
	"log"

	gomsg "github.com/hollis-labs/go-messaging"
)

// SessionGateway resolves a steering envelope's recipient address to a
// live agent session and delivers turn text into it. Production wires an
// adapter over agent.Manager (see internal/runtime/bootstrap/steering.go);
// tests pass a fake.
//
// Kept as a narrow interface so this package does not import the agent
// runtime — the bridge stays unit-testable without standing up a session
// manager, mirroring the reactor.Deps adapter pattern.
type SessionGateway interface {
	// LiveSession resolves a recipient address to the ID of a RUNNING
	// session. ok is false when the address targets no live session
	// (terminal, never booted, or an address kind the gateway does not
	// route) — the bridge then leaves the envelope for durable Inbox
	// pickup rather than dropping it.
	LiveSession(addr gomsg.Address) (sessionID string, ok bool)

	// SteerTurn delivers text into the running session identified by
	// sessionID as its next turn. Production routes through
	// agent.Manager.SendTurn, which frames the turn per the session's
	// RuntimeKind.
	SteerTurn(ctx context.Context, sessionID, text string) error
}

// EnvelopeConsumer is the optional surface the bridge uses to mark an
// envelope consumed after a successful turn delivery. broker.Broker
// satisfies it. Pass nil to skip lifecycle marking (test paths).
//
// Marking consumed is load-bearing for de-duplication: the messaging
// Store's Consume inserts a delivery row, and Inbox excludes any envelope
// that already has a delivery row for the recipient. So a bridge-steered
// envelope will NOT be re-delivered if the agent later opts into inbox
// polling (CW-20260518-0042).
type EnvelopeConsumer interface {
	Consume(ctx context.Context, id string, recipient gomsg.Address) error
}

// Outcome is the symbolic result of a Deliver call — for observability
// and test assertions.
type Outcome string

const (
	// OutcomeNotAddressed: the envelope's recipient is not an agent or
	// session address, so the bridge does not handle it. No side effect.
	OutcomeNotAddressed Outcome = "not_addressed"

	// OutcomeNoLiveSession: the recipient is a steerable address but no
	// running session backs it. The envelope is left in the store for
	// durable Inbox pickup. No error — a steering message sent to an
	// idle/terminal agent is a normal race, not a failure.
	OutcomeNoLiveSession Outcome = "no_live_session"

	// OutcomeDelivered: the envelope was rendered and injected into the
	// live session as its next turn, and (if a consumer is wired) marked
	// consumed.
	OutcomeDelivered Outcome = "delivered"

	// OutcomeFailed: a live session was resolved but SteerTurn returned
	// an error. Result.Err carries it; the envelope is left unconsumed so
	// a retry path (operator resend, future catch-up) can pick it up.
	OutcomeFailed Outcome = "failed"
)

// DeliveryResult describes a single Deliver call. Side effects (the turn
// injection, the consume marking) have already happened by the time the
// caller sees the result.
type DeliveryResult struct {
	Outcome    Outcome
	EnvelopeID string
	SessionID  string // resolved live session; empty unless one was found
	Err        error  // non-nil only for OutcomeFailed
}

// Bridge carries steering envelopes into live agent loops. Construct via
// New; call Deliver per envelope (or run a Loop, which calls Deliver for
// every envelope on a broker subscription).
//
// Safe for concurrent use — the Bridge holds no mutable state; ordering,
// when it matters, is the caller's job (the V0 Loop is single-goroutine).
type Bridge struct {
	gw       SessionGateway
	consumer EnvelopeConsumer
}

// New returns a Bridge wired to gw. consumer may be nil — the bridge then
// skips consumed-marking and logs that the lifecycle write was skipped.
func New(gw SessionGateway, consumer EnvelopeConsumer) *Bridge {
	return &Bridge{gw: gw, consumer: consumer}
}

// steerableKinds is the closed set of envelope kinds the bridge injects
// as turns. It is deliberately the COMPLEMENT of the reactor's V0 routed
// kinds (escalation / status_update / request / handoff): those four have
// specific reactor semantics — a HITL checkpoint, a pause+block, a peer
// fanout, a reassignment — that must win over turn injection even when
// the envelope happens to be addressed to a live session. notice and
// response are the steering-relevant kinds: a user nudging an
// orchestrator, or a peer relaying an answer. Keeping the two routers
// disjoint by kind means an envelope is never double-handled.
var steerableKinds = map[gomsg.Kind]bool{
	gomsg.MsgKindNotice:   true,
	gomsg.MsgKindResponse: true,
}

// Steerable reports whether env is a steering envelope — a notice or
// response addressed to an agent or a session. The two axes:
//   - Address: the recipient must be a live-agent-addressable kind
//     (agent or session); the reactor routes by Kind, the bridge by
//     recipient.
//   - Kind: restricted to notice/response so the bridge never competes
//     with the reactor's escalation/status_update/request/handoff
//     handling (see steerableKinds).
func Steerable(env gomsg.Envelope) bool {
	if !steerableKinds[env.Kind] {
		return false
	}
	switch env.To.Kind {
	case gomsg.KindAgent, gomsg.KindSession:
		return true
	default:
		return false
	}
}

// Deliver carries one envelope into a live agent loop. It is total — every
// envelope produces a DeliveryResult, never a panic — so a Loop can call
// it for every envelope on a system-wide subscription without pre-filtering.
//
// The flow: skip non-steerable envelopes; resolve the recipient to a live
// session (a miss is a normal no-op, not an error); render the envelope to
// turn text; inject it via the gateway's SteerTurn; mark the envelope
// consumed. A SteerTurn failure leaves the envelope unconsumed.
func (b *Bridge) Deliver(ctx context.Context, env gomsg.Envelope) DeliveryResult {
	res := DeliveryResult{Outcome: OutcomeNotAddressed, EnvelopeID: env.ID}
	if !Steerable(env) {
		return res
	}

	sessionID, ok := b.gw.LiveSession(env.To)
	if !ok {
		// Not a failure: a steering message can race ahead of an agent
		// booting, or arrive after it exited. The envelope stays durable
		// in the store; an opt-in inbox poll (CW-20260518-0042) or a
		// future catch-up pass can still pick it up.
		log.Printf("[steering] no live session for env=%s to=%s — left for durable pickup",
			env.ID, env.To.URN())
		res.Outcome = OutcomeNoLiveSession
		return res
	}
	res.SessionID = sessionID

	text := RenderTurn(env)
	if err := b.gw.SteerTurn(ctx, sessionID, text); err != nil {
		log.Printf("[steering] steer-turn failed env=%s session=%s: %v", env.ID, sessionID, err)
		res.Outcome = OutcomeFailed
		res.Err = err
		return res
	}
	res.Outcome = OutcomeDelivered

	// Mark consumed so the envelope reflects "carried into the loop" and
	// is excluded from any later Inbox drain. Best-effort: a consume
	// failure does not undo the (successful) turn delivery — we log and
	// keep the OutcomeDelivered result.
	if b.consumer != nil {
		if err := b.consumer.Consume(ctx, env.ID, env.To); err != nil {
			log.Printf("[steering] consume marking failed env=%s: %v", env.ID, err)
		}
	} else {
		log.Printf("[steering] no consumer wired — env=%s delivered but not marked consumed", env.ID)
	}

	log.Printf("[steering] delivered env=%s kind=%s -> session=%s", env.ID, env.Kind, sessionID)
	return res
}
