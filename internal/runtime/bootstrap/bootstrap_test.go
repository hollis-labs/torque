package bootstrap_test

import (
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/bootstrap"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	err := bootstrap.Executors(reg, profiles, nil)
	require.NoError(t, err)

	// CLI executor should be registered
	cli, err := reg.Get("cli")
	require.NoError(t, err)
	assert.Equal(t, "cli", cli.Name())
	assert.True(t, cli.Capabilities().SupportsStreaming)

	// API executor should be registered. Phase E (CW-20260427-0043) wired the
	// vendor-SDK executor and honestly reports tools=false until Plan 4 lands;
	// the legacy stub claimed SupportsTools=true with no actual plumbing.
	api, err := reg.Get("api")
	require.NoError(t, err)
	assert.Equal(t, "api", api.Name())
	assert.True(t, api.Capabilities().SupportsStreaming)
	assert.False(t, api.Capabilities().SupportsTools, "tools support deferred to Plan 4")
}

func TestBootstrapExecutorsListAll(t *testing.T) {
	profiles := config.ProfileMap{
		"default": {Executor: "cli", Provider: "claude"},
	}

	reg := executor.NewRegistry()
	err := bootstrap.Executors(reg, profiles, nil)
	require.NoError(t, err)

	list := reg.List()
	assert.GreaterOrEqual(t, len(list), 2, "should have at least cli and api executors")
}
