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

// An agent CLI's error output can echo a credential from its environment.
// This fake opencode prints its OPENAI_API_KEY (a provider key Torque passes
// through to the CLI) in a stderr line and in its JSON error, then exits 1,
// so the first turn fails before the session is ready. None of what Torque
// persists may carry the key: the boot error (the run's ErrorMessage),
// session.log, stream.jsonl or the per-run stderr log (CW-20261001-0123).
func TestBootRedactsLaunchSecretsFromPersistedOutput(t *testing.T) {
	const secret = "sk-test-redact-0123456789abcdef"
	dir := t.TempDir()
	bin := filepath.Join(dir, "opencode")
	fake := `#!/bin/sh
echo "warn: auth with key $OPENAI_API_KEY was refused" >&2
printf '{"type":"error","sessionID":"ses_leak","error":{"name":"APIError","data":{"message":"Incorrect API key provided: %s"}}}\n' "$OPENAI_API_KEY"
exit 1
`
	require.NoError(t, os.WriteFile(bin, []byte(fake), 0o755))
	t.Setenv("OPENCODE_CLI_PATH", bin)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OPENAI_API_KEY", secret)
	dataDir := t.TempDir()
	t.Setenv("TORQUE_DATA_DIR", dataDir)

	cd := composeDeps(t, fakeRuntimeConfig{}, "opencode")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "opencode"}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	const sessID, runID = "SES-REDACT", 4242
	_, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: "CW-REDACT", AgentProfile: "worker", Workdir: t.TempDir(), RunID: runID,
		Mode: agent.ModeLongLived, IDFn: func() string { return sessID },
	})
	require.Error(t, err, "the first turn exits 1, so boot fails")
	msg := err.Error()
	assert.NotContains(t, msg, secret)
	assert.Contains(t, msg, "Incorrect API key provided: [redacted]")
	assert.Contains(t, msg, "warn: auth with key [redacted] was refused")

	logs := filepath.Join(cd.Deps.WorkspacesRoot, "unscoped", sessID, "logs")
	for _, path := range []string{
		filepath.Join(logs, "session.log"),
		filepath.Join(logs, "stream.jsonl"),
		filepath.Join(dataDir, "runs", "4242.stderr.log"),
	} {
		b, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.NotContains(t, string(b), secret, "%s carries the key", filepath.Base(path))
		assert.Contains(t, string(b), "[redacted]", "%s lost the line instead of redacting it", filepath.Base(path))
	}
}
