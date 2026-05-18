package bootstrap

import (
	"context"
	"fmt"

	gomsg "github.com/hollis-labs/go-messaging"

	"github.com/hollis-labs/torque/internal/broker"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/runtime/steering"
)

// SteeringBridge wires the steering bridge (CW-20260518-0041, messaging
// epic A) into the running daemon. It builds the agent.Manager adapter
// that resolves recipient addresses to live sessions and delivers turns,
// constructs the Bridge, and starts a Loop subscribed system-wide on the
// broker's envelope stream.
//
// The Loop runs ALONGSIDE the reactor Loop — both subscribe on a zero
// Address; the messaging Store fans each a private copy of every
// envelope. They act on disjoint envelopes: the reactor routes by Kind
// (escalation/status_update/request/handoff), the bridge by recipient
// Address (envelopes addressed to a live agent/session).
//
// Returns (closer, error). closer is always non-nil — call at shutdown to
// drain the loop goroutine. On error the bridge was not started.
func SteeringBridge(
	ctx context.Context,
	brk *broker.Broker,
	sessions *agent.Manager,
) (func(), error) {
	if brk == nil {
		return func() {}, fmt.Errorf("steering bridge bootstrap: broker is required")
	}

	bridge := steering.New(managerGateway{mgr: sessions}, brk)
	// System-wide subscription (zero Address, empty Filter): the Bridge
	// itself encodes the steerable-address rule, so the loop sees every
	// envelope and lets non-steerable ones fall through cheaply.
	loop := steering.NewLoop(brk, bridge, gomsg.Address{}, gomsg.Filter{})
	if err := loop.Start(ctx); err != nil {
		return func() {}, fmt.Errorf("steering bridge loop start: %w", err)
	}
	return loop.Close, nil
}

// managerGateway bridges steering.SessionGateway to agent.Manager. A nil
// manager is tolerated — every resolution misses, so the bridge degrades
// to "no live session" for every envelope (test composition roots that
// did not stand up the agent manager).
type managerGateway struct {
	mgr *agent.Manager
}

// LiveSession resolves a recipient address to a running session ID.
//
// Addressing convention:
//   - msg://session/<authority>/<id> — id is the Torque session ID; the
//     session must exist and be in the running state.
//   - msg://agent/<authority>/<id>   — id is the orchestrator's task ID;
//     resolves to that task's running session, if any.
//
// Any other address kind, a terminal/absent session, or a lookup error
// yields ok=false — the bridge then leaves the envelope for durable
// Inbox pickup.
func (g managerGateway) LiveSession(addr gomsg.Address) (string, bool) {
	if g.mgr == nil {
		return "", false
	}
	switch addr.Kind {
	case gomsg.KindSession:
		sess, err := g.mgr.Get(addr.ID)
		if err != nil || sess == nil || sess.Status != agent.StatusRunning {
			return "", false
		}
		return sess.ID, true
	case gomsg.KindAgent:
		// An agent address targets the orchestrator by its task ID; the
		// live session is the running session for that task.
		live, err := g.mgr.List(agent.StatusRunning, addr.ID, "", 1)
		if err != nil || len(live) == 0 {
			return "", false
		}
		return live[0].ID, true
	default:
		return "", false
	}
}

// SteerTurn injects text into the running session as its next turn,
// routing through agent.Manager.SendTurn so the turn is framed per the
// session's RuntimeKind (streaming-stdio / jsonrpc-stdio / subprocess).
func (g managerGateway) SteerTurn(ctx context.Context, sessionID, text string) error {
	if g.mgr == nil {
		return fmt.Errorf("steering gateway: agent manager not wired")
	}
	sess, err := g.mgr.Get(sessionID)
	if err != nil {
		return fmt.Errorf("steering gateway: resolve session %s: %w", sessionID, err)
	}
	return g.mgr.SendTurn(ctx, sess, text)
}
