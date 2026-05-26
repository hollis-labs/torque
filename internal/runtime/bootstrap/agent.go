package bootstrap

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/hollis-labs/torque/internal/runtime/steering"
	"github.com/hollis-labs/torque/internal/runtime/writeq"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/toolbroker"
)

// AgentDeps constructs the unified agent.Dependencies + Manager and runs
// the startup orphan sweep. Replaces the prior bootstrap.SessionMgr +
// cliexec.New(profiles, svc, tools) split — every callsite that previously
// reached for sessionmgr.Manager or cliexec.CLIExecutor now gets the
// composed deps and routes through agent.Boot.
//
// nil-safe inputs:
//   - svc may be nil for unit tests that don't exercise per-task MCP loopback.
//   - tools may be nil; substituted with toolbroker.NewDefault() so registered
//     executors still see a non-nil router.
//   - bus may be nil; session.state_changed events are silently dropped.
//
// Returns deps with deps.Sessions populated and Sweep run; caller binds
// the returned manager into HTTP/MCP handlers (server.WithSessions,
// adapter.WithSessions).
//
// closer is always non-nil — invoke at daemon shutdown to drain the session
// lifecycle hook goroutine + any in-flight observer-spawned stop work
// (CW-20260509-0028 layers 1 + 2). When the hook is opted out (any of bus,
// store, sessions nil — e.g. test wirings), closer is a safe no-op so
// callers can call it unconditionally without nil-checking.
//
// pollReg is the opt-in inbox-poll registry (CW-20260518-0042) shared
// with the steering bridge. It is threaded into the orchestrator-class
// MCP loopback adapter so the torque_inbox_poll tool and the bridge
// agree on which recipients have opted into polling. nil is tolerated —
// the loopback adapter then reports polling as unavailable.
//
// reminderReg is the turn-boundary reminder registry (CW-20260519-0065)
// shared with the steering bridge. Threaded into every MCP loopback
// adapter (worker and orchestrator class) so the torque_steering_dismiss
// tool can mark envelopes ack'd, and onto agent.Dependencies so the
// long-lived executor can re-surface unaddressed envelopes at turn
// boundaries. nil disables the reminder pass entirely.
func AgentDeps(
	store *sqlstore.Store,
	profiles config.ProfileSource,
	svc *service.Service,
	tools *toolbroker.ToolRouter,
	bus *scheduler.EventBus,
	stateWriter writeq.Writer,
	pollReg *steering.PollRegistry,
	reminderReg *steering.ReminderRegistry,
) (*agent.Dependencies, func(), error) {
	if store == nil {
		return nil, nil, fmt.Errorf("agent deps bootstrap: store is nil")
	}
	if tools == nil {
		tools = toolbroker.NewDefault()
	}

	// CW-20260510-0110: resolve Mux binary + args at startup. Empty
	// Command → per-task bootdir plants carry only the torque
	// loopback (existing behavior). Non-empty Command → plants gain
	// a parallel `mux` MCP server entry that spawns Mux as an stdio
	// child, exposing Vanta + cross-task torque + cerberus to the
	// spawned agent.
	muxCfg := resolveMuxConfig()

	deps := &agent.Dependencies{
		Store:            store,
		StateWriter:      stateWriter,
		Profiles:         profiles,
		Tools:            tools,
		Bus:              bus,
		ApiKeyHelperPath: resolveApiKeyHelperPath(),
		MuxCommand:       muxCfg.Command,
		MuxArgs:          muxCfg.Args,
		MuxEnv:           muxCfg.Env,
		Reminder:         reminderReg,
		// WorkspacesRoot defaults to $HOME/.torque/workspaces inside
		// agent.WorkspaceCreate when left empty.
	}
	// loopbackBuilder needs a reference to deps.Sessions, but Sessions is
	// constructed by agent.NewManager(deps) BELOW (and NewManager itself
	// reads deps fields at construction time). The closure captures `deps`
	// by reference; deps.Sessions is read at handle-construction time
	// (per-Boot call), which always happens AFTER NewManager has set it.
	// This breaks the cycle without a two-phase construction.
	deps.Loopback = loopbackBuilder(svc, func() *agent.Manager { return deps.Sessions }, pollReg, reminderReg)
	deps.Sessions = agent.NewManager(deps)

	// Orphan sweep: mirror the prior sessionmgr.Sweep behavior. Any
	// `launching` / `running` rows whose process is gone after a daemon
	// restart get marked `crashed` so dashboards reflect reality.
	swept, err := deps.Sessions.Sweep()
	if err != nil {
		return nil, nil, fmt.Errorf("agent orphan sweep: %w", err)
	}
	if swept > 0 {
		// Surface the sweep count via a top-level lifecycle event so
		// dashboards know a daemon restart marked sessions crashed.
		emitter := agent.NewSchedulerEmitter(bus)
		emitter.EmitSessionEvent("session.sweep", map[string]interface{}{
			"swept": swept,
		})
	}

	// CW-20260509-0028 layer 1 + 2 — orchestrator self-stop.
	// Layer 1: plan-terminal transitions, observed via the service-layer
	// TaskTransitionObserver hook (primary; covers MCP/HTTP/scheduler
	// callsites uniformly) AND via the scheduler EventBus subscription
	// (defense-in-depth for any future scheduler path that publishes
	// task.transitioned for plan tasks).
	// Layer 2: session-complete marker comments via CommentObserver.
	// Returns nil if any required collaborator is nil; the closer is a
	// no-op in that case so callers can call it unconditionally.
	hook := agent.NewSessionLifecycleHook(bus, store, deps.Sessions)
	closer := func() {}
	if hook != nil {
		hook.Start()
		closer = hook.Close
		if svc != nil {
			if svc.Task != nil {
				svc.Task.SetTransitionObserver(hook)
			}
			if svc.Comment != nil {
				svc.Comment.SetObserver(hook)
			}
		}
	}

	// CW-20260519-0126 — plan structural-change emission. AddPhase /
	// RemovePhase fire service.PlanPhaseObserver, which the emitter
	// bridges onto the scheduler.EventBus as plan.phase_added /
	// plan.phase_removed. Closes the 2026-05-19 orchestrator stand-down
	// gap (no event told it ph-1 had been removed).
	if bus != nil && svc != nil && svc.Plan != nil {
		if emitter := agent.NewPlanPhaseEmitter(bus); emitter != nil {
			svc.Plan.SetPhaseObserver(emitter)
		}
	}
	return deps, closer, nil
}

