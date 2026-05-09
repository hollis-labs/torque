package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-runner/runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpenStderrSidecar_TeesToSessionLog is the regression test for
// CW-20260508-0006: subprocess-per-turn claude failures must be visible in
// <workspace>/logs/session.log, not just in the per-run sidecar log.
//
// The fix is structural — openStderrSidecar now wires a third writer at the
// session scope so RunID=0 boot paths (every long-lived orchestrator boot
// via MCP session_create) leave stderr where forensic tooling looks first.
func TestOpenStderrSidecar_TeesToSessionLog(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("CLOCKWORK_DATA_DIR", dataDir)

	wsRoot := t.TempDir()
	sessionLog := filepath.Join(wsRoot, "logs", "session.log")

	const runID = int64(7)
	w, tail, closer := openStderrSidecar(runID, sessionLog)
	require.NotNil(t, w)
	require.NotNil(t, tail)
	require.NotNil(t, closer)
	defer closer()

	const stderrLine = "claude: failed to connect to MCP loopback (boom)\n"
	n, err := w.Write([]byte(stderrLine))
	require.NoError(t, err)
	require.Equal(t, len(stderrLine), n)

	// Tail buffer captures the bytes (used by executor for failure-reason tail).
	assert.Contains(t, string(tail.Bytes()), strings.TrimSpace(stderrLine))

	// Per-run sidecar log carries the bytes (preserves CW-20260417-0024).
	sidecarPath := filepath.Join(dataDir, "runs", "7.stderr.log")
	sidecarBytes, err := os.ReadFile(sidecarPath)
	require.NoError(t, err, "per-run sidecar log must exist")
	assert.Contains(t, string(sidecarBytes), strings.TrimSpace(stderrLine))

	// Session log carries the bytes (CW-20260508-0006 — this is the fix).
	sessionBytes, err := os.ReadFile(sessionLog)
	require.NoError(t, err, "session.log must exist after stderr write")
	assert.Contains(t, string(sessionBytes), strings.TrimSpace(stderrLine),
		"subprocess stderr must be teed into <workspace>/logs/session.log")
}

// TestOpenStderrSidecar_EmptySessionLogPath is the back-compat path: when
// callers don't pass a session log destination (legacy callers / pre-CW-0006
// shape), only the tail buffer + per-run sidecar fire. This ensures the
// optional sessionLogPath argument is genuinely optional.
func TestOpenStderrSidecar_EmptySessionLogPath(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("CLOCKWORK_DATA_DIR", dataDir)

	w, tail, closer := openStderrSidecar(11, "")
	require.NotNil(t, w)
	defer closer()

	_, err := w.Write([]byte("legacy path\n"))
	require.NoError(t, err)

	assert.Contains(t, string(tail.Bytes()), "legacy path")
	sidecarBytes, err := os.ReadFile(filepath.Join(dataDir, "runs", "11.stderr.log"))
	require.NoError(t, err)
	assert.Contains(t, string(sidecarBytes), "legacy path")
}

// TestOpenStderrSidecar_SessionLogParentDirAutoCreated covers the case where
// the workspace dir tree exists but the logs/ subdir doesn't yet — the sidecar
// opener must create it (with 0o700 to match workspace.go's permissions
// convention). Without this, the first-write-after-Boot would race with
// workspace materialization.
func TestOpenStderrSidecar_SessionLogParentDirAutoCreated(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("CLOCKWORK_DATA_DIR", dataDir)

	wsRoot := t.TempDir()
	sessionLog := filepath.Join(wsRoot, "deep", "nested", "logs", "session.log")

	w, _, closer := openStderrSidecar(1, sessionLog)
	defer closer()

	_, err := w.Write([]byte("parent dir test\n"))
	require.NoError(t, err)

	bytes, err := os.ReadFile(sessionLog)
	require.NoError(t, err)
	assert.Contains(t, string(bytes), "parent dir test")
}

