package bootstrap

import (
	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/cliexec"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/hollis-labs/clockwork-manifold/internal/toolbroker"
	executorapi "github.com/hollis-labs/clockwork-manifold/plugins/executor-api"
)

// Executors registers the built-in executor plugins with the registry.
// svc is the service-layer handle threaded into cliexec for per-task MCP
// loopback adapters (CW-20260427-0059). tools is the unified tool-broker
// facade (go-toolbroker selection + permission-gated dispatch) introduced by
// CW-20260503-0015 / Plan 4. When tools is nil, NewDefault is used so the
// executors still see a non-nil router (no-op-on-empty registries) — keeps
// `cliexec` and `executor-api` honest about reporting SupportsTools=true
// even when no MCP tools have been registered yet. Additional executors can
// be registered via the plugin host after bootstrap.
func Executors(reg *executor.Registry, profiles config.ProfileMap, svc *service.Service, tools *toolbroker.ToolRouter) error {
	if tools == nil {
		tools = toolbroker.NewDefault()
	}

	// Register cliexec (Phase B CW-20260427-0040): wrapper-driven CLI executor
	// composing go-providers + go-sandbox + go-runner + go-agent-sessions.
	// Phase D (CW-20260427-0042) folded the opencode adapter in; the `cli`
	// slot now serves claude/codex/gemini/copilot/opencode via go-providers.
	// CW-20260427-0059 threads svc for per-task MCP loopback. CW-20260503-0015
	// threads the tool-broker (Plan 4) so per-task tool calls flow through
	// the permission engine and audit log.
	cli := cliexec.New(profiles, svc, tools)
	reg.Register(cli)

	// Register executor-api. Same Plan-4 wiring: tool-broker is the canonical
	// permission/audit pipeline shared with cliexec.
	api := executorapi.New(profiles, tools)
	reg.Register(api)

	return nil
}
