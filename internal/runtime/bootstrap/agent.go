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
func AgentDeps(
	store *sqlstore.Store,
	profiles config.ProfileSource,
	svc *service.Service,
	tools *toolbroker.ToolRouter,
	bus *scheduler.EventBus,
	stateWriter writeq.Writer,
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
		// WorkspacesRoot defaults to $HOME/.torque/workspaces inside
		// agent.WorkspaceCreate when left empty.
	}
	// loopbackBuilder needs a reference to deps.Sessions, but Sessions is
	// constructed by agent.NewManager(deps) BELOW (and NewManager itself
	// reads deps fields at construction time). The closure captures `deps`
	// by reference; deps.Sessions is read at handle-construction time
	// (per-Boot call), which always happens AFTER NewManager has set it.
	// This breaks the cycle without a two-phase construction.
	deps.Loopback = loopbackBuilder(svc, func() *agent.Manager { return deps.Sessions })
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
	return deps, closer, nil
}

// resolveApiKeyHelperPath returns the absolute path to the
// torque-apikey-helper binary that ships next to the daemon, or an
// empty string when the helper is absent.
//
// Resolution order:
//
//  1. TORQUE_APIKEY_HELPER env var (operator override; useful for
//     dev sessions where the helper was built into a separate dir).
//  2. <dir(os.Executable())>/torque-apikey-helper — the production
//     deployment shape: cerberus_resource_apply syncs both binaries
//     into the same artifact dir.
//  3. exec.LookPath equivalent against the daemon's PATH — fallback
//     for non-cerberus deployments where the helper is on PATH.
//
// Returns "" (empty path) when none of the above resolve. Boot then
// skips the apiKeyHelper field in .claude/settings.json and bare-mode
// claude falls back to ANTHROPIC_API_KEY in env (the existing
// CW-20260509-0011 contract). A clear `log.Printf` notes the
// resolution outcome at startup so operators can correlate auth
// failures with helper availability.
//
// CW-20260509-0016. Closes the auth gap for subscription users
// dispatching bare-mode claude without an API key in the daemon env.
func resolveApiKeyHelperPath() string {
	if override := os.Getenv("TORQUE_APIKEY_HELPER"); override != "" {
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
		log.Printf("[bootstrap] TORQUE_APIKEY_HELPER=%s (resolved=%s) set but path is not an executable file; ignoring", override, resolved)
	}

	exe, err := os.Executable()
	if err == nil {
		// Resolve symlinks so an artifact-dir helper is found even when the
		// daemon was launched via a symlink. EvalSymlinks errors fall back
		// to the unresolved path.
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = resolved
		}
		candidate := filepath.Join(filepath.Dir(exe), "torque-apikey-helper")
		if isExecutableFile(candidate) {
			log.Printf("[bootstrap] apiKeyHelper resolved next to daemon binary at %s", candidate)
			return candidate
		}
	}

	// PATH lookup as a last resort — homebrew installs, dev `go install`
	// targets, etc. Match the helper binary name.
	if path, lookErr := lookExecOnPath("torque-apikey-helper"); lookErr == nil {
		log.Printf("[bootstrap] apiKeyHelper resolved on PATH at %s", path)
		return path
	}

	log.Printf("[bootstrap] apiKeyHelper NOT resolved (no TORQUE_APIKEY_HELPER override, no sibling binary, no PATH match) — bare-mode claude will require ANTHROPIC_API_KEY in env")
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
