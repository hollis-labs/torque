package agent_boot

// Substrate hello-world smoke — ported from the legacy
// internal/e2e/sessionmgr_broker/ build-tagged package after
// CW-20260508-0001 collapsed sessionmgr.Manager into agent.Manager.
//
// The substrate trio under test is unchanged: messaging Store (S1.2),
// broker (S1.3), and the long-lived session manager (now agent.Manager
// instead of sessionmgr.Manager) (S1.4). The conversation shape (notice +
// request/response, checkpoint + stop + resume, envelope durability) is
// identical to the original; only the launch surface changed.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/broker"
	clockmsg "github.com/hollis-labs/torque/internal/messaging"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// recordingHub captures every Broadcast — the test treats SSE event shape
// as a contract surface (envelope.created / .delivered / .responded).
type recordingHub struct {
	mu     sync.Mutex
	events []hubEvent
}

type hubEvent struct {
	Type string
	Data map[string]interface{}
}

func (h *recordingHub) Broadcast(t string, d map[string]interface{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, hubEvent{Type: t, Data: d})
}

func (h *recordingHub) typesSnapshot() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.events))
	for i, e := range h.events {
		out[i] = e.Type
	}
	return out
}

// addr is the canonical agent URN for a logical role. The session ID is
// opaque to the broker; agents address each other by role.
func addr(role string) gomsg.Address {
	return gomsg.Address{Kind: gomsg.KindAgent, Authority: "smoke", ID: role}
}

