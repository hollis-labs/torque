package agent

import (
	"encoding/json"
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
			assert.NoError(t, turn.CodexApprovalResponder{Mode: got}.Validate())
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
