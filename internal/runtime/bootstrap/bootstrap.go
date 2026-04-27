package bootstrap

import (
	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/cliexec"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	executorapi "github.com/hollis-labs/clockwork-manifold/plugins/executor-api"
)

// Executors registers the built-in executor plugins with the registry.
// toolRouter is the core tool router interface (nil until Plan 4).
// Additional executors can be registered via the plugin host after bootstrap.
func Executors(reg *executor.Registry, profiles config.ProfileMap, toolRouter interface{}) error {
	// Register cliexec (Phase B CW-20260427-0040): wrapper-driven CLI executor
	// composing go-providers + go-sandbox + go-runner + go-agent-sessions.
	// Phase D (CW-20260427-0042) folded the opencode adapter in; the `cli`
	// slot now serves claude/codex/gemini/copilot/opencode via go-providers.
	cli := cliexec.New(profiles)
	reg.Register(cli)

	// Register executor-api
	api := executorapi.New(profiles, toolRouter)
	reg.Register(api)

	return nil
}
