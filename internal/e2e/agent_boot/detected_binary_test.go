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

// TestBootSpawnsTheDetectedBinary is CW-20261001-0098. opencode lives only
// in ~/.opencode/bin, an install dir go-providers' Detect searches but that
// is not on the daemon's PATH, and no OPENCODE_CLI_PATH is set. The wrapper
// path spawns the planted launch's argv[0]; before the fix that was the bare
// name `opencode`, which PATH could not resolve, so boot failed with
// `exec: "opencode": executable file not found in $PATH`. Boot now pins the
// adapter's detected absolute path as the launch plan's provider binary.
func TestBootSpawnsTheDetectedBinary(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".opencode", "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	record := filepath.Join(t.TempDir(), "spawned")
	fake := `#!/bin/sh
printf '%s\n' "$0" > "$TORQUE_TEST_SPAWNED"
printf '%s\n' '{"type":"step_start","sessionID":"ses_detect","part":{"type":"step-start"}}'
printf '%s\n' '{"type":"step_finish","sessionID":"ses_detect","part":{"type":"step-finish","reason":"stop","tokens":{"input":1,"output":1}}}'
`
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "opencode"), []byte(fake), 0o755))

	t.Setenv("HOME", home)
	t.Setenv("OPENCODE_CLI_PATH", "")
	// Only system dirs: neither the TestMain agent shims nor any real
	// opencode install is reachable through PATH.
	t.Setenv("PATH", "/usr/bin:/bin")

	cd := composeDeps(t, fakeRuntimeConfig{}, "opencode")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "opencode"}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: "CW-DETECTED-BINARY", AgentProfile: "worker", Workdir: t.TempDir(),
		Mode: agent.ModeOneShot, Description: "say hello",
		Env: map[string]string{"TORQUE_TEST_SPAWNED": record},
	})
	require.NoError(t, err, "boot must find opencode in ~/.opencode/bin without PATH")
	assert.Equal(t, agent.StatusDone, sess.Status)
	spawned, err := os.ReadFile(record)
	require.NoError(t, err, "the fake in ~/.opencode/bin must be the process spawned")
	assert.Equal(t, filepath.Join(binDir, "opencode")+"\n", string(spawned))
}
