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

	err := bootstrap.Executors(reg, profiles, nil, nil)
	require.NoError(t, err)

	// CLI executor should be registered. CW-20260503-0015 (Plan 4) flipped
	// SupportsTools=true on cliexec — a real ToolRouter is now threaded in
	// (NewDefault when bootstrap is called with nil), so calls flow through
	// the permission engine + audit log instead of escaping unfettered.
	cli, err := reg.Get("cli")
	require.NoError(t, err)
	assert.Equal(t, "cli", cli.Name())
	assert.True(t, cli.Capabilities().SupportsStreaming)
	assert.True(t, cli.Capabilities().SupportsTools, "Plan 4: tool-broker wired")

	// API executor should be registered. Phase E (CW-20260427-0043) wired the
	// vendor-SDK executor; CW-20260503-0015 flipped SupportsTools=true once
	// the tool-broker landed (the legacy stub claimed tools=true with no
	// plumbing; Phase E reverted it pending Plan 4; this is Plan 4).
	api, err := reg.Get("api")
	require.NoError(t, err)
	assert.Equal(t, "api", api.Name())
	assert.True(t, api.Capabilities().SupportsStreaming)
	assert.True(t, api.Capabilities().SupportsTools, "Plan 4: tool-broker wired")
}

func TestBootstrapExecutorsListAll(t *testing.T) {
	profiles := config.ProfileMap{
		"default": {Executor: "cli", Provider: "claude"},
	}

	reg := executor.NewRegistry()
	err := bootstrap.Executors(reg, profiles, nil, nil)
	require.NoError(t, err)

	list := reg.List()
	assert.GreaterOrEqual(t, len(list), 2, "should have at least cli and api executors")
}
