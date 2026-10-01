package agent

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/providerplant"
	"github.com/hollis-labs/agentkit/agentsessions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/writeq"
)

// A Boot that fails after launcher.Prepare allocated the boot dir removes
// it (CW-20261001-0161). Before, a planting failure returned with the
// allocated dir left in $TMPDIR/torque-boot, and no session row named it.
func TestBoot_PlantFailureRemovesTheAllocatedBootDir(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	planted := false
	prev := plantBootDir
	plantBootDir = func(_ context.Context, prepared *agentlaunch.PreparedLaunch, _ ...providerplant.Option) (*agentlaunch.PreparedExecution, error) {
		_, err := os.Stat(prepared.PlantedBootDir)
		planted = err == nil
		return nil, errors.New("planting failed")
	}
	t.Cleanup(func() { plantBootDir = prev })

	store := newTestStoreForLongLived(t)
	deps := &Dependencies{
		Store:          store,
		StateWriter:    writeq.NewDirect(store),
		WorkspacesRoot: t.TempDir(),
		Profiles: config.ProfileMap{
			"test": {Executor: "cli", Provider: "claude-code", RuntimeKind: "streaming-stdio", PermissionMode: "acceptEdits"},
		},
	}
	deps.Sessions = NewManager(deps)

	_, err := Boot(context.Background(), deps, Options{
		TaskID: "CW-TEST-PLANT-FAIL", AgentProfile: "test", Workdir: t.TempDir(), Mode: ModeLongLived,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBootFailed), "%v", err)
	assert.Contains(t, err.Error(), "plant boot dir: planting failed")
	require.True(t, planted, "the boot dir existed when planting ran: the test exercises the dir Prepare allocated")

	entries, err := os.ReadDir(DefaultBuildDirRoot())
	require.NoError(t, err)
	assert.Empty(t, entries, "the allocated boot dir must not outlive the failed Boot")
}

type countingLoopback struct{ shutdowns int }

func (l *countingLoopback) URL() string                    { return "http://127.0.0.1:1/mcp" }
func (l *countingLoopback) Shutdown(context.Context) error { l.shutdowns++; return nil }

// Resources Boot hands over after the session already ended are released
// at once; handed over before it ends, they are released by the run
// goroutine's teardown (finishWrapperSession). Either order leaks nothing.
func TestAdoptWrapperResources_EitherSideOfTheSessionEnding(t *testing.T) {
	newRes := func(t *testing.T) (sessionResources, *countingLoopback, *int, string) {
		dir := t.TempDir() + "/boot"
		require.NoError(t, os.MkdirAll(dir, 0o700))
		closes := 0
		lb := &countingLoopback{}
		return sessionResources{
			loopback:    lb,
			closeStderr: func() { closes++ },
			closeStream: func() { closes++ },
			bootDir:     dir,
		}, lb, &closes, dir
	}
	released := func(t *testing.T, lb *countingLoopback, closes *int, dir string) {
		t.Helper()
		assert.Equal(t, 1, lb.shutdowns, "loopback shut down once")
		assert.Equal(t, 2, *closes, "both sidecars closed")
		_, err := os.Stat(dir)
		assert.True(t, os.IsNotExist(err), "boot dir removed")
	}

	t.Run("handed over after the session ended", func(t *testing.T) {
		m := NewManager(&Dependencies{})
		h := &wrapperHandle{runDone: make(chan struct{})}
		m.registerWrapperSession("S1", h)
		m.finishWrapperSession("S1", h)
		res, lb, closes, dir := newRes(t)
		m.adoptWrapperResources("S1", h, res)
		released(t, lb, closes, dir)
	})
	t.Run("handed over before the session ended", func(t *testing.T) {
		m := NewManager(&Dependencies{})
		h := &wrapperHandle{runDone: make(chan struct{})}
		m.registerWrapperSession("S2", h)
		res, lb, closes, dir := newRes(t)
		m.adoptWrapperResources("S2", h, res)
		assert.Zero(t, lb.shutdowns, "nothing released while the session runs")
		m.finishWrapperSession("S2", h)
		released(t, lb, closes, dir)
		m.teardownSession("S2") // Stop after the end: idempotent
		released(t, lb, closes, dir)
	})
}