// TestOpenStderrSidecar_IntegratesWithRunner is the end-to-end wiring test
// the ticket asks for. Spawn a real subprocess (`/bin/sh -c "echo ... >&2"`)
// via go-runner with the writer openStderrSidecar produces — the same flow
// boot.go uses for ModeOneShot adapter spawns. Asserts the stderr line
// shows up in <workspace>/logs/session.log, proving subprocess-per-turn
// failure stderr is now visible at the per-session forensic surface.
//
// The fake adapter only needs Name/Detect/BuildArgs/ParseLine to satisfy
// runner.Config.Provider — runner doesn't call StreamChat or any of the
// HTTP-shaped methods.
func TestOpenStderrSidecar_IntegratesWithRunner(t *testing.T) {
	shPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("sh not available: %v", err)
	}

	dataDir := t.TempDir()
	t.Setenv("CLOCKWORK_DATA_DIR", dataDir)

	wsRoot := t.TempDir()
	sessionLog := filepath.Join(wsRoot, "logs", "session.log")

	const runID = int64(1234)
	stderrWriter, tail, closer := openStderrSidecar(runID, sessionLog)
	defer closer()

	adapter := &echoStderrAdapter{binPath: shPath}
	const sentinel = "echo-stderr-sentinel-line-XXVYY"

	cfg := runner.Config{
		Provider:  adapter,
		Workspace: wsRoot,
		Args:      []string{"-c", "echo " + sentinel + " >&2; exit 1"},
		Stderr:    stderrWriter,
		OnEvent:   func(runner.Event) {},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The shell exits 1, which surfaces as a *exec.ExitError; we only care
	// about the side-effect of stderr capture, not the exit code.
	_ = runner.Run(ctx, cfg)

	// Close before reading so the file is flushed.
	closer()

	// session.log carries the bytes — the CW-20260508-0006 acceptance criterion.
	sessionBytes, err := os.ReadFile(sessionLog)
	require.NoError(t, err)
	assert.Contains(t, string(sessionBytes), sentinel,
		"subprocess stderr must land in <workspace>/logs/session.log")

	// Per-run sidecar carries the bytes (preserves CW-20260417-0024).
	sidecarBytes, err := os.ReadFile(filepath.Join(dataDir, "runs", "1234.stderr.log"))
	require.NoError(t, err)
	assert.Contains(t, string(sidecarBytes), sentinel)

	// Tail buffer carries the bytes (executor surfaces this on failure).
	assert.Contains(t, string(tail.Bytes()), sentinel)
}

// echoStderrAdapter is a minimal provider.CLIAdapter that points runner.Run
// at /bin/sh so the test can drive a real subprocess with controlled stderr
// output without needing to build a fixture binary.
type echoStderrAdapter struct{ binPath string }

func (a *echoStderrAdapter) Name() string                      { return "echo-stderr" }
func (a *echoStderrAdapter) BuildArgs(_, _, _ string) []string { return nil }
func (a *echoStderrAdapter) Detect() (string, bool)            { return a.binPath, true }
func (a *echoStderrAdapter) ParseLine(_ []byte) ([]provider.StreamEvent, error) {
	return nil, nil
}

// TestOpenStderrSidecar_RunSidecarFailureDoesNotBlockSessionLog ensures the
// three sinks (tail / sidecar / session.log) degrade independently. If the
// per-run dir is unwritable the session log still receives stderr — losing
// any one sink is never a hard failure.
func TestOpenStderrSidecar_RunSidecarFailureDoesNotBlockSessionLog(t *testing.T) {
	// Point CLOCKWORK_DATA_DIR at a path that can't be a directory.
	notADir := filepath.Join(t.TempDir(), "blocking-file")
	require.NoError(t, os.WriteFile(notADir, []byte{}, 0o600))
	t.Setenv("CLOCKWORK_DATA_DIR", notADir)

	wsRoot := t.TempDir()
	sessionLog := filepath.Join(wsRoot, "logs", "session.log")

	w, tail, closer := openStderrSidecar(99, sessionLog)
	defer closer()

	_, err := w.Write([]byte("graceful degradation\n"))
	require.NoError(t, err)

	// tail still gets it.
	assert.Contains(t, string(tail.Bytes()), "graceful degradation")

	// session.log still gets it even though the per-run sidecar failed.
	bytes, err := os.ReadFile(sessionLog)
	require.NoError(t, err, "session.log tee must survive per-run sidecar failures")
	assert.Contains(t, string(bytes), "graceful degradation")
}
