package agent

import (
	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
	"github.com/hollis-labs/clockwork-manifold/internal/toolbroker"
	"github.com/hollis-labs/go-agent-sessions/agentsessions"
)

// RuntimeFactory constructs the agentsessions.Runtime Boot uses to spawn the
// session. Production leaves Dependencies.RuntimeFactory nil — Boot falls
// back to agentsessions.NewFromAdapter against the resolved
// AdapterRuntimeConfig. Tests inject a closure that returns a fakeRuntime
// recording StartOptions without spawning a binary; this is the only
// injection seam between Boot and the lib's runtime constructor.
type RuntimeFactory func(cfg agentsessions.AdapterRuntimeConfig) (agentsessions.Runtime, error)

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

	// RuntimeFactory, when non-nil, overrides the default
	// agentsessions.NewFromAdapter call in Boot. See the RuntimeFactory
	// godoc above. nil = production default.
	RuntimeFactory RuntimeFactory

	// Sessions is the long-lived agent Manager. Boot() drives Start through
	// it; lifecycle methods (SendInput / Stop / Wait / Attach / Checkpoint /
	// Resume) hang off it. Set by the composition root after Dependencies
	// is partially populated; NewManager(deps) returns a manager bound to
	// the partially-populated struct (it captures the pointer, not the
	// snapshot).
	Sessions *Manager

	// ApiKeyHelperPath, when non-empty, is threaded into bare-mode
	// claude's planted .claude/settings.json as `apiKeyHelper: <path>`.
	// Bare-mode claude invokes the helper per request and consumes its
	// first line of stdout as the bearer token used for the API call.
	// Closes CW-20260509-0016: bare mode disables OAuth/keychain
	// auto-resolution, so subscription users (no ANTHROPIC_API_KEY in
	// env) need an explicit hook. The composition root resolves this
	// path at startup (typically `<dir(os.Executable())>/clockwork-
	// apikey-helper`) and falls back to the empty string when the
	// helper is absent — bare mode then requires ANTHROPIC_API_KEY in
	// env (the existing CW-20260509-0011 contract). Empty here is
	// safe; non-empty MUST point at an executable file (the path is
	// validated at Boot time, not on every dispatch).
	ApiKeyHelperPath string
}
