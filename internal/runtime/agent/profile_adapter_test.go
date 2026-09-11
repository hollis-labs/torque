package agent

import (
	"testing"

	"github.com/hollis-labs/go-agent-wrapper/adapters"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrapperAdapterHonorsClaudeProfile(t *testing.T) {
	for _, mode := range []string{"acceptEdits", "plan", "bypassPermissions"} {
		t.Run(mode, func(t *testing.T) {
			profile := config.AgentProfile{
				Provider: "claude-code", Model: "configured-model",
				PermissionMode: mode, Args: []string{"--effort", "high"},
			}
			cli, _, err := adapterFor(profile, "worker", RuntimeKindStreamingStdio)
			require.NoError(t, err)
			adapter, err := adapters.Select(adapters.Selection{
				Provider: adapters.ProviderClaude, LaunchMode: adapters.LaunchStreamingStdio,
				CLIAdapter: &profileCLIAdapter{CLIAdapter: cli, profile: profile},
			})
			require.NoError(t, err)
			args := adapter.CLIAdapter().BuildArgs("first turn", "", "resume-id")
			assert.Equal(t, []string{"--effort", "high", "--resume", "resume-id", "-p",
				"--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
				"--model", "configured-model"}, args)
			assert.NotContains(t, args, "--dangerously-skip-permissions")
		})
	}
}

func TestWrapperAdapterPreservesOpenCodeModelPosition(t *testing.T) {
	profile := config.AgentProfile{Provider: "opencode", Model: "provider/model"}
	cli, _, err := adapterFor(profile, "worker", RuntimeKindSubprocess)
	require.NoError(t, err)
	adapter := &profileCLIAdapter{CLIAdapter: cli, profile: profile}
	args := adapter.BuildArgs("prompt with spaces", "", "")
	assert.Equal(t, cli.BuildArgs("prompt with spaces", "", ""), args)
	assert.Equal(t, "prompt with spaces", args[len(args)-1])
}
