package bootstrap

import (
	"fmt"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/agent"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/hollis-labs/clockwork-manifold/internal/toolbroker"
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
// closer, when non-nil, must be invoked at daemon shutdown to drain the
// session lifecycle hook goroutine (CW-20260509-0028 layer 1). nil when
// any of (bus, store, sessions) is nil — i.e. test wirings that opt out
// of the hook.
func AgentDeps(
	store *sqlstore.Store,
	profiles config.ProfileMap,
	svc *service.Service,
	tools *toolbroker.ToolRouter,
	bus *scheduler.EventBus,
) (*agent.Dependencies, func(), error) {
	if store == nil {
		return nil, nil, fmt.Errorf("agent deps bootstrap: store is nil")
	}
	if tools == nil {
		tools = toolbroker.NewDefault()
	}
	deps := &agent.Dependencies{
		Store:    store,
		Profiles: profiles,
		Tools:    tools,
		Bus:      bus,
		// WorkspacesRoot defaults to $HOME/.clockwork/workspaces inside
		// agent.workspaceCreate when left empty.
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
