package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/harness/adapters/acp"
	gopermission "github.com/hollis-labs/substrate/harness/interception/permission"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
)

// copilotOptions is the option set Copilot offers (go-providers'
// copilot/acp_permission fixture).
var copilotOptions = []acp.PermissionOption{
	{OptionID: "allow_once", Name: "Allow once", Kind: acp.PermissionAllowOnce},
	{OptionID: "allow_always", Name: "Always allow", Kind: acp.PermissionAllowAlways},
	{OptionID: "reject_once", Name: "Reject", Kind: acp.PermissionRejectOnce},
}

// CW-20261001-0113: posture × ACP tool kind → the option selected. A grant
// is always allow_once; a decline is reject_once.
func TestDecideACPPermission(t *testing.T) {
	kinds := []string{"read", "search", "think", "edit", "delete", "move", "execute", "fetch", "other", "switch_mode", ""}
	granted := map[gopermission.Mode][]string{
		gopermission.ModePlan:        nil,
		gopermission.ModeDefault:     {"read", "search", "think"},
		gopermission.ModeAcceptEdits: {"read", "search", "think", "edit"},
		gopermission.ModeYolo:        kinds,
	}
	for posture, grants := range granted {
		for _, kind := range kinds {
			want := "reject_once"
			for _, g := range grants {
				if g == kind {
					want = "allow_once"
				}
			}
			sel, _, reason := decideACPPermission(posture, acp.PermissionRequest{
				ToolCall: acp.PermissionToolCall{ToolCallID: "c1", Kind: kind},
				Options:  copilotOptions,
			})
			assert.Equal(t, want, sel.OptionID, "posture %s kind %q (%s)", posture, kind, reason)
		}
	}
}

func TestDecideACPPermission_OptionsTheAgentOffers(t *testing.T) {
	read := acp.PermissionToolCall{ToolCallID: "c1", Kind: "read"}
	exec := acp.PermissionToolCall{ToolCallID: "c2", Kind: "execute"}

	// A grant never takes allow_always: without allow_once it declines.
	sel, chosen, reason := decideACPPermission(gopermission.ModeYolo, acp.PermissionRequest{ToolCall: exec, Options: []acp.PermissionOption{
		{OptionID: "always", Kind: acp.PermissionAllowAlways},
		{OptionID: "no", Kind: acp.PermissionRejectOnce},
	}})
	assert.Equal(t, "no", sel.OptionID)
	assert.Equal(t, acp.PermissionRejectOnce, chosen)
	assert.Contains(t, reason, "offered no allow_once")

	// Option ids are the agent's own; only the kind decides.
	sel, _, _ = decideACPPermission(gopermission.ModeDefault, acp.PermissionRequest{ToolCall: read, Options: []acp.PermissionOption{
		{OptionID: "proceed-1", Kind: acp.PermissionAllowOnce},
	}})
	assert.Equal(t, "proceed-1", sel.OptionID)

	// No reject_once to pick: ACP's cancelled outcome (the zero selection),
	// never reject_always.
	sel, chosen, _ = decideACPPermission(gopermission.ModeDefault, acp.PermissionRequest{ToolCall: exec, Options: []acp.PermissionOption{
		{OptionID: "allow", Kind: acp.PermissionAllowOnce},
		{OptionID: "never", Kind: acp.PermissionRejectAlways},
	}})
	assert.Equal(t, acp.PermissionSelection{}, sel)
	assert.Equal(t, acp.PermissionOptionKind("cancelled"), chosen)
}

// Every decision lands in the session log with the kind, the tool and the
// option chosen; the raw input does not.
func TestACPPermissionResponder_LogsEachDecision(t *testing.T) {
	var log strings.Builder
	respond := acpPermissionResponder(permissionPosture(config.AgentProfile{}), &log)
	sel, err := respond(context.Background(), acp.PermissionRequest{
		ToolCall: acp.PermissionToolCall{ToolCallID: "c1", Title: "Run shell command: pwd", Kind: "execute", RawInput: []byte(`{"command":"pwd","token":"s3cret"}`)},
		Options:  copilotOptions,
	})
	require.NoError(t, err)
	assert.Equal(t, "reject_once", sel.OptionID, "an unset permission_mode is the default posture")
	assert.Equal(t, `acp permission: kind=execute tool="Run shell command: pwd" posture=default option=reject_once (execute is not granted under default)`+"\n", log.String())
	assert.NotContains(t, log.String(), "s3cret")
}
