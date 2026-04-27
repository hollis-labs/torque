package bootstrap

import (
	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/cliexec"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	executorapi "github.com/hollis-labs/clockwork-manifold/plugins/executor-api"
	executoropencode "github.com/hollis-labs/clockwork-manifold/plugins/executor-opencode"
)

// Executors registers the built-in executor plugins with the registry.
// toolRouter is the core tool router interface (nil until Plan 4).
// Additional executors can be registered via the plugin host after bootstrap.
func Executors(reg *executor.Registry, profiles config.ProfileMap, toolRouter interface{}) error {
	// Register cliexec (CW-20260427-0040 Phase B): wrapper-driven CLI
	// executor composing go-providers + go-sandbox + go-runner +
	// go-agent-sessions. Replaces the legacy executor-cli plugin.
	cli := cliexec.New(profiles)
	reg.Register(cli)

	// Register executor-api
	api := executorapi.New(profiles, toolRouter)
	reg.Register(api)

	// Register executor-opencode: dispatches tasks via `opencode run`.
	oc := executoropencode.New()
	reg.Register(oc)

	return nil
}