// resolveApiKeyHelperPath returns the absolute path to a Torque
// apiKeyHelper binary when the operator has explicitly opted in via the
// TORQUE_APIKEY_HELPER env var, or an empty string otherwise.
//
// Opt-in only as of 2026-05-26. Previous behavior auto-resolved a sibling
// binary or PATH lookup so the helper threaded onto .claude/settings.json
// by default. That auto-wire was correct for ANTHROPIC_API_KEY-style keys
// (sk-ant-api03-…) but actively broke subscription OAuth users: claude
// CLI 2.1.x treats apiKeyHelper output as an API key, and the keychain
// payload for subscription users is an OAuth access token (sk-ant-oat01-…)
// that claude rejects as "Invalid API key" once the planted settings.json
// declares apiKeyHelper. With auto-resolve removed, the planted settings
// omits the field and claude falls through to its default discovery chain
// (env → keychain), matching the Nanite layout (which never threads
// apiKeyHelper) and the working behavior for OAuth subscription users.
//
// Operators dispatching against an ANTHROPIC_API_KEY-style helper opt in
// explicitly via TORQUE_APIKEY_HELPER=<absolute path>. Empty/unset → no
// helper threaded → claude reads keychain/env auth directly.
//
// CW-20260509-0016 closed the original auth gap; this 2026-05-26 change
// inverts the default (was: auto-on, opt-out via env unset) so the more
// common subscription path is the default. ANTHROPIC_API_KEY-only
// deployments are unchanged (claude reads the env directly, the helper
// is only one of several discovery paths).
func resolveApiKeyHelperPath() string {
	override := os.Getenv("TORQUE_APIKEY_HELPER")
	if override == "" {
		log.Printf("[bootstrap] apiKeyHelper disabled (TORQUE_APIKEY_HELPER not set) — planted .claude/settings.json omits apiKeyHelper; claude uses default keychain/env discovery. Set TORQUE_APIKEY_HELPER=<absolute path> to opt in for ANTHROPIC_API_KEY-style deployments.")
		return ""
	}
	// Normalize to absolute + symlink-resolved so the doc-promised
	// "absolute path" contract holds even when an operator sets a
	// relative path or routes through a symlink. Failures fall back to
	// the unresolved override; the executable-file check below catches
	// outright bogus paths regardless.
	resolved := override
	if abs, err := filepath.Abs(resolved); err == nil {
		resolved = abs
	}
	if eval, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = eval
	}
	if isExecutableFile(resolved) {
		log.Printf("[bootstrap] apiKeyHelper resolved via TORQUE_APIKEY_HELPER=%s", resolved)
		return resolved
	}
	log.Printf("[bootstrap] TORQUE_APIKEY_HELPER=%s (resolved=%s) set but path is not an executable file; ignoring (claude uses default keychain/env discovery)", override, resolved)
	return ""
}

// isExecutableFile reports whether path is a regular file with at
// least one execute bit set. Tests this via os.Stat — fast and
// avoids the false-positive of "directory we have +x on".
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !info.Mode().IsRegular() {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}

// lookExecOnPath is a thin wrapper around exec.LookPath to keep
// resolveApiKeyHelperPath compact. Splits responsibility for "the
// helper happened to be installed on PATH" from the daemon-adjacent
// resolution.
func lookExecOnPath(name string) (string, error) {
	return exec.LookPath(name)
}
