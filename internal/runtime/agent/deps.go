package agent

import (
	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
	"github.com/hollis-labs/clockwork-manifold/internal/toolbroker"
)

// Dependencies bundles every collaborator agent.Boot and Manager need.
// Constructed once at composition root (cmd/clockwork/serve.go) and passed
// by pointer to every Boot caller. No global state.
//
// Construction order (NewManager populates deps.Sessions in place):
//
//	deps := &agent.Dependencies{Store: store, Profiles: profiles, ...}
//	deps.Sessions = agent.NewManager(deps)  // safe — captures *deps
//
// Nil-able fields:
//   - Service may be nil for unit tests that don't exercise the per-task MCP
//     loopback (matches the legacy cliexec.New(svc=nil) test path).
//   - Tools may be nil; Boot defaults to toolbroker.NewDefault() so the
//     OneShot scheduler path still has a non-nil router.
//   - Bus may be nil; events are silently dropped (nil-sink convention).
type Dependencies struct {
	// Store is the canonical session/checkpoint persistence layer.
	Store *sqlstore.Store

	// Profiles maps agent_profile names → AgentProfile shape (provider,
	// args, model, env policy, ...).
	Profiles config.ProfileMap

	// Loopback constructs the per-task MCP loopback handle (closure-bound
	// to the booted task — no task_id parameter required or accepted by
	// tools). nil disables the loopback (test path; mirrors the legacy
	// cliexec.New(svc=nil) shape). The concrete factory lives in
	// bootstrap/loopback.go to keep the agent package import-graph free
	// of mcpadapter (which imports planstart, which imports agent).
	Loopback LoopbackBuilder

	// Tools is the unified tool-broker (selection + permission engine +
	// audit log). nil → toolbroker.NewDefault() preserved for tests.
	Tools *toolbroker.ToolRouter

	// Bus carries session.state_changed lifecycle events through the SSE
	// bridge so dashboards see the same shape as run_events. nil = drop.
	Bus *scheduler.EventBus

	// WorkspacesRoot is the parent dir under which per-session workspace
	// dirs are materialized. Default $HOME/.clockwork/workspaces; tests
	// override to a tempdir.
	WorkspacesRoot string

	// Sessions is the long-lived agent Manager. Boot() drives Start through
	// it; lifecycle methods (SendInput / Stop / Wait / Attach / Checkpoint /
	// Resume) hang off it. Set by the composition root after Dependencies
	// is partially populated; NewManager(deps) returns a manager bound to
	// the partially-populated struct (it captures the pointer, not the
	// snapshot).
	Sessions *Manager
}