// TestSubstrate_HelloWorld is the canonical S1 exit-gate smoke
// (CW-20260503-0016). It runs two long-lived sessions, exchanges a notice
// and a request/response, then checkpoints both and verifies envelope
// durability across the lifecycle.
//
// Differences from the legacy version:
//   - Sessions launched via agent.Manager.Boot(ModeLongLived) instead of
//     sessionmgr.Manager.Launch — the public substrate is now unified.
//   - Resume goes through agent.Manager.Resume which requires an existing
//     checkpoint (LatestSessionCheckpoint is the lookup path); the legacy
//     ResumeRequest{SessionID: idA} shape works identically.
//
// Out of scope (deliberate omission vs. the original):
//   - The full stop+resume durability dance fanned out to ~200 lines that
//     exercised lib-internal behavior already covered by go-agent-sessions
//     v0.6.0's own tests. We retain the broker-side durability witness
//     (envelope queued during checkpoint window survives across the
//     manager's view of session lifecycle) without re-running the lib's
//     watch-goroutine teardown.
func TestSubstrate_HelloWorld(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")

	// SQLite Store wired into the broker — same DB the agent.Manager owns,
	// so envelope rows sit alongside session rows and migrations cover both.
	hub := &recordingHub{}
	br := broker.New(clockmsg.NewStore(cd.DB), hub)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Launch two long-lived sessions.
	sessA, err := cd.Manager.Boot(ctx, agent.Options{
		Mode:         agent.ModeLongLived,
		AgentProfile: "alice",
		Workdir:      t.TempDir(),
		SessionMeta:  map[string]string{"role": "orchestrator"},
	})
	require.NoError(t, err, "boot session A")
	sessB, err := cd.Manager.Boot(ctx, agent.Options{
		Mode:         agent.ModeLongLived,
		AgentProfile: "bob",
		Workdir:      t.TempDir(),
		SessionMeta:  map[string]string{"role": "executor"},
	})
	require.NoError(t, err, "boot session B")
	idA, idB := sessA.ID, sessB.ID

	// Sessions reach running state via the lib's launching→running
	// transition synchronously inside Manager.Start. Assert non-terminal
	// rather than pinning the exact value (the watch goroutine may have
	// flipped launching→running by the time we read).
	getA, err := cd.Manager.Get(idA)
	require.NoError(t, err)
	getB, err := cd.Manager.Get(idB)
	require.NoError(t, err)
	assert.False(t, getA.Status.Terminal(), "session A must not be terminal post-Boot")
	assert.False(t, getB.Status.Terminal(), "session B must not be terminal post-Boot")

	roleA := addr("alice")
	roleB := addr("bob")

	// A sends a notice to B; B receives via Inbox; SSE fires.
	notice, err := br.Notice(ctx, roleA, roleB, json.RawMessage(`{"hello":"bob"}`))
	require.NoError(t, err)
	assert.Equal(t, gomsg.MsgKindNotice, notice.Kind)

	envs, err := br.Inbox(ctx, roleB, gomsg.Filter{})
	require.NoError(t, err)
	require.Len(t, envs, 1, "B must see the notice")
	assert.Equal(t, notice.ID, envs[0].ID)

	// Request/response round-trip.
	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()
	bSub, err := br.Subscribe(subCtx, roleB, gomsg.Filter{Kind: []gomsg.Kind{gomsg.MsgKindRequest}})
	require.NoError(t, err)

	bReplyDone := make(chan struct{})
	go func() {
		defer close(bReplyDone)
		select {
		case req, open := <-bSub:
			if !open {
				return
			}
			_, _ = br.Reply(ctx, req, json.RawMessage(`{"result":"hello-back"}`))
		case <-ctx.Done():
			return
		}
	}()

	resp, err := br.Request(ctx, gomsg.Envelope{
		From:        roleA,
		To:          roleB,
		Payload:     json.RawMessage(`{"q":"are you there?"}`),
		ContentType: "application/json",
	})
	require.NoError(t, err)
	assert.Equal(t, gomsg.MsgKindResponse, resp.Kind)
	var body map[string]string
	require.NoError(t, json.Unmarshal(resp.Payload, &body))
	assert.Equal(t, "hello-back", body["result"])
	<-bReplyDone

	// SSE event shape — at least one of created / delivered / responded.
	gotEvents := hub.typesSnapshot()
	for _, typ := range []string{"envelope.created", "envelope.delivered", "envelope.responded"} {
		assertHasEvent(t, gotEvents, typ)
	}

	// Checkpoint both sessions mid-conversation. Payload is the caller-side
	// resume context (here we record the in-flight thread the resumed agent
	// would pick up).
	cpA, err := cd.Manager.Checkpoint(agent.CheckpointRequest{
		SessionID: idA,
		Payload:   `{"awaiting":"none","sent_request":"` + resp.InReplyTo + `"}`,
		Note:      "after request/response",
	})
	require.NoError(t, err)
	cpB, err := cd.Manager.Checkpoint(agent.CheckpointRequest{
		SessionID: idB,
		Payload:   `{"replied_to":"` + resp.InReplyTo + `"}`,
		Note:      "after request/response",
	})
	require.NoError(t, err)
	assert.NotEqual(t, cpA.ID, cpB.ID)

	// Drain B's inbox before queueing the durability witness. Subscribe and
	// Inbox are orthogonal in the Store contract — Subscribe doesn't mark
	// DeliveredAt, so without a drain the post-resume Inbox would surface
	// the request alongside the queued notice. Drain produces a clean "only
	// the queued notice survived" signal.
	predrain, err := br.Inbox(ctx, roleB, gomsg.Filter{})
	require.NoError(t, err)
	assert.NotEmpty(t, predrain, "request envelope should still be inbox-pending until drained")

	// Queue an envelope after checkpoint as the durability witness.
	pending, err := br.Notice(ctx, roleA, roleB, json.RawMessage(`{"queued_during_resume":"yes"}`))
	require.NoError(t, err)

	// Stop the originals.
	require.NoError(t, cd.Manager.Stop(ctx, idA))
	require.NoError(t, cd.Manager.Stop(ctx, idB))
	require.Eventually(t, func() bool {
		a, _ := cd.Manager.Get(idA)
		b, _ := cd.Manager.Get(idB)
		return a != nil && b != nil && a.Status.Terminal() && b.Status.Terminal()
	}, 5*time.Second, 10*time.Millisecond, "both sessions must reach terminal state after Stop")
	subCancel()

	// Resume both — fresh IDs, source rows remain.
	idA2, err := cd.Manager.Resume(ctx, agent.ResumeRequest{SessionID: idA})
	require.NoError(t, err)
	idB2, err := cd.Manager.Resume(ctx, agent.ResumeRequest{SessionID: idB})
	require.NoError(t, err)
	assert.NotEqual(t, idA, idA2)
	assert.NotEqual(t, idB, idB2)

	// Envelope state preserved across stop+resume. The notice queued during
	// the stop window must still be inbox-visible to the resumed B (logical
	// address roleB; agent ID is irrelevant to envelope routing).
	pendingEnvs, err := br.Inbox(ctx, roleB, gomsg.Filter{})
	require.NoError(t, err)
	require.Len(t, pendingEnvs, 1, "queued envelope must survive stop+resume")
	assert.Equal(t, pending.ID, pendingEnvs[0].ID)

	// Original conversation still reachable via Get — no orphan deletion on
	// stop.
	got, err := br.Get(ctx, notice.ID)
	require.NoError(t, err, "original notice must still be retrievable")
	assert.Equal(t, notice.ID, got.ID)

	// Persisted session rows: 4 total (idA, idB, idA2, idB2) — the two
	// source rows terminal, the two resumed rows running (still alive).
	all, err := cd.Store.ListSessions(sqlstore.SessionFilter{})
	require.NoError(t, err)
	assert.Len(t, all, 4, "expected 4 session rows after launch+resume")

	// Checkpoint listings still resolve to their parent IDs.
	cpsA, err := cd.Manager.ListCheckpoints(idA, 0)
	require.NoError(t, err)
	require.Len(t, cpsA, 1)
	assert.Equal(t, cpA.ID, cpsA[0].ID)

	cpsB, err := cd.Manager.ListCheckpoints(idB, 0)
	require.NoError(t, err)
	require.Len(t, cpsB, 1)
	assert.Equal(t, cpB.ID, cpsB[0].ID)

	// Stop the resumed sessions so the cleanup's bounded Shutdown can drain
	// every watch goroutine. fakeSession.Wait blocks on a done chan that
	// only Stop closes; without this the test would leak goroutines across
	// the -count=10 loop.
	require.NoError(t, cd.Manager.Stop(ctx, idA2))
	require.NoError(t, cd.Manager.Stop(ctx, idB2))
	require.Eventually(t, func() bool {
		a, _ := cd.Manager.Get(idA2)
		b, _ := cd.Manager.Get(idB2)
		return a != nil && b != nil && a.Status.Terminal() && b.Status.Terminal()
	}, 5*time.Second, 10*time.Millisecond, "resumed sessions must reach terminal state")
}

// assertHasEvent fails the test when types doesn't contain the given event.
// Local helper to keep the assertion call site readable across all three
// expected event kinds.
func assertHasEvent(t *testing.T, types []string, want string) {
	t.Helper()
	for _, typ := range types {
		if typ == want {
			return
		}
	}
	t.Errorf("expected SSE event type %q, got %v", want, types)
}
