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
func AgentDeps(
	store *sqlstore.Store,
	profiles config.ProfileMap,
	svc *service.Service,
	tools *toolbroker.ToolRouter,
	bus *scheduler.EventBus,
) (*agent.Dependencies, error) {
	if store == nil {
		return nil, fmt.Errorf("agent deps bootstrap: store is nil")
	}
	if tools == nil {
		tools = toolbroker.NewDefault()
	}
	deps := &agent.Dependencies{
		Store:    store,
		Profiles: profiles,
		Loopback: loopbackBuilder(svc),
		Tools:    tools,
		Bus:      bus,
		// WorkspacesRoot defaults to $HOME/.clockwork/workspaces inside
		// agent.workspaceCreate when left empty.
	}
	deps.Sessions = agent.NewManager(deps)

	// Orphan sweep: mirror the prior sessionmgr.Sweep behavior. Any
	// `launching` / `running` rows whose process is gone after a daemon
	// restart get marked `crashed` so dashboards reflect reality.
	swept, err := deps.Sessions.Sweep()
	if err != nil {
		return nil, fmt.Errorf("agent orphan sweep: %w", err)
	}
	if swept > 0 {
		// Surface the sweep count via a top-level lifecycle event so
		// dashboards know a daemon restart marked sessions crashed.
		emitter := agent.NewSchedulerEmitter(bus)
		emitter.EmitSessionEvent("session.sweep", map[string]interface{}{
			"swept": swept,
		})
	}
	return deps, nil
}
