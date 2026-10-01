package agent_boot

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// TestBootReportsAFailingFirstTurn is CW-20261001-0105 (smoke E2, run 1169).
// A per-turn opencode session runs its first turn inside runtime.Start;
// this fake reports opencode's JSON error on stdout, writes a line to
// stderr and exits 1, as opencode does for an unknown model. Boot must
// carry the provider's error and the stderr line into its error (the run's
// ErrorMessage), the stream into stream.jsonl and the output into
// session.log. Before the fix all three had nothing but
// "runner: process exited 1".
func TestBootReportsAFailingFirstTurn(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "opencode")
	fake := `#!/bin/sh
echo 'warn: model catalog has no anthropic provider' >&2
printf '%s\n' '{"type":"error","sessionID":"ses_fail","error":{"name":"ProviderModelNotFoundError","data":{"message":"Model not found: opencode/claude-sonnet-4-5"}}}'
exit 1
`
	require.NoError(t, os.WriteFile(bin, []byte(fake), 0o755))
	t.Setenv("OPENCODE_CLI_PATH", bin)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cd := composeDeps(t, fakeRuntimeConfig{}, "opencode")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "opencode", Model: "opencode/claude-sonnet-4-5"}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	const sessID = "SES-FIRST-TURN-FAIL"
	_, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: "CW-FIRST-TURN", AgentProfile: "worker", Workdir: t.TempDir(),
		Mode: agent.ModeLongLived, IDFn: func() string { return sessID },
	})
	require.Error(t, err, "the first turn exits 1, so boot fails")
	msg := err.Error()
	assert.Contains(t, msg, "process exited 1")
	assert.Contains(t, msg, "provider error: Model not found: opencode/claude-sonnet-4-5")
	assert.Contains(t, msg, "stderr: warn: model catalog has no anthropic provider")
	assert.LessOrEqual(t, len(msg), 4096, "the detail is bounded")

	logs := filepath.Join(cd.Deps.WorkspacesRoot, "unscoped", sessID, "logs")
	sessionLog, err := os.ReadFile(filepath.Join(logs, "session.log"))
	require.NoError(t, err)
	assert.Contains(t, string(sessionLog), "warn: model catalog has no anthropic provider")
	assert.Contains(t, string(sessionLog), "[error] Model not found: opencode/claude-sonnet-4-5")
	stream, err := os.ReadFile(filepath.Join(logs, "stream.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(stream), `"type":"error"`)
	assert.Contains(t, string(stream), "Model not found: opencode/claude-sonnet-4-5")
}
