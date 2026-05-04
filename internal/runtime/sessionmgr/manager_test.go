package sessionmgr_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/sessionmgr"
	"github.com/hollis-labs/go-agent-sessions/agentsessions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// fakeSession satisfies agentsessions.Session for tests. complete(code)
// unblocks Wait; killed → Stop record; alive controls Health.Alive.
type fakeSession struct {
	pid    int
	done   chan struct{}
	once   sync.Once
	code   atomic.Int32
	killed atomic.Bool
}

func newFakeSession(pid int) *fakeSession {
	return &fakeSession{pid: pid, done: make(chan struct{})}
}

func (f *fakeSession) Wait() (int, error) {
	<-f.done
	return int(f.code.Load()), nil
}
func (f *fakeSession) Stop(_ context.Context) error {
	f.killed.Store(true)
	f.once.Do(func() {
		f.code.Store(0)
		close(f.done)
	})
	return nil
}
func (f *fakeSession) SendInput(_ context.Context, _ []byte) error {
	if f.killed.Load() {
		return agentsessions.ErrNoInputChannel
	}
	return nil
}
func (f *fakeSession) Resize(_ context.Context, _, _ uint16) error { return nil }
func (f *fakeSession) Health() agentsessions.HealthStatus {
	return agentsessions.HealthStatus{Alive: !f.killed.Load(), PID: f.pid}
}
func (f *fakeSession) CheckpointHints() (agentsessions.CheckpointHint, bool) {
	return nil, false
}
func (f *fakeSession) complete(code int) {
	f.once.Do(func() {
		f.code.Store(int32(code))
		close(f.done)
	})
}

// fakeRuntime is an agentsessions.Runtime that produces fakeSessions and
// records every Start.
type fakeRuntime struct {
	id      string
	caps    agentsessions.Capabilities
	mu      sync.Mutex
	last    *fakeSession
	nextPID atomic.Int32
	startErr error
}

func newFakeRuntime(id string) *fakeRuntime {
	r := &fakeRuntime{id: id}
	r.nextPID.Store(1000)
	return r
}

func (r *fakeRuntime) ID() string                                  { return r.id }
func (r *fakeRuntime) Kind() string                                { return "fake" }
func (r *fakeRuntime) Caps() agentsessions.Capabilities            { return r.caps }
func (r *fakeRuntime) Prepare(_ context.Context) error             { return nil }
func (r *fakeRuntime) Start(_ context.Context, opts agentsessions.StartOptions) (agentsessions.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.startErr != nil {
		return nil, r.startErr
	}
	pid := int(r.nextPID.Add(1))
	s := newFakeSession(pid)
	r.last = s
	_ = opts
	return s, nil
}
func (r *fakeRuntime) lastSession() *fakeSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

// fakeRegistry returns the same fakeRuntime every time, keyed by the
// supplied profile name.
type fakeRegistry struct {
	rt          *fakeRuntime
	resolveErr  error
}

func (r *fakeRegistry) RuntimeFor(profile string, _ []byte) (agentsessions.Runtime, error) {
	if r.resolveErr != nil {
		return nil, r.resolveErr
	}
	return r.rt, nil
}

// recordingEmitter captures every emitted event.
type recordingEmitter struct {
	mu sync.Mutex
	ev []map[string]interface{}
}

func (e *recordingEmitter) EmitSessionEvent(typ string, data map[string]interface{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := make(map[string]interface{}, len(data)+1)
	for k, v := range data {
		d[k] = v
	}
	d["__type"] = typ
	e.ev = append(e.ev, d)
}
func (e *recordingEmitter) snapshot() []map[string]interface{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]map[string]interface{}, len(e.ev))
	copy(out, e.ev)
	return out
}

func newTestStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	// Pin MaxOpenConns to 1 so concurrent goroutines share a single
	// in-memory DB. modernc/sqlite opens a fresh DB per connection when
	// the DSN is :memory:; serializing the pool sidesteps it without
	// dragging tests onto a temp file.
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.True(t, cond(), "condition never satisfied within %s", timeout)
}

