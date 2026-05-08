package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadProfilesCLI(t *testing.T) {
	yaml := `
agent_profiles:
  default:
    executor: cli
    provider: claude
    command: claude
    model: claude-sonnet-4-20250514
    args: ["--dangerously-skip-permissions"]
    output_format: stream-json
    timeout_seconds: 600
    max_agent_depth: 3
    pty: false

  codex:
    executor: cli
    provider: codex
    command: codex
    model: o4-mini
    args: ["--quiet"]
    output_format: print
    timeout_seconds: 300
    env_strip_prefixes: ["ANTHROPIC_"]

  copilot:
    executor: cli
    provider: copilot
    command: gh copilot
    model: ""
    args: []
    output_format: print

  gemini:
    executor: cli
    provider: gemini
    command: gemini
    model: gemini-2.5-pro
    args: ["--sandbox=permissive"]
    output_format: print
`
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0644))

	profiles, err := config.LoadProfiles(path)
	require.NoError(t, err)
	assert.Len(t, profiles, 4)

	// Default profile
	def := profiles["default"]
	assert.Equal(t, "cli", def.Executor)
	assert.Equal(t, "claude", def.Provider)
	assert.Equal(t, "claude", def.Command)
	assert.Equal(t, "claude-sonnet-4-20250514", def.Model)
	assert.Equal(t, []string{"--dangerously-skip-permissions"}, def.Args)
	assert.Equal(t, "stream-json", def.OutputFormat)
	assert.Equal(t, 600, def.TimeoutSeconds)
	assert.Equal(t, 3, def.MaxAgentDepth)
	require.NotNil(t, def.PTY, "fixture has pty: false explicit")
	assert.False(t, *def.PTY)

	// Codex profile
	codex := profiles["codex"]
	assert.Equal(t, "codex", codex.Provider)
	assert.Equal(t, []string{"ANTHROPIC_"}, codex.EnvStripPrefixes)
}

func TestLoadProfilesAPI(t *testing.T) {
	yaml := `
agent_profiles:
  api-default:
    executor: api
    provider: anthropic
    model: claude-sonnet-4-20250514
    temperature: 0.3
    max_tokens: 8192
    system_prompt: "You are a helpful coding assistant."
    tools: ["read_file", "write_file", "bash"]

  ollama-local:
    executor: api
    provider: ollama
    model: llama3.3
    temperature: 0.7
    base_url: "http://localhost:11434"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0644))

	profiles, err := config.LoadProfiles(path)
	require.NoError(t, err)

	apiDef := profiles["api-default"]
	assert.Equal(t, "api", apiDef.Executor)
	assert.Equal(t, "anthropic", apiDef.Provider)
	assert.Equal(t, "claude-sonnet-4-20250514", apiDef.Model)
	assert.InDelta(t, 0.3, apiDef.Temperature, 0.01)
	assert.Equal(t, 8192, apiDef.MaxTokens)
	assert.Equal(t, "You are a helpful coding assistant.", apiDef.SystemPrompt)
	assert.Equal(t, []string{"read_file", "write_file", "bash"}, apiDef.Tools)

	ollama := profiles["ollama-local"]
	assert.Equal(t, "http://localhost:11434", ollama.BaseURL)
}

func TestLoadProfilesMissing(t *testing.T) {
	_, err := config.LoadProfiles("/nonexistent/profiles.yaml")
	assert.Error(t, err)
}

func TestLoadProfilesEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.yaml")
	require.NoError(t, os.WriteFile(path, []byte(""), 0644))

	profiles, err := config.LoadProfiles(path)
	require.NoError(t, err)
	assert.Empty(t, profiles)
}

func TestGetProfileOrDefault(t *testing.T) {
	profiles := config.ProfileMap{
		"default": {Executor: "cli", Provider: "claude", Command: "claude", Model: "claude-sonnet-4-20250514"},
		"fast":    {Executor: "cli", Provider: "claude", Command: "claude", Model: "claude-haiku-4-20250514"},
	}

	// Named profile
	p := config.GetProfileOrDefault(profiles, "fast")
	assert.Equal(t, "claude-haiku-4-20250514", p.Model)

	// Default fallback
	p = config.GetProfileOrDefault(profiles, "")
	assert.Equal(t, "claude-sonnet-4-20250514", p.Model)

	// Unknown falls back to default
	p = config.GetProfileOrDefault(profiles, "nonexistent")
	assert.Equal(t, "claude-sonnet-4-20250514", p.Model)

	// No default returns zero profile
	empty := config.ProfileMap{}
	p = config.GetProfileOrDefault(empty, "anything")
	assert.Equal(t, "", p.Executor)
}

// CW-20260503-0019 (S2.3) — substrate builtins fill in for internal-
// task agent profiles when the user's profiles.yaml hasn't named them.
// User entries always win; the builtin fallback is the safety net for
// fresh installs.
func TestGetProfileOrDefault_BuiltinReviewer(t *testing.T) {
	// Empty user config — builtin reviewer-end-agent should resolve.
	p := config.GetProfileOrDefault(config.ProfileMap{}, "reviewer-end-agent")
	assert.Equal(t, "cli", p.Executor, "builtin reviewer profile must resolve")
	assert.NotEmpty(t, p.Provider)
	assert.Greater(t, p.TimeoutSeconds, 0)

	// User override beats the builtin.
	user := config.ProfileMap{
		"reviewer-end-agent": {Executor: "api", Provider: "anthropic", TimeoutSeconds: 1200},
	}
	p = config.GetProfileOrDefault(user, "reviewer-end-agent")
	assert.Equal(t, "api", p.Executor, "user profile must override builtin")
	assert.Equal(t, "anthropic", p.Provider)
	assert.Equal(t, 1200, p.TimeoutSeconds)
}
