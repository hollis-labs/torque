package bootstrap

import (
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/agent"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	executorapi "github.com/hollis-labs/clockwork-manifold/plugins/executor-api"
)

// Executors registers the built-in executor plugins with the registry.
// deps is the unified agent.Dependencies (constructed by AgentDeps); the
// agent.Executor satisfies executor.Executor and is registered as the "cli"
// slot the legacy cliexec.CLIExecutor occupied.
//
// The executor-api plugin shares the same tool-broker via deps.Tools so
// per-task tool calls flow through the permission engine + audit log
// regardless of which executor handles the dispatch.
func Executors(reg *executor.Registry, deps *agent.Dependencies) error {
	// Register the unified agent.Executor at "cli" — same slot cliexec
	// occupied. agent.Executor.Run wraps agent.Boot(Mode=ModeOneShot)
	// for scheduler-dispatched tasks.
	reg.Register(agent.NewExecutor(deps))

	// executor-api: HTTP/SDK executor with the same tool-broker pipeline.
	api := executorapi.New(deps.Profiles, deps.Tools)
	reg.Register(api)

	return nil
}
