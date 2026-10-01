package agent

import (
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"

	"github.com/hollis-labs/torque/internal/config"
)

// plantsMux reports whether a session gets the daemon's `mux` MCP server
// (deps.MuxCommand), which proxies vanta, torque and cerberus by default,
// cerberus among them able to run commands and stop services on the host.
//
//   - Claude (CW-20261001-0226), on every runtime kind: only when the profile
//     grants it by name (mux_servers). A Claude worker's planted set is the
//     run's loopback alone by default, since cerberus is the riskiest server
//     and the worker sessions never called mux. Native Claude also loads no MCP
//     server Torque did not plant (--strict-mcp-config), so a profile's
//     mux_servers is all of the mux surface its sessions reach. A Claude
//     session over ACP is still held to bypassPermissions, below.
//   - Codex (CW-20261001-0110) and every ACP session (CW-20261001-0120):
//     outside yolo only the run's own loopback is offered, structurally,
//     wherever Torque cannot gate mux's tools by posture. Codex runs an MCP
//     tool its server marks readOnlyHint without asking, so the approval
//     responder (codexApprovalHook) never sees those calls; the live smoke had
//     mux_health run unasked under a default profile. Whether an ACP agent
//     asks before running an MCP tool is unverified, and Torque answers no ACP
//     permission request yet (CW-20261001-0113). Both get mux only under an
//     explicit bypassPermissions; unset and every other posture get the
//     loopback alone. Their mux_servers, when set, narrow it.
//   - OpenCode on its native runtime keeps mux as before: its tool approvals
//     do not have that bypass.
//
// muxArgsFor says which servers a session that gets mux gets.
func plantsMux(profile config.AgentProfile, kind RuntimeKind) bool {
	runtime := runtimeIDFor(profile.Provider)
	if runtime == string(runtimes.Claude) && len(profile.MuxServers) == 0 {
		return false
	}
	if kind.ACP() || runtime == string(runtimes.Codex) {
		return config.PermissionMode(profile.PermissionMode) == config.PermissionModeBypass
	}
	return true
}

// muxArgsFor is the argv the planted mux entry runs for a session granted
// servers: the daemon's MuxArgs (deps.MuxArgs: the interactive shape, or
// TORQUE_MUX_ARGS) curated to exactly those servers with `--only`.
//
// `--only` is what restricts mux. `--servers` only picks which upstream
// tools mux surfaces as native tools: under it mux still registers
// mux_discover and mux_call, which route through every server in its
// catalog, and its own Tether mux_* tools (session launch, send input,
// message send) stay on the planted token and scopes. `--only` is mux's
// curated mode: only those upstream servers' tools, and none of that.
// mux refuses `--only` together with `--servers` or `--broker`, and without
// `--proxy`, so the daemon's own `--servers` and `--only` (with their
// values) and `--broker` are dropped, `--proxy` is kept or added, and the
// rest of the daemon's args (token, scopes) stay.
func muxArgsFor(base []string, servers []string) []string {
	if len(servers) == 0 {
		return slices.Clone(base)
	}
	return muxOnlyArgs(base, servers)
}

// muxOnlyArgs is base with its server selection replaced by `--only
// <servers>`, as muxArgsFor describes.
func muxOnlyArgs(base []string, servers []string) []string {
	args := make([]string, 0, len(base)+3)
	proxy := false
	for i := 0; i < len(base); i++ {
		flag, value, inline := strings.Cut(base[i], "=")
		switch flag {
		case "--servers", "--only":
			// The flag's value is the next token, unless that is a flag
			// (a dangling one).
			if !inline && i+1 < len(base) && !strings.HasPrefix(base[i+1], "-") {
				i++
			}
			continue
		case "--broker":
			continue
		case "--proxy":
			if inline && value == "false" {
				continue
			}
			proxy = true
		}
		args = append(args, base[i])
	}
	if !proxy {
		args = append(args, "--proxy")
	}
	return append(args, "--only", strings.Join(servers, ","))
}