// The legacy path's counterpart: a session whose terminal event ran
// teardownSession before Boot handed its resources over gets them released
// on arrival; handed over first, teardown releases them; a Boot that fails
// before handing over leaves no mark behind (CW-20261001-0166).
func TestAdoptLegacyResources_EitherSideOfTheSessionEnding(t *testing.T) {
	newRes := func(t *testing.T) (sessionResources, *countingLoopback, *int, string) {
		dir := t.TempDir() + "/boot"
		require.NoError(t, os.MkdirAll(dir, 0o700))
		closes := 0
		lb := &countingLoopback{}
		return sessionResources{
			loopback:    lb,
			closeStderr: func() { closes++ },
			closeStream: func() { closes++ },
			bootDir:     dir,
			closePoller: func() { closes++ },
		}, lb, &closes, dir
	}
	released := func(t *testing.T, lb *countingLoopback, closes *int, dir string) {
		t.Helper()
		assert.Equal(t, 1, lb.shutdowns, "loopback shut down once")
		assert.Equal(t, 3, *closes, "sidecars and poller closed")
		_, err := os.Stat(dir)
		assert.True(t, os.IsNotExist(err), "boot dir removed")
	}

	t.Run("the session ended before the hand-over", func(t *testing.T) {
		m := NewManager(&Dependencies{})
		m.beginLegacyAdoption("L1")
		m.teardownSession("L1") // the terminal event
		res, lb, closes, dir := newRes(t)
		m.adoptLegacyResources("L1", res)
		released(t, lb, closes, dir)
		assert.Empty(t, m.pendingAdoption, "the mark is gone")
	})
	t.Run("the session ended after the hand-over", func(t *testing.T) {
		m := NewManager(&Dependencies{})
		m.beginLegacyAdoption("L2")
		res, lb, closes, dir := newRes(t)
		m.adoptLegacyResources("L2", res)
		assert.Zero(t, lb.shutdowns, "nothing released while the session runs")
		m.teardownSession("L2")
		released(t, lb, closes, dir)
		assert.Empty(t, m.pendingAdoption)
	})
	t.Run("Boot failed before the hand-over", func(t *testing.T) {
		m := NewManager(&Dependencies{})
		m.beginLegacyAdoption("L3")
		m.abandonLegacyAdoption("L3")
		assert.Empty(t, m.pendingAdoption)
	})
}

// endedRuntime is a legacy runtime whose session has ended by the time
// Start returns, the way an agent CLI that exits at once does.
type endedRuntime struct{}

func (endedRuntime) ID() string                       { return "ended-fake" }
func (endedRuntime) Kind() string                     { return string(RuntimeKindStreamingStdio) }
func (endedRuntime) Caps() agentsessions.Capabilities { return agentsessions.Capabilities{} }
func (endedRuntime) Prepare(context.Context) error    { return nil }
func (endedRuntime) Start(context.Context, agentsessions.StartOptions) (agentsessions.Session, error) {
	return endedSession{}, nil
}

type endedSession struct{}

func (endedSession) Wait() (int, error)                           { return 0, nil }
func (endedSession) Stop(context.Context) error                   { return nil }
func (endedSession) SendInput(context.Context, []byte) error      { return nil }
func (endedSession) Resize(context.Context, uint16, uint16) error { return nil }
func (endedSession) Health() agentsessions.HealthStatus           { return agentsessions.HealthStatus{} }
func (endedSession) CheckpointHints() (agentsessions.CheckpointHint, bool) {
	return agentsessions.CheckpointHint{}, false
}

type listenerLoopback struct {
	ln  net.Listener
	srv *http.Server
}

func (l *listenerLoopback) URL() string                        { return "http://" + l.ln.Addr().String() + "/mcp" }
func (l *listenerLoopback) Shutdown(ctx context.Context) error { return l.srv.Shutdown(ctx) }

// A long-lived legacy session that ends before Boot registers its
// resources still has its boot dir removed and its loopback closed. The
// hook holds Boot until the session's terminal event has run
// teardownSession, which before this change found nothing registered, and
// the resources registered after it were never released.
func TestBootLegacy_SessionEndingBeforeRegistrationLeaksNothing(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	store := newTestStoreForLongLived(t)
	deps := &Dependencies{
		Store:          store,
		StateWriter:    writeq.NewDirect(store),
		WorkspacesRoot: t.TempDir(),
		Profiles: config.ProfileMap{
			"test": {Executor: "cli", Provider: "claude-code", RuntimeKind: "streaming-stdio", PermissionMode: "acceptEdits"},
		},
		RuntimeFactory: func(agentsessions.AdapterRuntimeConfig) (agentsessions.Runtime, error) { return endedRuntime{}, nil },
	}
	var lb *listenerLoopback
	deps.Loopback = func(string, string) (LoopbackHandle, error) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		lb = &listenerLoopback{ln: ln, srv: &http.Server{Handler: http.NotFoundHandler(), ReadHeaderTimeout: time.Second}}
		go func() { _ = lb.srv.Serve(ln) }()
		return lb, nil
	}
	deps.Sessions = NewManager(deps)
	m := deps.Sessions

	prev := legacyAdoptHook
	legacyAdoptHook = func(sessID string) {
		require.Eventually(t, func() bool {
			m.mu.Lock()
			defer m.mu.Unlock()
			return m.pendingAdoption[sessID]
		}, 5*time.Second, 5*time.Millisecond, "the session's terminal event ran teardown before the hand-over")
	}
	t.Cleanup(func() { legacyAdoptHook = prev })

	sess, err := Boot(context.Background(), deps, Options{
		TaskID: "CW-TEST-LEGACY-ENDED", AgentProfile: "test", Workdir: t.TempDir(), Mode: ModeLongLived,
	})
	require.NoError(t, err)
	require.NotEmpty(t, sess.BootDir)

	_, statErr := os.Stat(sess.BootDir)
	assert.True(t, os.IsNotExist(statErr), "the boot dir is removed: %v", statErr)
	conn, dialErr := net.DialTimeout("tcp", lb.ln.Addr().String(), 200*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
	}
	assert.Error(t, dialErr, "the loopback listener is closed")
	m.mu.Lock()
	defer m.mu.Unlock()
	assert.Empty(t, m.bootDirs, "nothing left registered")
	assert.Empty(t, m.loopbacks)
	assert.Empty(t, m.pidPollers)
	assert.Empty(t, m.pendingAdoption)
}
