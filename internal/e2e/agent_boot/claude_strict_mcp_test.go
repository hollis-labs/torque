package agent_boot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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

// assertStrictOnce checks a Claude launch's argv carries --strict-mcp-config
// exactly once, among the options: before the "--" that ends them, so it is
// never read as prompt text.
func assertStrictOnce(t *testing.T, args []string) {
	t.Helper()
	assert.Equal(t, 1, countArg(args, "--strict-mcp-config"), "argv: %q", args)
	if end := slices.Index(args, "--"); end >= 0 {
		assert.Less(t, slices.Index(args, "--strict-mcp-config"), end, "the flag must come before --: %q", args)
	}
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
				assertStrictOnce(t, args)
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

// muxArgs reads a planted mux server's argv out of the planted .mcp.json.
func muxArgs(t *testing.T, servers map[string]json.RawMessage) []string {
	t.Helper()
	raw, ok := servers["mux"]
	require.True(t, ok, "no mux server planted: %v", servers)
	var entry struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	require.NoError(t, json.Unmarshal(raw, &entry), string(raw))
	assert.Equal(t, "/usr/local/bin/mux", entry.Command)
	return entry.Args
}

// withDaemonMux gives the daemon a mux binary and its default argv, the
// shape the production bootstrap resolves (internal/runtime/bootstrap/mux.go).
func withDaemonMux(cd *composedDeps) {
	cd.Deps.MuxCommand = "/usr/local/bin/mux"
	cd.Deps.MuxArgs = []string{"mcp", "--proxy", "--servers", "vanta,torque,cerberus", "--token", "local-dev", "--scopes", "session.write,message.write"}
}

// A Claude worker gets NO planted mux by default (CW-20261001-0226): the
// daemon's default `mux mcp --proxy --servers vanta,torque,cerberus` goes
// away for it, cerberus (deploy, ssh) being the riskiest server and the
// sessions never calling mux. The planted file is the run's loopback alone,
// on both runtime kinds and under every role, even with a mux binary on the
// daemon.
func TestClaudeLaunch_NoMuxByDefault(t *testing.T) {
	for _, kind := range []string{"", "subprocess"} {
		for _, role := range []string{"worker", "planner", "reviewer-end-agent"} {
			name := kind
			if name == "" {
				name = "streaming-stdio"
			}
			t.Run(name+"/"+role, func(t *testing.T) {
				_, servers := bootClaude(t, kind, role, agent.Options{Role: role}, withDaemonMux)
				assert.Contains(t, servers, "loopback")
				assert.NotContains(t, servers, "mux", "no mux unless the profile names mux_servers")
				assert.Len(t, servers, 1, "the loopback alone: %v", servers)
			})
		}
	}
}

// A profile that names mux_servers gets mux planted with exactly those, the
// daemon's token and scopes kept; cerberus is in the planted set only when
// the profile names it. The loopback is untouched.
func TestClaudeLaunch_MuxServersPlantsExactlyThoseNamed(t *testing.T) {
	for _, kind := range []string{"", "subprocess"} {
		for _, tc := range []struct {
			name    string
			servers []string
			want    string
		}{
			{"two servers", []string{"vanta", "tesseract"}, "vanta,tesseract"},
			{"one server", []string{"torque"}, "torque"},
			{"cerberus named", []string{"vanta", "cerberus"}, "vanta,cerberus"},
		} {
			name := kind
			if name == "" {
				name = "streaming-stdio"
			}
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				_, servers := bootClaude(t, kind, "worker", agent.Options{}, func(cd *composedDeps) {
					withDaemonMux(cd)
					prof := cd.Deps.Profiles.(config.ProfileMap)["worker"]
					prof.MuxServers = tc.servers
					cd.Deps.Profiles = config.ProfileMap{"worker": prof}
				})
				assert.Contains(t, servers, "loopback")
				args := muxArgs(t, servers)
				assert.Equal(t,
					[]string{"mcp", "--proxy", "--token", "local-dev", "--scopes", "session.write,message.write", "--only", tc.want},
					args)
				// --only is mux's curated mode: exactly these servers' tools, and no
				// mux_discover/mux_call into the others or mux's own Tether tools,
				// which --servers would leave on the planted token and scopes.
				assert.NotContains(t, args, "--servers", "--servers leaves mux_call open to every server")
				assert.NotContains(t, args, "--broker")
			})
		}
	}
}