func TestManager_LaunchAndWait(t *testing.T) {
	store := newTestStore(t)
	rt := newFakeRuntime("test-runtime")
	reg := &fakeRegistry{rt: rt}
	emitter := &recordingEmitter{}
	mgr := sessionmgr.New(store, reg, emitter)

	id, err := mgr.Launch(context.Background(), sessionmgr.LaunchRequest{
		AgentProfile: "default",
		Workdir:      "/tmp",
		ProjectID:    "PRJ-1",
		TaskID:       "T-1",
	})
	require.NoError(t, err)
	require.NotEmpty(t, id)

	got, err := mgr.Get(id)
	require.NoError(t, err)
	assert.Equal(t, sessionmgr.StatusRunning, got.Status)
	assert.Equal(t, "PRJ-1", got.ProjectID)
	assert.Equal(t, "T-1", got.TaskID)

	// Drive the session to terminal state.
	rt.lastSession().complete(0)

	code, err := mgr.Wait(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, 0, code)

	waitFor(t, 2*time.Second, func() bool {
		s, err := mgr.Get(id)
		return err == nil && s.Status == sessionmgr.StatusDone
	})

	// Lifecycle events: at least created (launching) and the final
	// state_changed.
	events := emitter.snapshot()
	require.NotEmpty(t, events)
	var sawStateChanged bool
	for _, e := range events {
		if e["__type"] == "session.state_changed" {
			sawStateChanged = true
		}
	}
	assert.True(t, sawStateChanged, "expected session.state_changed emission")
}

func TestManager_StopMarksDone(t *testing.T) {
	store := newTestStore(t)
	rt := newFakeRuntime("rt")
	mgr := sessionmgr.New(store, &fakeRegistry{rt: rt}, nil)

	id, err := mgr.Launch(context.Background(), sessionmgr.LaunchRequest{
		AgentProfile: "default", Workdir: "/tmp",
	})
	require.NoError(t, err)

	require.NoError(t, mgr.Stop(context.Background(), id))

	code, err := mgr.Wait(context.Background(), id)
	require.NoError(t, err)
	// When the manager Stops the inner session, watch records StateDone
	// (killing flag set).
	assert.Equal(t, 0, code)

	waitFor(t, 2*time.Second, func() bool {
		s, err := mgr.Get(id)
		return err == nil && s.Status.Terminal()
	})

	got, err := mgr.Get(id)
	require.NoError(t, err)
	assert.Equal(t, sessionmgr.StatusDone, got.Status)
}

func TestManager_CheckpointAndResume(t *testing.T) {
	store := newTestStore(t)
	rt := newFakeRuntime("rt")
	mgr := sessionmgr.New(store, &fakeRegistry{rt: rt}, nil)

	id, err := mgr.Launch(context.Background(), sessionmgr.LaunchRequest{
		AgentProfile: "default", Workdir: "/tmp",
	})
	require.NoError(t, err)

	cp, err := mgr.Checkpoint(sessionmgr.CheckpointRequest{
		SessionID: id,
		Payload:   `{"step":42}`,
		Note:      "mid-run",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, cp.ID)
	assert.Equal(t, id, cp.SessionID)

	cps, err := mgr.ListCheckpoints(id, 0)
	require.NoError(t, err)
	assert.Len(t, cps, 1)
	assert.Equal(t, `{"step":42}`, cps[0].Payload)

	rt.lastSession().complete(0)
	_, err = mgr.Wait(context.Background(), id)
	require.NoError(t, err)

	waitFor(t, 2*time.Second, func() bool {
		s, err := mgr.Get(id)
		return err == nil && s.Status.Terminal()
	})

	// Resume should spawn a new session id, copy core fields, and pick
	// the latest checkpoint.
	newID, err := mgr.Resume(context.Background(), sessionmgr.ResumeRequest{
		SessionID: id,
	})
	require.NoError(t, err)
	assert.NotEqual(t, id, newID)

	resumed, err := mgr.Get(newID)
	require.NoError(t, err)
	assert.Equal(t, sessionmgr.StatusRunning, resumed.Status)

	rt.lastSession().complete(0)
	_, err = mgr.Wait(context.Background(), newID)
	require.NoError(t, err)
}

func TestManager_ResumeWithoutCheckpoint(t *testing.T) {
	store := newTestStore(t)
	rt := newFakeRuntime("rt")
	mgr := sessionmgr.New(store, &fakeRegistry{rt: rt}, nil)

	id, err := mgr.Launch(context.Background(), sessionmgr.LaunchRequest{
		AgentProfile: "default", Workdir: "/tmp",
	})
	require.NoError(t, err)

	_, err = mgr.Resume(context.Background(), sessionmgr.ResumeRequest{SessionID: id})
	require.ErrorIs(t, err, sessionmgr.ErrNoCheckpoint)

	rt.lastSession().complete(0)
}

func TestManager_ResumeUnknownSession(t *testing.T) {
	store := newTestStore(t)
	mgr := sessionmgr.New(store, &fakeRegistry{rt: newFakeRuntime("rt")}, nil)
	_, err := mgr.Resume(context.Background(), sessionmgr.ResumeRequest{SessionID: "missing"})
	require.ErrorIs(t, err, sessionmgr.ErrSessionNotFound)
}

func TestManager_ConcurrentLaunches(t *testing.T) {
	store := newTestStore(t)
	rt := newFakeRuntime("rt")
	mgr := sessionmgr.New(store, &fakeRegistry{rt: rt}, nil)

	const n = 16
	var wg sync.WaitGroup
	ids := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, err := mgr.Launch(context.Background(), sessionmgr.LaunchRequest{
				AgentProfile: "default",
				Workdir:      "/tmp",
				TaskID:       fmt.Sprintf("T-%d", i),
			})
			ids[i] = id
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "launch %d", i)
		assert.NotEmpty(t, ids[i], "launch %d id", i)
	}

	// IDs must be unique.
	seen := map[string]struct{}{}
	for _, id := range ids {
		_, dup := seen[id]
		require.False(t, dup, "duplicate session id %s", id)
		seen[id] = struct{}{}
	}

	// All n rows persisted.
	listed, err := mgr.List("", "", "", 0)
	require.NoError(t, err)
	assert.Len(t, listed, n)

	// Drain the in-flight sessions so Shutdown doesn't deadlock. Inner
	// manager keeps the most recent fakeSession; iterate by stopping each
	// id explicitly.
	for _, id := range ids {
		_ = mgr.Stop(context.Background(), id)
	}
}

