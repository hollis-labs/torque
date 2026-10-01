package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-agent-wrapper/acp"
	"github.com/hollis-labs/go-agent-wrapper/adapters"
	"github.com/hollis-labs/go-agent-wrapper/launch"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	runtimeevents "github.com/hollis-labs/go-runtime-events/runtimeevents"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
)

// TestSelectRuntime_NativeRuntimes pins CW-20260930-0134: every runtime the
// registry has a native launch factory for is selected through
// launch.Select in the mode Torque resolved, with Torque's adapter (and its
// profile options) inside the wrapper adapter.
func TestSelectRuntime_NativeRuntimes(t *testing.T) {
	cases := []struct {
		provider string
		kind     RuntimeKind
		runtime  runtimes.ID
		check    func(t *testing.T, cli provider.CLIAdapter)
	}{
		{"claude-code", RuntimeKindStreamingStdio, runtimes.Claude, func(t *testing.T, cli provider.CLIAdapter) {
			a := cli.(*provider.ClaudeAdapter)
			assert.Equal(t, "stream-json", a.InputMode)
			assert.Equal(t, string(config.PermissionModePlan), a.PermissionMode)
		}},
		{"claude-code", RuntimeKindSubprocess, runtimes.Claude, func(t *testing.T, cli provider.CLIAdapter) {
			assert.Empty(t, cli.(*provider.ClaudeAdapter).InputMode, "subprocess-per-turn is claude print mode")
		}},
		{"codex", RuntimeKindJsonRpcStdio, runtimes.Codex, func(t *testing.T, cli provider.CLIAdapter) {
			assert.Equal(t, "app-server", cli.(*provider.CodexAdapter).Mode)
		}},
		{"codex", RuntimeKindSubprocess, runtimes.Codex, func(t *testing.T, cli provider.CLIAdapter) {
			assert.NotEqual(t, "app-server", cli.(*provider.CodexAdapter).Mode, "an explicit subprocess kind must not get the registry's jsonrpc-stdio default")
		}},
		{"opencode", RuntimeKindSubprocess, runtimes.OpenCode, func(t *testing.T, cli provider.CLIAdapter) {
			a := cli.(*provider.OpencodeAdapter)
			assert.Equal(t, "worker", a.Agent)
			assert.Equal(t, "m/x", a.Model)
		}},
		{"opencode", RuntimeKindServeHTTP, runtimes.OpenCode, func(t *testing.T, cli provider.CLIAdapter) {
			assert.Equal(t, "serve-http", cli.(*provider.OpencodeAdapter).Mode)
		}},
		{"agy", RuntimeKindSubprocess, runtimes.Antigravity, func(t *testing.T, cli provider.CLIAdapter) {
			a := cli.(*provider.AntigravityAdapter)
			assert.Equal(t, "m/x", a.Model)
			assert.Equal(t, "plan", a.Permission)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.provider+"/"+string(tc.kind), func(t *testing.T) {
			profile := config.AgentProfile{Provider: tc.provider, Model: "m/x", PermissionMode: string(config.PermissionModePlan)}
			sel, err := selectRuntime(profile, "worker", tc.kind)
			require.NoError(t, err)
			tc.check(t, sel.cli)
			require.NotNil(t, sel.wrapper)
			assert.Equal(t, string(tc.runtime), sel.wrapper.Name())
			ra, ok := sel.wrapper.(adapters.RuntimeAdapter)
			require.True(t, ok, "a native selection is a RuntimeAdapter")
			assert.Same(t, sel.cli, ra.CLIAdapter(), "the wrapper drives Torque's configured adapter, unwrapped")
		})
	}
}

func TestSelectRuntime_Refusals(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		kind     RuntimeKind
		want     string
	}{
		{"bare claude stays retired", "claude", RuntimeKindStreamingStdio, "bare claude provider retired"},
		{"empty provider", "", RuntimeKindSubprocess, "profile has empty provider"},
		{"unknown provider", "gemini", RuntimeKindSubprocess, `unknown provider "gemini"`},
		{"mode the registry lacks", "opencode", RuntimeKindJsonRpcStdio, `does not support runtime kind "jsonrpc-stdio"`},
		{"ACP mode the registry lacks", "pi", RuntimeKindACPTCP, `does not support runtime kind "acp-tcp"; supported: acp-stdio`},
		{"claude PTY has no launch factory", "claude-code", RuntimeKindPTY, "unsupported selection"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := selectRuntime(config.AgentProfile{Provider: tc.provider}, "worker", tc.kind)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
	_, err := selectRuntime(config.AgentProfile{Provider: "claude-code"}, "worker", RuntimeKindPTY)
	assert.True(t, errors.Is(err, launch.ErrUnsupportedSelection))
}

// TestSelectRuntime_ACP pins CW-20261001-0097: an ACP mode selects the
// wrapper's ACP client adapter through launch.Select with no go-providers
// adapter, for the ACP-only runtimes and for the native runtimes' acp-stdio.
func TestSelectRuntime_ACP(t *testing.T) {
	cases := []struct {
		provider string
		kind     RuntimeKind
		runtime  runtimes.ID
	}{
		{"copilot", RuntimeKindACPStdio, runtimes.Copilot},
		{"copilot", RuntimeKindACPTCP, runtimes.Copilot},
		{"copilot-cli", RuntimeKindACPStdio, runtimes.Copilot},
		{"pi", RuntimeKindACPStdio, runtimes.Pi},
		{"pi-acp", RuntimeKindACPStdio, runtimes.Pi},
		{"claude-code", RuntimeKindACPStdio, runtimes.Claude},
		{"codex", RuntimeKindACPStdio, runtimes.Codex},
		{"opencode", RuntimeKindACPStdio, runtimes.OpenCode},
	}
	for _, tc := range cases {
		t.Run(tc.provider+"/"+string(tc.kind), func(t *testing.T) {
			sel, err := selectRuntime(config.AgentProfile{Provider: tc.provider}, "worker", tc.kind)
			require.NoError(t, err)
			assert.Nil(t, sel.cli, "an ACP mode has no go-providers adapter")
			require.NotNil(t, sel.wrapper)
			_, ok := sel.wrapper.(acp.ClientAdapter)
			assert.True(t, ok, "an ACP selection is an acp.ClientAdapter")
			desc := sel.wrapper.Describe()
			assert.Equal(t, adapters.ProtocolACP, desc.Protocol)
			assert.Equal(t, string(tc.runtime), desc.Provider)
			assert.True(t, sel.caps.ProviderSessionID, "session/new's sessionId is the provider session id")
		})
	}
}

func TestACPTaskDispatchRefusal(t *testing.T) {
	// Pi's bridge never passes MCP servers on, so it is refused up front.
	assert.Contains(t, acpTaskDispatchRefusal("pi", RuntimeKindACPStdio), "pi-acp cannot reach Torque's loopback MCP")
	assert.Contains(t, acpTaskDispatchRefusal("pi-acp", RuntimeKindACPStdio), "pi-acp cannot reach Torque's loopback MCP")
	// Every other ACP agent is judged at launch, from its mcpCapabilities.
	for _, provider := range []string{"copilot", "claude-code", "codex", "opencode"} {
		assert.Empty(t, acpTaskDispatchRefusal(provider, RuntimeKindACPStdio), provider)
	}
	assert.Empty(t, acpTaskDispatchRefusal("copilot", RuntimeKindACPTCP))
	// Native kinds are never refused here.
	assert.Empty(t, acpTaskDispatchRefusal("claude-code", RuntimeKindStreamingStdio))
}

func TestSelectRuntime_ClaudeDevModeAndAntigravityPosture(t *testing.T) {
	sel, err := selectRuntime(config.AgentProfile{Provider: "claude-code", Args: []string{"--dangerously-skip-permissions"}}, "w", RuntimeKindStreamingStdio)
	require.NoError(t, err)
	a := sel.cli.(*provider.ClaudeAdapter)
	assert.True(t, a.SkipPermissions)
	assert.Empty(t, a.PermissionMode, "dev mode leaves PermissionMode to the SkipPermissions plant")

	for mode, want := range map[string]string{
		"":                  "accept-edits", // unset resolves to Torque's acceptEdits default
		"acceptEdits":       "accept-edits",
		"bypassPermissions": "bypass",
		"plan":              "plan",
		"default":           "",
	} {
		sel, err := selectRuntime(config.AgentProfile{Provider: "antigravity", PermissionMode: mode}, "w", RuntimeKindSubprocess)
		require.NoError(t, err)
		assert.Equal(t, want, sel.cli.(*provider.AntigravityAdapter).Permission, "permission_mode %q", mode)
	}
}

// go-agent-wrapper v0.15.0 sessions emit kinds the sink has no translation
// for (agent.tool_result, agent.subagent_spawn, permission and policy
// events). They must be ignored, not fail the session or reach the stream.
func TestWrapperSink_IgnoresUntranslatedKinds(t *testing.T) {
	fanout := make(chan llmtypes.StreamEvent, 8)
	s := &torqueRuntimeEventSink{fanout: fanout}
	for _, kind := range []runtimeevents.EventKind{
		runtimeevents.KindAgentToolResult,
		runtimeevents.KindAgentSubagentSpawn,
		runtimeevents.KindAgentPermissionRequested,
		runtimeevents.KindAgentPermissionResolved,
		runtimeevents.KindPolicyBlock,
		runtimeevents.KindSessionIdle,
		// go-agent-wrapper v0.17.0's kinds, from agentkit v0.14's typed
		// events on every per-turn turn: Torque has no handler for them yet.
		runtimeevents.KindAgentPermissionDenied,
		runtimeevents.KindSessionAuthFailed,
		runtimeevents.KindSessionLost,
		runtimeevents.EventKind("agent.something_new"),
	} {
		require.NoError(t, s.Write(context.Background(), runtimeevents.Event{Kind: kind, Payload: json.RawMessage(`{"x":1}`)}), "kind %s", kind)
	}
	close(fanout)
	for ev := range fanout {
		t.Fatalf("untranslated kind produced a stream event: %+v", ev)
	}
}

type detectStub struct {
	provider.CLIAdapter
	path string
	ok   bool
}

func (d detectStub) Detect() (string, bool) { return d.path, d.ok }

// detectedBinary pins only an absolute Detect result (CW-20261001-0098); a
// miss or a bare name leaves the launch on its bare-name fallback.
func TestDetectedBinary(t *testing.T) {
	assert.Equal(t, "/opt/agents/opencode", detectedBinary(detectStub{path: "/opt/agents/opencode", ok: true}))
	assert.Empty(t, detectedBinary(detectStub{path: "opencode", ok: true}), "a bare name is not pinned")
	assert.Empty(t, detectedBinary(detectStub{ok: false}))
	assert.Empty(t, detectedBinary(nil))
}
