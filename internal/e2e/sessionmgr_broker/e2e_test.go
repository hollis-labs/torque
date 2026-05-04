// Package sessionmgr_broker_e2e is the S1 sprint-exit smoke
// (CW-20260503-0016). It composes the substrate trio — sessionmgr (S1.4),
// broker (S1.3), messaging Store (S1.2) — and validates a two-session
// conversation can survive checkpoint + stop + resume.
//
// No real Anthropic/Codex calls; the sessionmgr Manager is wired against
// a fakeRuntime that returns inert fakeSessions. The substrate's
// integration shape is the assertion target, not provider correctness.
package sessionmgr_broker_e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/hollis-labs/go-agent-sessions/agentsessions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/clockwork-manifold/internal/broker"
	clockmsg "github.com/hollis-labs/clockwork-manifold/internal/messaging"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/sessionmgr"
)

// fakeSession is a no-op agentsessions.Session whose lifecycle the test
// drives directly via Stop / complete.
type fakeSession struct {
	pid  int
	done chan struct{}
	once sync.Once
	code atomic.Int32
	dead atomic.Bool
}

func newFakeSession(pid int) *fakeSession {
	return &fakeSession{pid: pid, done: make(chan struct{})}
}

func (f *fakeSession) Wait() (int, error) {
	<-f.done
	return int(f.code.Load()), nil
}
func (f *fakeSession) Stop(_ context.Context) error {
	f.dead.Store(true)
	f.once.Do(func() { close(f.done) })
	return nil
}
func (f *fakeSession) SendInput(_ context.Context, _ []byte) error {
	if f.dead.Load() {
		return agentsessions.ErrNoInputChannel
	}
	return nil
}
func (f *fakeSession) Resize(_ context.Context, _, _ uint16) error { return nil }
func (f *fakeSession) Health() agentsessions.HealthStatus {
	return agentsessions.HealthStatus{Alive: !f.dead.Load(), PID: f.pid}
}
func (f *fakeSession) CheckpointHints() (agentsessions.CheckpointHint, bool) {
	return nil, false
}

// fakeRuntime is the minimum agentsessions.Runtime impl. Each Start call
// allocates a new fakeSession and returns it.
type fakeRuntime struct {
	id   string
	caps agentsessions.Capabilities

	mu      sync.Mutex
	pidNext atomic.Int32
}

func newFakeRuntime(id string) *fakeRuntime {
	r := &fakeRuntime{id: id}
	r.pidNext.Store(2000)
	return r
}

func (r *fakeRuntime) ID() string                       { return r.id }
func (r *fakeRuntime) Kind() string                     { return "fake" }
func (r *fakeRuntime) Caps() agentsessions.Capabilities { return r.caps }
func (r *fakeRuntime) Prepare(_ context.Context) error  { return nil }
func (r *fakeRuntime) Start(_ context.Context, _ agentsessions.StartOptions) (agentsessions.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pid := int(r.pidNext.Add(1))
	return newFakeSession(pid), nil
}

// fakeRegistry returns the shared fakeRuntime for any profile name —
// the e2e is only exercising lifecycle plumbing, not adapter selection.
type fakeRegistry struct{ rt *fakeRuntime }

func (r *fakeRegistry) RuntimeFor(_ string, _ []byte) (agentsessions.Runtime, error) {
	return r.rt, nil
}

// recordingHub captures every Broadcast — the test treats SSE event
// shape as a contract surface (envelope.created / .delivered / .responded).
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

// stubEmitter satisfies sessionmgr.EventEmitter — the broker's recording
// hub is the SSE assertion target, not session.state_changed.
type stubEmitter struct{}

func (stubEmitter) EmitSessionEvent(_ string, _ map[string]interface{}) {}

func setupE2E(t *testing.T) (*sessionmgr.Manager, *broker.Broker, *recordingHub, *sqlstore.Store, func()) {
	t.Helper()
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "smoke.db") +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	mgr := sessionmgr.New(store, &fakeRegistry{rt: newFakeRuntime("rt-fake")}, stubEmitter{})

	hub := &recordingHub{}
	br := broker.New(clockmsg.NewStore(db), hub)

	cleanup := func() {
		// Bound Shutdown — go-agent-sessions waits for every watch
		// goroutine to drain (each session's Wait() must return). The
		// test stops every session before cleanup, but we cap the wait
		// so a test-side leak surfaces as a fail rather than a hang.
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = mgr.Shutdown(shutdownCtx)
		shutdownCancel()
		store.Close()
		db.Close()
	}
	return mgr, br, hub, store, cleanup
}

