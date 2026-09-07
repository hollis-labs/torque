package agent_boot

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/go-sqlite/sqlitekit"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// claudeStreamJSONFixture is a minimal claude-code streaming-stdio
// (--input-format stream-json --output-format stream-json) fixture: for
// every line read from stdin it emits a fixed, valid stream-json turn
// (system/init with a session id, one assistant text delta, a successful
// result with usage), then loops for the next turn. Exits cleanly on EOF or
// SIGTERM, matching claude-code's real long-lived streaming-stdio process
// shape. Wire shapes are lifted directly from go-providers'
// parseClaudeStreamLine (provider/pty_claude.go) -- not invented here.
const claudeStreamJSONFixture = `#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' '{"type":"system","subtype":"init","session_id":"fixture-session-1"}'
  printf '%s\n' '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"fixture ok"}]}}'
  printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"ok","usage":{"input_tokens":10,"output_tokens":5}}'
done
exit 0
`

// writeClaudeFixture publishes the fixture script and points go-providers'
// ClaudeAdapter.Detect() at it via CLAUDE_CLI_PATH, so runtime.Prepare's
// binary resolution finds it instead of a real claude install.
func writeClaudeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "claude-fixture.sh")
	require.NoError(t, os.WriteFile(path, []byte(claudeStreamJSONFixture), 0o755))
	t.Setenv("CLAUDE_CLI_PATH", path)
	return path
}

// composeWrapperDeps mirrors composeDeps but deliberately leaves
// Dependencies.RuntimeFactory nil, so Boot takes the CW-20260904-0098
// go-agent-wrapper-routed path instead of the legacy agentsessions-direct
// path RuntimeFactory forces (boot.go's useWrapper predicate). This is the
// one seam that path cannot be exercised through fakeRuntime -- proving it
// works requires a real (if minimal) subprocess, hence the fixture above.
func composeWrapperDeps(t *testing.T, profileProvider string) *agent.Dependencies {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlitekit.OpenWriter(context.Background(), filepath.Join(dir, "wrapper_boot.db"), sqlitekit.OpenOptions{Options: sqlitekit.WriterOptions()})
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)

	prof := config.AgentProfile{Executor: "cli", Provider: profileProvider}
	deps := &agent.Dependencies{
		Store:          store,
		Profiles:       config.ProfileMap{"torque-backend": prof, "default": prof},
		Loopback:       nil,
		WorkspacesRoot: filepath.Join(dir, "workspaces"),
	}
	deps.Sessions = agent.NewManager(deps)

	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := deps.Sessions.Shutdown(shutdownCtx); err != nil {
			t.Errorf("agent.Manager.Shutdown returned error: %v", err)
		}
		cancel()
		store.Close()
		_ = db.Close()
	})

	return deps
}

// TestWrapperBoot_OneShot_HappyPath proves the go-agent-wrapper-routed
// ModeOneShot path (CW-20260904-0098) actually works end to end against a
// real (fixture) subprocess: wrapper.Wrapper spawns it, torqueRuntimeEventSink
// observes KindSessionReady/KindProcessStarted/KindAgentDelta/KindTurnCompleted,
// the session reaches StatusDone, and the reported exit code is 0.
func TestWrapperBoot_OneShot_HappyPath(t *testing.T) {
	writeClaudeFixture(t)
	deps := composeWrapperDeps(t, "claude-code")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sess, err := deps.Sessions.Boot(ctx, agent.Options{
		TaskID:       "CW-TEST-WRAPPER-001",
		AgentProfile: "torque-backend",
		Workdir:      t.TempDir(),
		Mode:         agent.ModeOneShot,
		Description:  "say hello",
	})
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Equal(t, agent.StatusDone, sess.Status, "ModeOneShot must reach StatusDone on the wrapper path")
	require.NotNil(t, sess.ExitCode)
	require.Equal(t, 0, *sess.ExitCode)

	got, err := deps.Sessions.Get(sess.ID)
	require.NoError(t, err)
	require.Equal(t, agent.StatusDone, got.Status, "persisted row must also reflect StatusDone")
}

// TestWrapperBoot_LongLived_StopAndWait proves Manager.Stop/Wait dual-dispatch
// correctly to the wrapper-routed session (manager.go's wrapperHandleFor
// branches) for a long-lived session, not just ModeOneShot's synchronous path.
func TestWrapperBoot_LongLived_StopAndWait(t *testing.T) {
	writeClaudeFixture(t)
	deps := composeWrapperDeps(t, "claude-code")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sess, err := deps.Sessions.Boot(ctx, agent.Options{
		TaskID:       "CW-TEST-WRAPPER-002",
		AgentProfile: "torque-backend",
		Workdir:      t.TempDir(),
		Mode:         agent.ModeLongLived,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Equal(t, agent.StatusRunning, sess.Status)

	// LivePID must be observable while the fixture is alive -- proves
	// torqueRuntimeEventSink's KindProcessStarted handling wrote a real pid.
	require.Eventually(t, func() bool {
		return deps.Sessions.LivePID(sess.ID) > 0
	}, 3*time.Second, 20*time.Millisecond, "LivePID must be > 0 once KindProcessStarted fires")

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	require.NoError(t, deps.Sessions.Stop(stopCtx, sess.ID))

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer waitCancel()
	_, _ = deps.Sessions.Wait(waitCtx, sess.ID)

	got, err := deps.Sessions.Get(sess.ID)
	require.NoError(t, err)
	require.True(t, got.Status == agent.StatusDone || got.Status == agent.StatusFailed,
		"session must reach a terminal state after Stop, got %q", got.Status)
}
