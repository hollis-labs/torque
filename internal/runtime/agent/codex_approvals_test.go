package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/agentkit/agentruntime/turn"
	gopermission "github.com/hollis-labs/go-permission"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
)

func TestCodexApprovalMode_MapsEveryPermissionMode(t *testing.T) {
	cases := []struct {
		permissionMode string
		want           gopermission.Mode
	}{
		{"default", gopermission.ModeDefault},
		{"acceptEdits", gopermission.ModeAcceptEdits},
		{"plan", gopermission.ModePlan},
		{"bypassPermissions", gopermission.ModeYolo},
		// Unset is the responder's headless default: neither the acceptEdits
		// Claude resolves to (which for Codex approves writes outside the
		// writable roots) nor yolo.
		{"", gopermission.ModeDefault},
		// Load-time validation rejects this; a profile built in code that
		// skips validation still gets the responder's default, not yolo.
		{"dontAsk", gopermission.ModeDefault},
	}
	for _, tc := range cases {
		t.Run("permission_mode="+tc.permissionMode, func(t *testing.T) {
			got := codexApprovalMode(config.AgentProfile{Provider: "codex", PermissionMode: tc.permissionMode})
			assert.Equal(t, tc.want, got)
			assert.NoError(t, turn.CodexApprovalResponder{Mode: got, MCPAllow: codexMCPAllow}.Validate())
		})
	}
}

// The hook answers each approval kind per the posture, and still refuses a
// request that is not an approval.
func TestCodexApprovalHook_AnswersPerPosture(t *testing.T) {
	mcpToolCall := json.RawMessage(`{"serverName":"loopback","_meta":{"codex_approval_kind":"mcp_tool_call"}}`)
	requests := []struct {
		name, method string
		params       json.RawMessage
	}{
		{"mcp_tool_call", turn.CodexElicitationMethod, mcpToolCall},
		{"file_change", turn.CodexFileChangeApprovalMethod, json.RawMessage(`{}`)},
		{"command", turn.CodexCommandApprovalMethod, json.RawMessage(`{}`)},
	}
	// allowed[mode] lists, in request order, whether each is granted.
	allowed := map[string][3]bool{
		"default":           {true, false, false},
		"acceptEdits":       {true, true, false},
		"":                  {true, false, false},
		"plan":              {false, false, false},
		"bypassPermissions": {true, true, true},
	}
	for mode, want := range allowed {
		hook := codexApprovalHook("SES-TEST", config.AgentProfile{Provider: "codex", PermissionMode: mode})
		for i, req := range requests {
			result, rpcErr := hook(req.method, req.params)
			require.Nil(t, rpcErr, "mode %q %s: an approval request must get a decision, not an error", mode, req.name)
			raw, err := json.Marshal(result)
			require.NoError(t, err)
			var decision struct {
				Decision string `json:"decision"`
				Action   string `json:"action"`
			}
			require.NoError(t, json.Unmarshal(raw, &decision))
			granted := decision.Decision == "accept" || decision.Action == "accept"
			assert.Equal(t, want[i], granted, "mode %q %s: got %s", mode, req.name, raw)
		}
	}

	hook := codexApprovalHook("SES-TEST", config.AgentProfile{Provider: "codex"})
	_, rpcErr := hook("item/tool/requestUserInput", json.RawMessage(`{}`))
	require.NotNil(t, rpcErr, "a request the responder cannot decide for a human is still refused")
}

