package config_test

import (
	"strings"
	"testing"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLintProfilesYAML_FlagsDriftCases(t *testing.T) {
	problems, err := config.LintProfilesYAML([]byte(`
agent_profiles:
  nanite-backend:
    executor: cli
    provider: opencode

  api-missing-provider-openai:
    executor: api
    model: gpt-5.4

  old-copilot:
    executor: cli
    provider: copilot

  codex-runner:
    executor: cli
    provider: opencode
    mystery_flag: true

  ollama-local:
    executor: api
    provider: ollama
    model: llama3.3
`))
	require.NoError(t, err)

	got := make([]string, 0, len(problems))
	for _, p := range problems {
		got = append(got, p.String())
	}
	joined := strings.Join(got, "\n")

	assert.Contains(t, joined, "agent_profiles.nanite-backend: dishonest profile name: missing provider binding")
	assert.Contains(t, joined, "agent_profiles.api-missing-provider-openai.provider: missing provider")
	assert.Contains(t, joined, "agent_profiles.old-copilot.provider: provider \"copilot\" is not executable for executor \"cli\"")
	assert.Contains(t, joined, "agent_profiles.codex-runner.mystery_flag: unknown field")
	assert.Contains(t, joined, "agent_profiles.codex-runner: dishonest profile name: suggests provider \"codex\" but config provider is \"opencode\"")
	assert.Contains(t, joined, "agent_profiles.ollama-local.provider: unknown provider \"ollama\"")
}

func TestLintProfilesYAML_AllowsHonestProviderTaggedProfiles(t *testing.T) {
	problems, err := config.LintProfilesYAML([]byte(`
agent_profiles:
  torque-backend-codex:
    executor: cli
    provider: codex
    model: gpt-5.4

  stack-explorer-auditor-codex:
    executor: cli
    provider: codex
    model: gpt-5.4

  claude-code-smoke:
    executor: cli
    provider: claude-code
    model: claude-sonnet-4-5

  reviewer-end-agent:
    executor: cli
    provider: codex
    model: gpt-5.4
`))
	require.NoError(t, err)
	assert.Empty(t, problems)
}

func TestLintProfilesYAML_FlagsUnknownTopLevelField(t *testing.T) {
	problems, err := config.LintProfilesYAML([]byte(`
profiles_version: 1
agent_profiles: {}
`))
	require.NoError(t, err)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0].String(), "profiles_version: unknown top-level field")
}

func TestLintProfilesYAML_AllowsValidAgentProfileAliases(t *testing.T) {
	problems, err := config.LintProfilesYAML([]byte(`
agent_profiles:
  codex-long:
    executor: cli
    provider: codex
    model: gpt-5.4
agent_profile_aliases:
  torque-backend: codex-long
`))
	require.NoError(t, err)
	assert.Empty(t, problems)
}

func TestLintProfilesYAML_FlagsAliasDrift(t *testing.T) {
	problems, err := config.LintProfilesYAML([]byte(`
agent_profiles:
  codex-long:
    executor: cli
    provider: codex
    model: gpt-5.4
agent_profile_aliases:
  dangling: no-such-profile
  codex-long: codex-long
`))
	require.NoError(t, err)
	require.Len(t, problems, 2)
	joined := problems[0].String() + "\n" + problems[1].String()
	assert.Contains(t, joined, `alias target "no-such-profile" is not a defined agent_profiles entry`)
	assert.Contains(t, joined, "alias name collides with an agent_profiles entry")
}
