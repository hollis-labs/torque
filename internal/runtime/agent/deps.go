package agent

import (
	"github.com/hollis-labs/go-agent-sessions/agentsessions"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/hollis-labs/torque/internal/runtime/writeq"
	"github.com/hollis-labs/torque/internal/toolbroker"
)

// RuntimeFactory constructs the agentsessions.Runtime Boot uses to spawn the
// session. Production leaves Dependencies.RuntimeFactory nil — Boot falls
// back to agentsessions.NewFromAdapter against the resolved
// AdapterRuntimeConfig. Tests inject a closure that returns a fakeRuntime
// recording StartOptions without spawning a binary; this is the only
// injection seam between Boot and the lib's runtime constructor.
type RuntimeFactory func(cfg agentsessions.AdapterRuntimeConfig) (agentsessions.Runtime, error)

// Dependencies bundles every collaborator agent.Boot and Manager need.
// Constructed once at composition root (cmd/torque/serve.go) and passed
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

	// StateWriter serializes session-state mutations back into torque.db.
	// Nil preserves direct Store writes for tests and narrow CLI paths.
	StateWriter writeq.Writer

	// Profiles maps agent_profile names → AgentProfile shape (provider,
	// args, model, env policy, ...).
	Profiles config.ProfileSource

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
	// dirs are materialized. Default $HOME/.torque/workspaces; tests
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
	// path at startup (typically `<dir(os.Executable())>/torque-
	// apikey-helper`) and falls back to the empty string when the
	// helper is absent — bare mode then requires ANTHROPIC_API_KEY in
	// env (the existing CW-20260509-0011 contract). Empty here is
	// safe; non-empty paths are re-validated at Boot time (executable-
	// regular-file check). If the path was valid at startup but has
	// since been removed/replaced, Boot logs the misconfig and falls
	// back to the env-key path rather than failing the dispatch.
	ApiKeyHelperPath string

	// MuxCommand, when non-empty, is the absolute path to the Mux
	// binary the per-task bootdir plant should expose to spawned
	// agents as a second MCP server entry (alongside the per-task
	// torque loopback). The agent then has access to Vanta + the
	// portfolio-wide Mux-aggregated tool surface, not just the
	// task-restricted loopback. Empty disables the entry (back-compat
	// fence — pre-CW-20260510-0110 behavior preserved byte-for-byte).
	//
	// Companion fields MuxArgs / MuxEnv carry the stdio-child argv +
	// env. The composition root resolves all three at startup (env
	// var + sibling-binary + PATH probe; mirrors ApiKeyHelperPath's
	// resolveApiKeyHelperPath shape) and threads them onto Boot's
	// PlantContext via the lib's AutoPlantBootDir overlay (StartOptions
	// .PlantContext). Per-Boot overrides are filed as a follow-up;
	// today the configuration is daemon-scoped.
	//
	// CW-20260510-0110.
	MuxCommand string

	// MuxArgs is the argv passed to MuxCommand by the planted MCP
	// stdio entry. Default mirrors the user's interactive
	// ~/.claude.json `mcpServers.mux` shape:
	// `["mcp", "--proxy", "--servers", "vanta,torque,cerberus",
	//  "--token", "local-dev", "--scopes", "session.write,message.write"]`.
	//
	// Daemon-scoped today; TORQUE_MUX_ARGS env var override is
	// supported at startup. Per-Boot per-task scoping (e.g. read-only
	// token for some workers) is filed as a follow-up.
	MuxArgs []string

	// MuxEnv carries optional KEY=VALUE pairs the planted Mux entry
	// should set when claude/opencode/codex spawn the stdio child.
	// Today empty — spawned agents inherit the daemon's env, which
	// carries the auth tokens and PATH that Mux needs. Provided for
	// the rare future case where an isolated env (e.g. a per-Mux
	// scoped API key) is required.
	MuxEnv []string
}
