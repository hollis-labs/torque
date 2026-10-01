package agent

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/providerplant"
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
	newRes := func(t *testing.T) (wrapperResources, *countingLoopback, *int, string) {
		dir := t.TempDir() + "/boot"
		require.NoError(t, os.MkdirAll(dir, 0o700))
		closes := 0
		lb := &countingLoopback{}
		return wrapperResources{
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