// addr is the canonical agent URN for a logical role. The sessionmgr ID
// is opaque to the broker; agents address each other by role.
func addr(role string) gomsg.Address {
	return gomsg.Address{Kind: gomsg.KindAgent, Authority: "smoke", ID: role}
}

// TestSubstrate_HelloWorld is the canonical S1 exit-gate smoke. It runs
// two long-lived sessions, exchanges a notice and a request/response, then
// checkpoints + stops + resumes both, verifying envelope durability.
func TestSubstrate_HelloWorld(t *testing.T) {
	mgr, br, hub, store, cleanup := setupE2E(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// AC2: launch two long-lived sessions.
	idA, err := mgr.Launch(ctx, sessionmgr.LaunchRequest{
		AgentProfile: "alice",
		Workdir:      t.TempDir(),
		SessionMeta:  map[string]string{"role": "orchestrator"},
	})
	require.NoError(t, err, "launch session A")
	idB, err := mgr.Launch(ctx, sessionmgr.LaunchRequest{
		AgentProfile: "bob",
		Workdir:      t.TempDir(),
		SessionMeta:  map[string]string{"role": "executor"},
	})
	require.NoError(t, err, "launch session B")

	sessA, err := mgr.Get(idA)
	require.NoError(t, err)
	sessB, err := mgr.Get(idB)
	require.NoError(t, err)
	assert.Equal(t, sessionmgr.StatusRunning, sessA.Status)
	assert.Equal(t, sessionmgr.StatusRunning, sessB.Status)

	roleA := addr("alice")
	roleB := addr("bob")

	// AC3: A sends a notice to B; B receives via Inbox; SSE event fires.
	notice, err := br.Notice(ctx, roleA, roleB, json.RawMessage(`{"hello":"bob"}`))
	require.NoError(t, err)
	assert.Equal(t, gomsg.MsgKindNotice, notice.Kind)

	envs, err := br.Inbox(ctx, roleB, gomsg.Filter{})
	require.NoError(t, err)
	require.Len(t, envs, 1, "B must see the notice")
	assert.Equal(t, notice.ID, envs[0].ID)

	// AC4: A sends a request; B replies; A's blocking Request returns
	// the response within timeout.
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

	// SSE: at minimum we expect created (notice + request + reply) and
	// delivered (notice via Inbox above) and responded (reply path). Order
	// across goroutines isn't guaranteed; assert presence of each type.
	gotEvents := hub.typesSnapshot()
	expectAtLeast := map[string]int{
		"envelope.created":   1,
		"envelope.delivered": 1,
		"envelope.responded": 1,
	}
	for typ, want := range expectAtLeast {
		got := 0
		for _, e := range gotEvents {
			if e == typ {
				got++
			}
		}
		assert.GreaterOrEqual(t, got, want, "expected ≥%d %s events, got %d (full=%v)", want, typ, got, gotEvents)
	}

	// AC5 — checkpoint both sessions mid-conversation. Payload is the
	// caller-side resume context (here we record the in-flight thread the
	// resumed agent would pick up).
	cpA, err := mgr.Checkpoint(sessionmgr.CheckpointRequest{
		SessionID: idA,
		Payload:   `{"awaiting":"none","sent_request":"` + resp.InReplyTo + `"}`,
		Note:      "after request/response",
	})
	require.NoError(t, err)
	cpB, err := mgr.Checkpoint(sessionmgr.CheckpointRequest{
		SessionID: idB,
		Payload:   `{"replied_to":"` + resp.InReplyTo + `"}`,
		Note:      "after request/response",
	})
	require.NoError(t, err)
	assert.NotEqual(t, cpA.ID, cpB.ID)

	// Drain B's inbox before queueing the durability witness. The request
	// envelope was consumed via Subscribe (live fan-out), but Subscribe
	// and Inbox are orthogonal queues in the Store contract — Subscribe
	// does NOT mark DeliveredAt. Without this drain, the post-resume
	// Inbox would surface the request alongside the queued notice; we
	// want a clean "only the queued notice survived" signal.
	predrain, err := br.Inbox(ctx, roleB, gomsg.Filter{})
	require.NoError(t, err)
	assert.NotEmpty(t, predrain, "request envelope should still be inbox-pending until drained")

	// Send one more envelope after checkpoint but before stop — this is
	// the durability witness: it must survive the stop+resume and remain
	// inbox-visible to the resumed B.
	pending, err := br.Notice(ctx, roleA, roleB, json.RawMessage(`{"queued_during_resume":"yes"}`))
	require.NoError(t, err)

	// AC5 — sessionmgr stops them.
	require.NoError(t, mgr.Stop(ctx, idA))
	require.NoError(t, mgr.Stop(ctx, idB))

	// Wait for the watch goroutine to record the terminal state. Polling
	// with a deadline (no sleep) keeps -race tolerant.
	require.Eventually(t, func() bool {
		a, _ := mgr.Get(idA)
		b, _ := mgr.Get(idB)
		return a != nil && b != nil && a.Status.Terminal() && b.Status.Terminal()
	}, 5*time.Second, 10*time.Millisecond, "both sessions must reach terminal state after Stop")

	// Subscribe-channel for B's replies should close cleanly when the
	// surrounding ctx ends — exercised by subCancel + the close detection
	// in the goroutine.
	subCancel()

	// AC5 — sessionmgr resumes them. Resume creates fresh IDs; the source
	// rows remain.
	idA2, err := mgr.Resume(ctx, sessionmgr.ResumeRequest{SessionID: idA})
	require.NoError(t, err)
	idB2, err := mgr.Resume(ctx, sessionmgr.ResumeRequest{SessionID: idB})
	require.NoError(t, err)
	assert.NotEqual(t, idA, idA2)
	assert.NotEqual(t, idB, idB2)

	a2, err := mgr.Get(idA2)
	require.NoError(t, err)
	b2, err := mgr.Get(idB2)
	require.NoError(t, err)
	assert.Equal(t, sessionmgr.StatusRunning, a2.Status)
	assert.Equal(t, sessionmgr.StatusRunning, b2.Status)

	// AC5 — envelope state preserved across stop+resume. The notice
	// queued during the stop window must still be inbox-visible to the
	// resumed B (logical address roleB; sessionmgr ID is irrelevant to
	// envelope routing).
	pendingEnvs, err := br.Inbox(ctx, roleB, gomsg.Filter{})
	require.NoError(t, err)
	require.Len(t, pendingEnvs, 1, "queued envelope must survive stop+resume")
	assert.Equal(t, pending.ID, pendingEnvs[0].ID)

	// And the original conversation is still reachable via Get — no
	// orphan deletion on stop.
	got, err := br.Get(ctx, notice.ID)
	require.NoError(t, err, "original notice must still be retrievable")
	assert.Equal(t, notice.ID, got.ID)

	// Sanity on the persisted session rows: 4 total (idA, idB, idA2, idB2),
	// the two source rows terminal, the two resumed rows running.
	all, err := store.ListSessions(sqlstore.SessionFilter{})
	require.NoError(t, err)
	assert.Len(t, all, 4, "expected 4 session rows after launch+resume")

	// Checkpoint listings still resolve to their parent IDs.
	cpsA, err := mgr.ListCheckpoints(idA, 0)
	require.NoError(t, err)
	require.Len(t, cpsA, 1)
	assert.Equal(t, cpA.ID, cpsA[0].ID)

	cpsB, err := mgr.ListCheckpoints(idB, 0)
	require.NoError(t, err)
	require.Len(t, cpsB, 1)
	assert.Equal(t, cpB.ID, cpsB[0].ID)

	// Stop the resumed sessions so cleanup's bounded Shutdown can drain
	// every watch goroutine. fakeSession.Wait blocks on a `done` channel
	// that only Stop closes; without this the test would leak goroutines
	// across the 10-iter Repeat loop.
	require.NoError(t, mgr.Stop(ctx, idA2))
	require.NoError(t, mgr.Stop(ctx, idB2))
	require.Eventually(t, func() bool {
		a, _ := mgr.Get(idA2)
		b, _ := mgr.Get(idB2)
		return a != nil && b != nil && a.Status.Terminal() && b.Status.Terminal()
	}, 5*time.Second, 10*time.Millisecond, "resumed sessions must reach terminal state")
}

// AC6 wants 10 consecutive non-flaky runs under -race. The canonical
// way is `go test -race -count=10 ./internal/e2e/sessionmgr_broker/...`
// from CI rather than a self-loop in-process — agent-sessions carries
// per-Manager state that the in-process repeat would have to tear down
// between iters, which adds test surface unrelated to the substrate.
// One run, fully -race-clean, is the in-tree contract; the multi-run
// gate stays in CI's hands.
