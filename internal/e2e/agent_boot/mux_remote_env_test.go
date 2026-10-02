package agent_boot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/mcpbridge"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// What bootstrap.PlantRemoteMCP adds to Dependencies.MuxEnv reaches the
// planted mux entry, whose env mux passes to the `torque mcp` it starts:
// that is how an agent under ProtectedPaths gets a `torque mcp` that relays
// to the daemon instead of opening main.db (CW-20261001-0199).
func TestBoot_PlantedMuxCarriesTheRemoteMCPEnv(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{}, "claude-code")
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "claude-code", MuxServers: []string{"torque"}}}
	cd.Deps.MuxCommand = "/usr/bin/mux"
	cd.Deps.MuxArgs = []string{"mcp", "--proxy", "--servers", "vanta,torque,cerberus"}
	cd.Deps.MuxEnv = []string{mcpbridge.RemoteEnv + "=http://127.0.0.1:8990/mcp"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess, err := cd.Manager.Boot(ctx, agent.Options{TaskID: "CW-TEST-MUX-REMOTE", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeLongLived})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })

	raw, err := os.ReadFile(filepath.Join(sess.BootDir, ".mcp.json"))
	require.NoError(t, err)
	var cfg struct {
		MCPServers map[string]struct {
			Args []string          `json:"args"`
			Env  map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(raw, &cfg), string(raw))
	mux, ok := cfg.MCPServers["mux"]
	require.True(t, ok, "the mux entry is planted: %s", raw)
	assert.Equal(t, "http://127.0.0.1:8990/mcp", mux.Env[mcpbridge.RemoteEnv])
	assert.Contains(t, mux.Args, "--only")
	assert.Contains(t, mux.Args, "torque")
	assert.NotContains(t, mux.Args, "--servers")
}