// Outside yolo an MCP tool call is approved only on the run's loopback
// server. The planted mux server reaches cerberus (ssh, docker) and more, so
// default, accept-edits and an unset mode decline it, as they do any other
// or unnamed server. plan declines everything; yolo (bypassPermissions)
// approves everything.
func TestCodexApprovalHook_MCPToolCallsOnlyOnLoopback(t *testing.T) {
	servers := []string{"loopback", "mux", "torque", "torque_loopback", ""}
	want := map[string]map[string]bool{
		"":                  {"loopback": true},
		"default":           {"loopback": true},
		"acceptEdits":       {"loopback": true},
		"plan":              {},
		"bypassPermissions": {"loopback": true, "mux": true, "torque": true, "torque_loopback": true, "": true},
	}
	for mode, allowed := range want {
		hook := codexApprovalHook("SES-TEST", config.AgentProfile{Provider: "codex", PermissionMode: mode})
		for _, server := range servers {
			params := json.RawMessage(`{"serverName":"` + server + `","_meta":{"codex_approval_kind":"mcp_tool_call"}}`)
			result, rpcErr := hook(turn.CodexElicitationMethod, params)
			require.Nil(t, rpcErr, "mode %q server %q: an MCP tool-call approval gets a decision", mode, server)
			raw, err := json.Marshal(result)
			require.NoError(t, err)
			var answer struct {
				Action string `json:"action"`
			}
			require.NoError(t, json.Unmarshal(raw, &answer))
			wantAction := "decline"
			if allowed[server] {
				wantAction = "accept"
			}
			assert.Equal(t, wantAction, answer.Action, "mode %q server %q", mode, server)
		}
	}
}

// plantsMux withholds mux short of an explicit bypassPermissions from codex
// sessions (CW-20261001-0110) and from every ACP session (CW-20261001-0120),
// and from a Claude session unless its profile names mux_servers
// (CW-20261001-0226); OpenCode on its native runtime is unchanged.
func TestPlantsMux(t *testing.T) {
	granted := []string{"vanta", "tesseract"}
	for _, tc := range []struct {
		provider string
		kind     RuntimeKind
		mode     string
		servers  []string
		want     bool
	}{
		{"codex", RuntimeKindJsonRpcStdio, "", nil, false},
		{"codex", RuntimeKindJsonRpcStdio, "default", nil, false},
		{"codex", RuntimeKindJsonRpcStdio, "acceptEdits", nil, false},
		{"codex", RuntimeKindJsonRpcStdio, "plan", nil, false},
		{"codex", RuntimeKindJsonRpcStdio, "dontAsk", nil, false},
		{"codex", RuntimeKindJsonRpcStdio, "bypassPermissions", nil, true},
		{"codex", RuntimeKindJsonRpcStdio, "acceptEdits", granted, false},
		{"codex", RuntimeKindJsonRpcStdio, "bypassPermissions", granted, true},
		// Claude: none by default, whatever the posture or runtime kind.
		{"claude-code", RuntimeKindStreamingStdio, "", nil, false},
		{"claude-code", RuntimeKindStreamingStdio, "plan", nil, false},
		{"claude-code", RuntimeKindStreamingStdio, "bypassPermissions", nil, false},
		{"claude-code", RuntimeKindSubprocess, "", nil, false},
		{"claude-code", "", "", nil, false},
		{"Claude-Code", RuntimeKindStreamingStdio, "", nil, false},
		// Claude: what a profile names is planted, in any posture or kind.
		{"claude-code", RuntimeKindStreamingStdio, "", granted, true},
		{"claude-code", RuntimeKindStreamingStdio, "plan", granted, true},
		{"claude-code", RuntimeKindSubprocess, "acceptEdits", granted, true},
		{"claude-code", "", "", granted, true},
		{"Claude-Code", RuntimeKindStreamingStdio, "", granted, true},
		{"opencode", RuntimeKindSubprocess, "", nil, true},
		{"opencode", RuntimeKindSubprocess, "", granted, true},
		{"copilot", RuntimeKindACPStdio, "", nil, false},
		{"copilot", RuntimeKindACPStdio, "default", nil, false},
		{"copilot", RuntimeKindACPStdio, "acceptEdits", nil, false},
		{"copilot", RuntimeKindACPStdio, "plan", nil, false},
		{"copilot", RuntimeKindACPTCP, "bypassPermissions", nil, true},
		{"copilot", RuntimeKindACPStdio, "bypassPermissions", nil, true},
		{"copilot", RuntimeKindACPStdio, "acceptEdits", granted, false},
		{"claude-code", RuntimeKindACPStdio, "acceptEdits", nil, false},
		{"claude-code", RuntimeKindACPStdio, "acceptEdits", granted, false},
		{"opencode", RuntimeKindACPStdio, "", nil, false},
		{"pi", RuntimeKindACPStdio, "bypassPermissions", nil, true},
	} {
		got := plantsMux(config.AgentProfile{Provider: tc.provider, PermissionMode: tc.mode, MuxServers: tc.servers}, tc.kind)
		assert.Equal(t, tc.want, got, "%s/%s/%q/%v", tc.provider, tc.kind, tc.mode, tc.servers)
	}
}

