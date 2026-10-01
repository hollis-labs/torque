package agent

import (
	"encoding/json"
	"log"

	"github.com/hollis-labs/agentkit/agentruntime/turn"
	"github.com/hollis-labs/agentkit/agentsessions"
	gopermission "github.com/hollis-labs/go-permission"

	"github.com/hollis-labs/torque/internal/config"
)

// codexApprovalMode maps a profile's permission_mode onto the posture
// agentkit's turn.CodexApprovalResponder answers Codex app-server approval
// requests with (CW-20261001-0055). Profiles spell the mode in Claude's
// settings vocabulary for every provider; go-permission spells the same four
// postures differently. What each grants, per the responder's table (Codex
// asks about a command or file change only when it would leave the sandbox):
//
//	default           → default       MCP tool calls approved, escalations declined
//	acceptEdits       → accept-edits  also file changes outside the writable roots
//	plan              → plan          everything declined
//	bypassPermissions → yolo          everything approved
//
// An unset mode resolves to acceptEdits through ResolvedPermissionMode, the
// same posture Claude gets. A value load-time validation would have rejected
// falls back to the responder's default posture, never to yolo.
func codexApprovalMode(profile config.AgentProfile) gopermission.Mode {
	switch profile.ResolvedPermissionMode() {
	case config.PermissionModeDefault:
		return gopermission.ModeDefault
	case config.PermissionModeAcceptEdits:
		return gopermission.ModeAcceptEdits
	case config.PermissionModePlan:
		return gopermission.ModePlan
	case config.PermissionModeBypass:
		return gopermission.ModeYolo
	default:
		return gopermission.ModeDefault
	}
}

// codexApprovalHook answers the approval requests a Codex app-server session
// sends before an MCP tool call or a sandbox escalation. Without a
// JsonRpcRequestHook agentsessions refuses every one with -32601, so Codex
// fails the action, and MCP tools Torque planted for the run were unusable.
// Each decision is logged with the session, so a declined action can be
// traced to the posture that declined it.
func codexApprovalHook(sessID string, profile config.AgentProfile) func(string, json.RawMessage) (any, *agentsessions.JsonRpcError) {
	responder := turn.CodexApprovalResponder{Mode: codexApprovalMode(profile)}
	return func(method string, params json.RawMessage) (any, *agentsessions.JsonRpcError) {
		out := responder.Decide(method, params)
		if out.Err != nil {
			log.Printf("agent.Boot: codex request session=%s method=%s refused: %s", sessID, method, out.Err.Message)
		} else {
			log.Printf("agent.Boot: codex approval session=%s mode=%s kind=%s allowed=%t (%s)", sessID, responder.Mode, out.Kind, out.Allowed, out.Reason)
		}
		return out.Response()
	}
}
