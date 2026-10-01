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
		// Unset is acceptEdits (ResolvedPermissionMode), never yolo.
		{"", gopermission.ModeAcceptEdits},
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
	mcpToolCall := json.RawMessage(`{"_meta":{"codex_approval_kind":"mcp_tool_call"}}`)
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
		"":                  {true, true, false},
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
