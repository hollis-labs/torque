package agent_boot

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/providertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// Claude loads only the MCP servers Torque plants (CW-20261001-0226): every
// Claude launch gets --strict-mcp-config, so the operator's user-level
// ~/.claude.json mcpServers (the interactive `mux`, whatever else is
// configured there) are not loaded beside the planted <boot dir>/.mcp.json.
// The planted file is unchanged: it still carries the run's loopback.

// bootClaude boots claude-code under profileName with the given runtime kind
// ("" is the default, streaming-stdio) against a fake CLI, and returns the
// first launch's argv and the servers in the planted --mcp-config file.
func bootClaude(t *testing.T, runtimeKind, profileName string, opts agent.Options, setup func(*composedDeps)) ([]string, map[string]json.RawMessage) {
	t.Helper()
	var run providertest.Run
	if runtimeKind == "subprocess" {
		run = providertest.Replay("claude/print_turn1")
	} else {
		run = providertest.Script(providertest.AwaitEOF())
	}
	fake := providertest.New(t, runtimes.Claude, run)
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, "claude-code")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{profileName: {
		Executor: "cli", Provider: "claude-code", RuntimeKind: runtimeKind, PermissionMode: "acceptEdits",
	}}
	// A real per-task loopback, so the planted file carries it.
	require.NoError(t, cd.Store.CreateTask(&sqlstore.TaskRecord{ID: "CW-STRICT-MCP", Title: "strict mcp", Priority: 2}))
	cd.Deps.Loopback = func(id, _ string) (agent.LoopbackHandle, error) {
		return serveWorkerLoopback(t, cd.Store, id), nil
	}
	if setup != nil {
		setup(cd)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	opts.TaskID, opts.AgentProfile, opts.Workdir = "CW-STRICT-MCP", profileName, t.TempDir()
	if opts.Mode == 0 {
		opts.Mode = agent.ModeLongLived
	}
	sess, err := cd.Manager.Boot(ctx, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })

	require.Eventually(t, func() bool { return len(fake.Calls()) > 0 }, 5*time.Second, 20*time.Millisecond)
	args := fake.Call(0).Args
	cfgPath, ok := fake.Call(0).ArgAfter("--mcp-config")
	require.True(t, ok, "claude is launched with --mcp-config: %q", args)
	raw, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var cfg struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(raw, &cfg), string(raw))
	return args, cfg.MCPServers
}

// Both claude runtime kinds Torque launches (streaming-stdio, the default,
// and subprocess-per-turn), under the worker role and the orchestrator-class
// roles, carry --strict-mcp-config, and the planted file still has the
// loopback server they report through.
func TestClaudeLaunch_StrictMCPConfig_EveryKindAndRole(t *testing.T) {
	for _, kind := range []string{"", "subprocess"} {
		for _, role := range []string{"worker", "planner", "reviewer-end-agent"} {
			name := kind
			if name == "" {
				name = "streaming-stdio"
			}
			t.Run(name+"/"+role, func(t *testing.T) {
				args, servers := bootClaude(t, kind, role, agent.Options{Role: role}, nil)
				assert.Equal(t, 1, countArg(args, "--strict-mcp-config"), "argv: %q", args)
				assert.Contains(t, servers, "loopback", "the planted loopback is untouched")
			})
		}
	}
}

// TORQUE_CLAUDE_STRICT_MCP=0 is the kill switch; a typo is not.
func TestClaudeLaunch_StrictMCPConfig_KillSwitch(t *testing.T) {
	for _, tc := range []struct {
		value  string
		strict bool
	}{{"0", false}, {"off", false}, {"false", false}, {"no", false}, {"oof", true}, {"", true}} {
		t.Run("value="+tc.value, func(t *testing.T) {
			t.Setenv("TORQUE_CLAUDE_STRICT_MCP", tc.value)
			for _, kind := range []string{"", "subprocess"} {
				args, servers := bootClaude(t, kind, "worker", agent.Options{}, nil)
				assert.Equal(t, tc.strict, countArg(args, "--strict-mcp-config") == 1, "kind %q argv: %q", kind, args)
				assert.Contains(t, servers, "loopback")
			}
		})
	}
}

// strict mode does not change what Torque plants: where a profile gets the
// mux server, it is still in the planted file beside the loopback.
func TestClaudeLaunch_StrictMCPConfig_PlantedMuxIsStillThere(t *testing.T) {
	_, servers := bootClaude(t, "", "worker", agent.Options{}, func(cd *composedDeps) {
		cd.Deps.MuxCommand = "/usr/local/bin/mux"
		cd.Deps.MuxArgs = []string{"mcp", "--proxy", "--servers", "vanta,torque,cerberus"}
	})
	assert.Contains(t, servers, "loopback")
	assert.Contains(t, servers, "mux")
}