// muxPlan is what mux a session gets: whether to plant it, with what argv,
// and what to log about the decision.
type muxPlan struct {
	Plant bool
	Args  []string
	// Why is the reason mux is not planted; Warn says it deserves a WARN
	// (a grant that cannot be honoured), not just the routine line.
	Why  string
	Warn bool
	// Notes are warnings about how a planted mux differs from the profile.
	Notes []string
}

// planMux decides the mux a session of profile gets on a runtime of kind:
// plantsMux says whether, the profile's mux_servers say which servers, and
// the daemon's state can narrow that.
//
//   - A daemon with no mux binary plants nothing. A profile that names
//     mux_servers is told, since it asked for a grant that cannot be made.
//   - While Torque write-protects its state (deps.MuxOmitsTorque) the planted
//     mux carries no torque server, since a `torque mcp` mux would spawn in
//     the agent's sandbox cannot write its database. The daemon's own server
//     list is narrowed at startup; a profile's mux_servers are narrowed here,
//     with a note, and a profile whose servers all drop gets no mux, never the
//     daemon's wider default.
func planMux(deps *Dependencies, profile config.AgentProfile, kind RuntimeKind) muxPlan {
	var plan muxPlan
	if !plantsMux(profile, kind) {
		plan.Why = muxNotPlantedReason(profile)
		return plan
	}
	if deps == nil || deps.MuxCommand == "" {
		if len(profile.MuxServers) > 0 {
			plan.Why = fmt.Sprintf("the profile names mux_servers %v but the daemon has no mux server to plant (mux is not installed or resolved, or was withheld while Torque's state is write-protected)", profile.MuxServers)
			plan.Warn = true
		}
		return plan
	}
	servers := slices.Clone(profile.MuxServers)
	if deps.MuxOmitsTorque && slices.Contains(servers, "torque") {
		servers = slices.DeleteFunc(servers, func(s string) bool { return s == "torque" })
		plan.Notes = append(plan.Notes, "torque is dropped from the profile's mux_servers: while Torque's state is write-protected a mux torque server cannot run in the agent's sandbox, and the session's loopback serves the task's Torque tools")
		if len(servers) == 0 {
			plan.Why = "every server the profile names was dropped while Torque's state is write-protected"
			plan.Warn = true
			return plan
		}
	}
	plan.Plant = true
	plan.Args = muxArgsFor(deps.MuxArgs, servers)
	return plan
}

// muxServersLogValue names the servers a planted mux argv runs, for the boot
// log: the value of its `--only` or `--servers`.
func muxServersLogValue(args []string) string {
	for i, a := range args {
		flag, _, inline := strings.Cut(a, "=")
		if flag != "--only" && flag != "--servers" {
			continue
		}
		if inline {
			return a
		}
		if i+1 < len(args) {
			return flag + " " + args[i+1]
		}
	}
	return "mux's own default servers (no --only or --servers in its argv)"
}

// muxNotPlantedReason says why plantsMux kept mux out of a session, for the
// boot log.
func muxNotPlantedReason(profile config.AgentProfile) string {
	if runtimeIDFor(profile.Provider) == string(runtimes.Claude) && len(profile.MuxServers) == 0 {
		return "a Claude session gets it only when its profile names mux_servers (CW-20261001-0226)"
	}
	return fmt.Sprintf("permission_mode %q; Codex and ACP sessions get it only under bypassPermissions (CW-20261001-0110, -0120)", profile.PermissionMode)
}

// logMuxPlan logs what planMux decided that the boot log does not already
// say: the warnings about a planted mux, and why none was planted (the
// routine reason only when the daemon has a mux to withhold).
func logMuxPlan(sessID string, deps *Dependencies, plan muxPlan) {
	for _, note := range plan.Notes {
		log.Printf("agent.Boot: session=%s: WARN %s", sessID, note)
	}
	if plan.Plant || plan.Why == "" {
		return
	}
	if plan.Warn {
		log.Printf("agent.Boot: session=%s: WARN mux MCP not planted: %s", sessID, plan.Why)
	} else if deps != nil && deps.MuxCommand != "" {
		log.Printf("agent.Boot: session=%s: mux MCP not planted: %s", sessID, plan.Why)
	}
}
