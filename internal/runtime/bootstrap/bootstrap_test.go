package bootstrap_test

import (
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/agent"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/bootstrap"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/hollis-labs/clockwork-manifold/internal/toolbroker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBootstrapExecutors verifies the post-CW-20260508-0001 surface:
// bootstrap.Executors registers the unified agent.Executor as the "cli" slot
// (replacing the legacy cliexec.CLIExecutor) and the executor-api plugin as
// "api". Both still report SupportsTools=true since the tool-broker is
// threaded via deps.Tools.
func TestBootstrapExecutors(t *testing.T) {
	profiles := config.ProfileMap{
		"default": {
			Executor: "cli",
			Provider: "claude",
			Command:  "claude",
			Model:    "claude-sonnet-4-20250514",
		},
		"api-default": {
			Executor: "api",
			Provider: "anthropic",
			Model:    "claude-sonnet-4-20250514",
		},
	}

	reg := executor.NewRegistry()
	deps := &agent.Dependencies{
		Profiles: profiles,
		Tools:    toolbroker.NewDefault(),
	}
	deps.Sessions = agent.NewManager(deps)

	err := bootstrap.Executors(reg, deps)
	require.NoError(t, err)

	cli, err := reg.Get("cli")
	require.NoError(t, err)
	assert.Equal(t, "cli", cli.Name())
	assert.True(t, cli.Capabilities().SupportsStreaming)
	assert.True(t, cli.Capabilities().SupportsTools, "Plan 4: tool-broker wired")
	assert.True(t, cli.Capabilities().SupportsResume, "CW-20260512-0059: cli executor supports resume for at least one provider (claude, codex); per-task answer via agent.ProviderCapabilities")

	api, err := reg.Get("api")
	require.NoError(t, err)
	assert.Equal(t, "api", api.Name())
	assert.True(t, api.Capabilities().SupportsStreaming)
	assert.True(t, api.Capabilities().SupportsTools, "Plan 4: tool-broker wired")
	assert.False(t, api.Capabilities().SupportsResume, "CW-20260512-0059: API executor has no native resume; reactor falls back to fresh-boot")
}

func TestBootstrapExecutorsListAll(t *testing.T) {
	profiles := config.ProfileMap{
		"default": {Executor: "cli", Provider: "claude"},
	}

	reg := executor.NewRegistry()
	deps := &agent.Dependencies{
		Profiles: profiles,
		Tools:    toolbroker.NewDefault(),
	}
	deps.Sessions = agent.NewManager(deps)

	err := bootstrap.Executors(reg, deps)
	require.NoError(t, err)

	list := reg.List()
	assert.GreaterOrEqual(t, len(list), 2, "should have at least cli and api executors")
}
