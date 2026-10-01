package agent_boot

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/providertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// A wrapper-routed session is torn down however it ends (CW-20261001-0161):
// its boot dir is removed and its loopback MCP listener closed, as the
// legacy path does on terminal state, whether Stop ran, the agent exited
// on its own, or the Manager shut down.

// listeningLoopback is a loopback handle with a real listener, so a test
// can tell whether teardown closed it.
type listeningLoopback struct {
	addr string
	srv  *http.Server
}

func (l *listeningLoopback) URL() string                        { return "http://" + l.addr + "/mcp" }
func (l *listeningLoopback) Shutdown(ctx context.Context) error { return l.srv.Shutdown(ctx) }

func (l *listeningLoopback) listening() bool {
	conn, err := net.DialTimeout("tcp", l.addr, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func composeTeardownDeps(t *testing.T) (*composedDeps, *listeningLoopback) {
	t.Helper()
	cd := composeDeps(t, fakeRuntimeConfig{}, "claude-code")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "claude-code", RuntimeKind: "streaming-stdio", PermissionMode: "acceptEdits"}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	lb := &listeningLoopback{addr: ln.Addr().String(), srv: &http.Server{Handler: http.NotFoundHandler(), ReadHeaderTimeout: time.Second}}
	go func() { _ = lb.srv.Serve(ln) }()
	t.Cleanup(func() { _ = lb.srv.Close() })
	cd.Deps.Loopback = func(string, string) (agent.LoopbackHandle, error) { return lb, nil }
	return cd, lb
}

// claudeFirstTurn is the first turn of go-providers' captured two-turn
// claude stream: the user message and Claude's reply through `result`.
func claudeFirstTurn(t *testing.T) []providertest.Step {
	t.Helper()
	steps := providertest.FixtureSteps(t, "claude/stream_two_turns")
	require.Len(t, steps, 13, "the fixture's shape changed; re-derive the first turn")
	return append([]providertest.Step(nil), steps[:6]...)
}

func requireTornDown(t *testing.T, bootDir string, lb *listeningLoopback) {
	t.Helper()
	require.NotEmpty(t, bootDir)
	require.Eventually(t, func() bool {
		_, err := os.Stat(bootDir)
		return os.IsNotExist(err) && !lb.listening()
	}, 5*time.Second, 20*time.Millisecond, "the boot dir must be removed and the loopback closed")
}

// TestWrapperSessionEndingOnItsOwnIsTornDown: a long-lived session whose
// agent exits by itself, with no Stop from anyone, is still torn down.
// Before, only Stop removed a wrapper session's boot dir and closed its
// loopback, so a manual session or orchestrator that exited kept both until
// the daemon did.
func TestWrapperSessionEndingOnItsOwnIsTornDown(t *testing.T) {
	fake := providertest.New(t, runtimes.Claude, providertest.Script(append(claudeFirstTurn(t), providertest.Exit(0))...))
	fake.Install()
	cd, lb := composeTeardownDeps(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: "CW-TEARDOWN-EXIT", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeLongLived,
	})
	require.NoError(t, err)
	require.True(t, lb.listening(), "the loopback serves while the session runs")

	requireTornDown(t, sess.BootDir, lb)
	rec, err := cd.Store.GetSession(sess.ID)
	require.NoError(t, err)
	assert.Contains(t, []string{"done", "failed"}, rec.State, "the session row is terminal")
}

// TestWrapperOneShotSessionLeavesNothing: a one-shot run removes its boot
// dir and closes its loopback by the time Boot returns.
func TestWrapperOneShotSessionLeavesNothing(t *testing.T) {
	fake := providertest.New(t, runtimes.Claude, providertest.Script(append(claudeFirstTurn(t), providertest.AwaitEOF(), providertest.Exit(0))...))
	fake.Install()
	cd, lb := composeTeardownDeps(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: "CW-TEARDOWN-ONESHOT", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeOneShot, Description: "say hi",
	})
	require.NoError(t, err)
	assert.Equal(t, agent.StatusDone, sess.Status)
	requireTornDown(t, sess.BootDir, lb)
}

// TestManagerShutdownTearsDownWrapperSessions: Shutdown stops a live
// wrapper session and tears it down. Before, Shutdown reached only the
// legacy agentsessions manager, so a wrapper session kept running past it
// with its boot dir and loopback.
func TestManagerShutdownTearsDownWrapperSessions(t *testing.T) {
	fake := providertest.New(t, runtimes.Claude, providertest.Script(append(claudeFirstTurn(t), providertest.AwaitEOF(), providertest.Exit(0))...))
	fake.Install()
	cd, lb := composeTeardownDeps(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: "CW-TEARDOWN-SHUTDOWN", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeLongLived,
	})
	require.NoError(t, err)
	_, err = os.Stat(sess.BootDir)
	require.NoError(t, err, "the boot dir exists while the session runs")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	require.NoError(t, cd.Manager.Shutdown(shutdownCtx))

	_, err = os.Stat(sess.BootDir)
	assert.True(t, os.IsNotExist(err), "Shutdown removes the boot dir: %v", err)
	assert.False(t, lb.listening(), "Shutdown closes the loopback")
	rec, err := cd.Store.GetSession(sess.ID)
	require.NoError(t, err)
	assert.Contains(t, []string{"done", "failed"}, rec.State, "the session row is terminal")
	calls := fake.Calls()
	require.Len(t, calls, 1)
	assert.True(t, calls[0].Exited, "the agent process ended: %s", strings.Join(calls[0].Notes, "; "))
}