// A profile's mux_servers narrows the planted mux argv to exactly those
// servers; the daemon's other args (token, scopes) stay, and a daemon argv
// with no --servers gets one.
func TestMuxArgsFor(t *testing.T) {
	base := []string{"mcp", "--proxy", "--servers", "vanta,torque,cerberus", "--token", "local-dev", "--scopes", "session.write"}
	named := config.AgentProfile{MuxServers: []string{"vanta", "tesseract"}}

	assert.Equal(t, base, muxArgsFor(base, config.AgentProfile{}), "unset: the daemon's own set stands")
	assert.Equal(t,
		[]string{"mcp", "--proxy", "--servers", "vanta,tesseract", "--token", "local-dev", "--scopes", "session.write"},
		muxArgsFor(base, named))
	assert.Equal(t, []string{"mcp", "--servers=vanta,tesseract"}, muxArgsFor([]string{"mcp", "--servers=torque"}, named), "the = spelling")
	assert.Equal(t, []string{"mcp", "--proxy", "--servers", "vanta,tesseract"}, muxArgsFor([]string{"mcp", "--proxy"}, named), "none in the daemon's argv: appended")
	assert.Equal(t, []string{"mcp", "--servers", "vanta,tesseract"}, muxArgsFor([]string{"mcp", "--servers"}, named), "a dangling --servers is not a value")
	assert.Equal(t, []string{"torque"}, strings.Split(muxArgsFor(base, config.AgentProfile{MuxServers: []string{"torque"}})[3], ","))

	// cerberus is only ever in the planted set when the profile names it.
	assert.NotContains(t, muxArgsFor(base, named)[3], "cerberus")
	assert.Contains(t, muxArgsFor(base, config.AgentProfile{MuxServers: []string{"vanta", "cerberus"}})[3], "cerberus")
	assert.Equal(t, "vanta,torque,cerberus", base[3], "the daemon's argv is not modified")
}

// The launch plan's Provider.Permission is a go-permission Mode since
// agentkit v0.17.0, which refuses Claude's own spellings; Torque sets it for
// claude-code only (CW-20261001-0157).
func TestResolveLaunchPermissionMode(t *testing.T) {
	for _, tc := range []struct {
		profile config.AgentProfile
		want    gopermission.Mode
	}{
		{config.AgentProfile{Provider: "claude-code"}, gopermission.ModeAcceptEdits},
		{config.AgentProfile{Provider: "claude-code", PermissionMode: "default"}, gopermission.ModeDefault},
		{config.AgentProfile{Provider: "claude-code", PermissionMode: "acceptEdits"}, gopermission.ModeAcceptEdits},
		{config.AgentProfile{Provider: "claude-code", PermissionMode: "plan"}, gopermission.ModePlan},
		{config.AgentProfile{Provider: "claude-code", PermissionMode: "bypassPermissions"}, gopermission.ModeYolo},
		{config.AgentProfile{Provider: "claude-code", Args: []string{"--dangerously-skip-permissions"}}, gopermission.ModeYolo},
		{config.AgentProfile{Provider: "codex", PermissionMode: "bypassPermissions"}, ""},
		{config.AgentProfile{Provider: "opencode", PermissionMode: "plan"}, ""},
		{config.AgentProfile{Provider: "agy", PermissionMode: "acceptEdits"}, ""},
	} {
		assert.Equal(t, tc.want, resolveLaunchPermissionMode(tc.profile), "%+v", tc.profile)
	}
}
