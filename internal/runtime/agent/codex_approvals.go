package agent

import (
	"encoding/json"
	"log"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/agentkit/agentruntime/turn"
	"github.com/hollis-labs/agentkit/agentsessions"
	gopermission "github.com/hollis-labs/go-permission"

	"github.com/hollis-labs/torque/internal/config"
)

// permissionPosture maps a profile's permission_mode onto go-permission's
// posture vocabulary, for the runtimes whose approval requests Torque
// answers itself: Codex app-server (codexApprovalHook) and ACP agents
// (acpPermissionResponder). Profiles spell the mode in Claude's settings
// vocabulary for every provider; go-permission spells the same four
// postures differently. An unset mode gets the default posture, not the
// acceptEdits that ResolvedPermissionMode gives Claude, and a value
// load-time validation would have rejected also falls back to default,
// never to yolo.
func permissionPosture(profile config.AgentProfile) gopermission.Mode {
	return permissionModeFor(config.PermissionMode(profile.PermissionMode))
}

// permissionModeFor maps a profile's permission_mode, which profiles spell in
// Claude's settings vocabulary, onto go-permission's Mode, the one spelling
// the launch plan (agentkit v0.17.0) and the approval responders take. A
// value load-time validation would have rejected falls back to default,
// never to yolo.
func permissionModeFor(mode config.PermissionMode) gopermission.Mode {
	switch mode {
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

// codexApprovalMode is the posture agentkit's turn.CodexApprovalResponder
// answers Codex app-server approval requests with (CW-20261001-0055). What
// each grants, per the responder's table (Codex asks about a command or file
// change only when it would leave the sandbox):
//
//	default           → default       MCP tool calls approved, escalations declined
//	acceptEdits       → accept-edits  also file changes outside the writable roots
//	plan              → plan          everything declined
//	bypassPermissions → yolo          everything approved
//
// Unset is default for Codex because a file-change approval is a write
// outside the writable roots, which an operator should opt into rather than
// inherit.
func codexApprovalMode(profile config.AgentProfile) gopermission.Mode {
	return permissionPosture(profile)
}

// plantsMux reports whether a session gets the daemon's `mux` MCP server
// (deps.MuxCommand), which proxies vanta, torque and cerberus, cerberus
// among them able to run commands and stop services on the host. Outside
// yolo only the run's own loopback is offered, structurally, wherever Torque
// cannot gate mux's tools by posture:
//
//   - Codex (CW-20261001-0110). Codex runs an MCP tool its server marks
//     readOnlyHint without asking, so the approval responder
//     (codexApprovalHook) never sees those calls; the live smoke had
//     mux_health run unasked under a default profile.
//   - Every ACP session (CW-20261001-0120). Whether an ACP agent asks
//     before running an MCP tool is unverified, and Torque answers no ACP
//     permission request yet (CW-20261001-0113).
//
// Both get mux only under an explicit bypassPermissions; unset and every
// other posture get the loopback alone. Claude and OpenCode on their native
// runtimes keep mux as before: their tool approvals do not have that bypass.
func plantsMux(profile config.AgentProfile, kind RuntimeKind) bool {
	if !kind.ACP() && runtimeIDFor(profile.Provider) != string(runtimes.Codex) {
		return true
	}
	return config.PermissionMode(profile.PermissionMode) == config.PermissionModeBypass
}

// codexLoopbackMCPServer is the [mcp_servers.<name>] key go-providers
// plants the run's own Torque loopback under in Codex's config.toml. The
// name is reserved there: a profile's MCP server spec cannot reuse it.
const codexLoopbackMCPServer = "loopback"

// codexApprovalHook answers the approval requests a Codex app-server session
// sends before an MCP tool call or a sandbox escalation. Without a
// JsonRpcRequestHook agentsessions refuses every one with -32601, so Codex
// fails the action, and MCP tools Torque planted for the run were unusable.
// Each decision is logged with the session, so a declined action can be
// traced to the posture that declined it.
//
// Outside yolo, an MCP tool call is approved only on the run's loopback
// server, which a worker needs to report its result: codexMCPAllow is the
// responder's allow-list (agentkit's CodexApprovalResponder.MCPAllow,
// CW-20261001-0124). The posture alone approves MCP tool calls from any
// server under default and accept-edits, and a session can also carry the
// planted `mux` server, whose tools reach cerberus (ssh, docker) and more;
// an unattended worker must not run those without a human. A call to any
// other server, or one that names none, is declined. plan declines every
// call and yolo approves every call, as before.
func codexApprovalHook(sessID string, profile config.AgentProfile) func(string, json.RawMessage) (any, *agentsessions.JsonRpcError) {
	responder := turn.CodexApprovalResponder{Mode: codexApprovalMode(profile), MCPAllow: codexMCPAllow}
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

// codexMCPAllow lists the MCP servers whose tool calls a Codex session's
// default and accept-edits postures approve: the run's own loopback alone.
// plantsMux keeps mux out of those sessions' config as well, since Codex
// runs a tool its server marks read-only without asking, so this list never
// sees those calls.
var codexMCPAllow = []string{codexLoopbackMCPServer}