// bootOpencodeConfig boots opencode (run, subprocess-per-turn) against a fake
// CLI and returns the "mcp" servers planted in its opencode.json.
func bootOpencodeConfig(t *testing.T, mux bool, servers []string) map[string]json.RawMessage {
	t.Helper()
	fake := providertest.New(t, runtimes.OpenCode, providertest.Replay("opencode/run_turn1"))
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, "opencode")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "opencode", MuxServers: servers}}
	require.NoError(t, cd.Store.CreateTask(&sqlstore.TaskRecord{ID: "CW-OC-MUX", Title: "opencode mux", Priority: 2}))
	cd.Deps.Loopback = func(id, _ string) (agent.LoopbackHandle, error) {
		return serveWorkerLoopback(t, cd.Store, id), nil
	}
	if mux {
		withDaemonMux(cd)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.Boot(ctx, agent.Options{TaskID: "CW-OC-MUX", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeLongLived})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })
	require.Eventually(t, func() bool { return len(fake.Calls()) > 0 }, 5*time.Second, 20*time.Millisecond)
	dir, ok := fake.Call(0).Getenv("OPENCODE_CONFIG_DIR")
	require.True(t, ok, "opencode is pointed at its planted config dir")
	raw, err := os.ReadFile(filepath.Join(dir, "opencode.json"))
	require.NoError(t, err)
	var cfg struct {
		MCP map[string]json.RawMessage `json:"mcp"`
	}
	require.NoError(t, json.Unmarshal(raw, &cfg), string(raw))
	return cfg.MCP
}

// OpenCode on its native runtime is unchanged by the Claude decision: it
// still gets the daemon's mux with the daemon's default servers (curated with
// --only), and a profile's mux_servers narrows them.
func TestOpencodeLaunch_MuxDefaultSetIsCurated(t *testing.T) {
	var entry struct {
		Command []string `json:"command"`
	}
	servers := bootOpencodeConfig(t, true, nil)
	require.Contains(t, servers, "mux")
	require.NoError(t, json.Unmarshal(servers["mux"], &entry))
	assert.Equal(t, []string{"/usr/local/bin/mux", "mcp", "--proxy", "--token", "local-dev", "--scopes", "session.write,message.write", "--only", "vanta,torque,cerberus"}, entry.Command)

	servers = bootOpencodeConfig(t, true, []string{"vanta"})
	require.Contains(t, servers, "mux")
	require.NoError(t, json.Unmarshal(servers["mux"], &entry))
	assert.Equal(t, []string{"/usr/local/bin/mux", "mcp", "--proxy", "--token", "local-dev", "--scopes", "session.write,message.write", "--only", "vanta"}, entry.Command)
}

// --strict-mcp-config is on every turn of a long-lived claude session, not
// only the first: a subprocess-per-turn session launches the CLI again for
// each turn, and turn 2 resumes the conversation with --resume.
func TestClaudeLaunch_StrictMCPConfig_OnTurnTwo(t *testing.T) {
	fake := providertest.New(t, runtimes.Claude,
		providertest.Replay("claude/print_turn1"),
		providertest.Replay("claude/print_turn2_resume").When("--resume"),
	)
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, "claude-code")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "claude-code", RuntimeKind: "subprocess", PermissionMode: "acceptEdits"}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.Boot(ctx, agent.Options{TaskID: "CW-STRICT-T2", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeLongLived})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })
	require.Eventually(t, func() bool { return len(fake.Calls()) >= 1 && fake.Call(0).Exited }, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, cd.Manager.SendTurn(ctx, sess, "turn two"))
	require.Eventually(t, func() bool { return len(fake.Calls()) >= 2 }, 5*time.Second, 20*time.Millisecond)

	assertStrictOnce(t, fake.Call(0).Args)
	assertStrictOnce(t, fake.Call(1).Args)
	assert.True(t, fake.Call(1).HasArg("--resume"), "turn two is the resume: %q", fake.Call(1).Args)
}

// While Torque's state is write-protected (deps.MuxOmitsTorque, set by the
// protection of CW-20261001-0141), a profile's mux_servers lose `torque`: a
// `torque mcp` that mux spawns in the agent's sandbox cannot write its
// database. The other servers stay, and a grant of torque alone plants no mux
// at all, never the daemon's wider default.
func TestClaudeLaunch_MuxServersDropTorqueUnderProtection(t *testing.T) {
	grant := func(servers ...string) func(*composedDeps) {
		return func(cd *composedDeps) {
			withDaemonMux(cd)
			cd.Deps.MuxOmitsTorque = true
			prof := cd.Deps.Profiles.(config.ProfileMap)["worker"]
			prof.MuxServers = servers
			cd.Deps.Profiles = config.ProfileMap{"worker": prof}
		}
	}
	_, servers := bootClaude(t, "", "worker", agent.Options{}, grant("vanta", "torque"))
	assert.Equal(t,
		[]string{"mcp", "--proxy", "--token", "local-dev", "--scopes", "session.write,message.write", "--only", "vanta"},
		muxArgs(t, servers))

	_, servers = bootClaude(t, "", "worker", agent.Options{}, grant("torque"))
	assert.Contains(t, servers, "loopback", "the loopback still serves the task's Torque tools")
	assert.NotContains(t, servers, "mux", "nothing is left to plant, and the daemon's default is not substituted")
}