func TestManager_OrphanSweepMarksCrashed(t *testing.T) {
	store := newTestStore(t)
	rt := newFakeRuntime("rt")
	mgr := sessionmgr.New(store, &fakeRegistry{rt: rt}, nil)

	// Insert a row directly that mimics a daemon-crashed `running` session
	// with a PID that's definitely gone (PID 0 is treated as stale).
	rec := &sqlstore.SessionRecord{
		ID:           "SES-orphan",
		AgentProfile: "default",
		Provider:     "claude",
		RuntimeID:    "clockwork-cli/claude",
		RuntimeKind:  "cli",
		Workdir:      "/tmp",
		State:        "running",
	}
	require.NoError(t, store.CreateSession(rec))
	// CreateSession leaves it in 'launching'; bump to running directly.
	require.NoError(t, store.UpdateSessionState(rec.ID, "running", 0, nil))

	swept, err := mgr.Sweep()
	require.NoError(t, err)
	assert.Equal(t, 1, swept)

	got, err := store.GetSession(rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "crashed", got.State)
	require.True(t, got.ExitCode.Valid)
	assert.Equal(t, int64(-1), got.ExitCode.Int64)
}

func TestManager_OrphanSweepSparesLivePID(t *testing.T) {
	store := newTestStore(t)
	rt := newFakeRuntime("rt")
	mgr := sessionmgr.New(store, &fakeRegistry{rt: rt}, nil)

	// Use this test process's own PID — guaranteed alive, signal 0
	// returns nil → sweep should spare the row.
	rec := &sqlstore.SessionRecord{
		ID:           "SES-live",
		AgentProfile: "default",
		Provider:     "claude",
		RuntimeID:    "clockwork-cli/claude",
		RuntimeKind:  "cli",
		Workdir:      "/tmp",
		State:        "running",
	}
	require.NoError(t, store.CreateSession(rec))
	require.NoError(t, store.UpdateSessionState(rec.ID, "running", os.Getpid(), nil))

	swept, err := mgr.Sweep()
	require.NoError(t, err)
	assert.Equal(t, 0, swept)

	got, err := store.GetSession(rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "running", got.State)
}

func TestManager_LaunchWithBadAdapter(t *testing.T) {
	store := newTestStore(t)
	mgr := sessionmgr.New(store, &fakeRegistry{resolveErr: errors.New("boom")}, nil)

	_, err := mgr.Launch(context.Background(), sessionmgr.LaunchRequest{
		AgentProfile: "broken", Workdir: "/tmp",
	})
	require.Error(t, err)
}

func TestManager_ShutdownBlocksFurtherLaunches(t *testing.T) {
	store := newTestStore(t)
	mgr := sessionmgr.New(store, &fakeRegistry{rt: newFakeRuntime("rt")}, nil)

	require.NoError(t, mgr.Shutdown(context.Background()))
	_, err := mgr.Launch(context.Background(), sessionmgr.LaunchRequest{
		AgentProfile: "default", Workdir: "/tmp",
	})
	require.ErrorIs(t, err, sessionmgr.ErrManagerStopped)
}

func TestManager_GetUnknown(t *testing.T) {
	store := newTestStore(t)
	mgr := sessionmgr.New(store, &fakeRegistry{rt: newFakeRuntime("rt")}, nil)
	_, err := mgr.Get("missing")
	require.ErrorIs(t, err, sessionmgr.ErrSessionNotFound)
}
