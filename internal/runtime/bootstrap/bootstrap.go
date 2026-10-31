package bootstrap

import (
	"os"
	"time"

	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/runtime/tetherexec"
	"github.com/hollis-labs/torque/internal/runtime/writeq"
	"github.com/hollis-labs/torque/internal/tetherclient"
	executorapi "github.com/hollis-labs/torque/plugins/executor-api"
)

// NewExecutorRegistry returns the executor registry a Torque process starts
// from: the mock executor, which torque serve has always registered, with
// Executors adding the built-ins once their dependencies exist. torque serve
// dispatches through it; torque mcp builds it only so task writes validate
// against the same names (CW-20260910-0087).
func NewExecutorRegistry() *executor.Registry {
	reg := executor.NewRegistry()
	reg.Register(executor.NewMockExecutor())
	return reg
}

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

	// tether: plain Tether sessions (CW-20261030-0210). Always registered so
	// task writes validate the name; it refuses dispatch (permanent error)
	// when TORQUE_TETHER_URL is unset.
	reg.Register(tetherExecutor(deps))

	return nil
}

// tetherExecutor builds the tether executor from the environment:
// TORQUE_TETHER_URL (http(s) or unix:/path), TORQUE_TETHER_TOKEN,
// TORQUE_TETHER_LAUNCH (default launch id), TORQUE_TETHER_WORKER,
// TORQUE_TETHER_STOP_AFTER_FINAL_TURN (opt-in reaper grace, e.g. 30s),
// TORQUE_TETHER_DEFER_MAX (default 30m), TORQUE_TETHER_RUN_TIMEOUT (default 2h),
// TORQUE_TETHER_POLL_INTERVAL (default 3s); durations like "90s".
// TORQUE_TETHER_ENVIRONMENT_ID (serve.go) is optional: when set, /capacity must report it.
func tetherExecutor(deps *agent.Dependencies) executor.Executor {
	cfg := tetherexec.Config{
		DefaultLaunch:      os.Getenv("TORQUE_TETHER_LAUNCH"),
		DefaultWorker:      os.Getenv("TORQUE_TETHER_WORKER"),
		StopAfterFinalTurn: os.Getenv("TORQUE_TETHER_STOP_AFTER_FINAL_TURN"),
		DeferMax:           envDuration("TORQUE_TETHER_DEFER_MAX"),
		DefaultTimeout:     envDuration("TORQUE_TETHER_RUN_TIMEOUT"),
		PollInterval:       envDuration("TORQUE_TETHER_POLL_INTERVAL"),
	}
	var binder tetherexec.RunBinder
	if deps.StateWriter != nil {
		binder = tetherexec.StoreBinder{Writer: deps.StateWriter}
	} else if deps.Store != nil {
		binder = tetherexec.StoreBinder{Writer: writeq.NewDirect(deps.Store)}
	}
	c := tetherclient.New(os.Getenv("TORQUE_TETHER_URL"), os.Getenv("TORQUE_TETHER_TOKEN"))
	if c == nil {
		return tetherexec.New(nil, binder, cfg)
	}
	return tetherexec.New(c, binder, cfg)
}

func envDuration(k string) time.Duration {
	d, err := time.ParseDuration(os.Getenv(k))
	if err != nil || d < 0 {
		return 0
	}
	return d
}
