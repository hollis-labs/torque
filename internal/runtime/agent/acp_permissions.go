package agent

import (
	"context"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/hollis-labs/go-agent-wrapper/acp"
	gopermission "github.com/hollis-labs/go-permission"
)

// ACP permission requests (CW-20261001-0113). An ACP agent that chooses to
// ask sends session/request_permission with the tool call's kind (ACP's
// ToolKind) and the options it offers; go-agent-wrapper hands it to
// Config.ACPBestEffortPermissionRequestResponder. Without one every request
// was declined, whatever the profile's permission_mode. The answer follows
// the profile's posture, as Codex app-server's does (#145):
//
//	plan         nothing
//	default      read-only kinds: read, search, think
//	accept-edits those and edit
//	yolo         everything (bypassPermissions)
//
// accept-edits grants `edit` alone. ACP classes delete and move as kinds of
// their own, not edits; a delete cannot be undone and a move can overwrite,
// and Claude's acceptEdits does not grant either. execute and fetch, and
// every other or unknown kind (other, switch_mode, a new one), are granted
// only under yolo.
//
// A grant always selects the agent's allow_once option, never allow_always,
// so no grant outlives the call; an agent that offers no allow_once is
// declined. A decline selects the agent's reject_once when offered, and
// otherwise answers ACP's cancelled outcome. The wrapper calls this
// "best-effort": an agent may run some operations without asking, so it is
// not an execution gate.

// acpReadOnlyToolKinds are the ACP tool kinds every posture short of plan
// grants.
var acpReadOnlyToolKinds = map[string]bool{"read": true, "search": true, "think": true}

// acpPostureGrants reports whether posture grants a tool call of ACP kind.
func acpPostureGrants(posture gopermission.Mode, kind string) bool {
	switch posture {
	case gopermission.ModeYolo:
		return true
	case gopermission.ModePlan:
		return false
	case gopermission.ModeAcceptEdits:
		return acpReadOnlyToolKinds[kind] || kind == "edit"
	default:
		return acpReadOnlyToolKinds[kind]
	}
}

// decideACPPermission picks the option to answer req with under posture,
// and says why. A zero selection is ACP's cancelled outcome.
func decideACPPermission(posture gopermission.Mode, req acp.PermissionRequest) (acp.PermissionSelection, acp.PermissionOptionKind, string) {
	kind := req.ToolCall.Kind
	if kind == "" {
		kind = "unknown"
	}
	if !acpPostureGrants(posture, req.ToolCall.Kind) {
		sel, chosen := acpRejectOnce(req)
		return sel, chosen, fmt.Sprintf("%s is not granted under %s", kind, posture)
	}
	for _, o := range req.Options {
		if o.Kind == acp.PermissionAllowOnce {
			return acp.SelectPermissionOption(o.OptionID), o.Kind, fmt.Sprintf("%s is granted under %s", kind, posture)
		}
	}
	sel, chosen := acpRejectOnce(req)
	return sel, chosen, fmt.Sprintf("%s is granted under %s, but the agent offered no allow_once", kind, posture)
}

// acpRejectOnce declines with the agent's reject_once option, or with ACP's
// cancelled outcome when it offers none.
func acpRejectOnce(req acp.PermissionRequest) (acp.PermissionSelection, acp.PermissionOptionKind) {
	for _, o := range req.Options {
		if o.Kind == acp.PermissionRejectOnce {
			return acp.SelectPermissionOption(o.OptionID), o.Kind
		}
	}
	return acp.PermissionSelection{}, "cancelled"
}

// acpPermissionResponder answers an ACP session's permission requests from
// posture and writes each decision to log (the session log). The tool's
// raw input is never logged; its title can carry a command, so it is
// shortened.
func acpPermissionResponder(posture gopermission.Mode, log io.Writer) acp.BestEffortPermissionRequestResponder {
	return func(_ context.Context, req acp.PermissionRequest) (acp.PermissionSelection, error) {
		sel, chosen, reason := decideACPPermission(posture, req)
		tool := req.ToolCall.Name
		if tool == "" {
			tool = req.ToolCall.Title
		}
		_, _ = fmt.Fprintf(log, "acp permission: kind=%s tool=%q posture=%s option=%s (%s)\n",
			req.ToolCall.Kind, shortenForLog(tool, 120), posture, chosen, reason)
		return sel, nil
	}
}

func shortenForLog(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}
