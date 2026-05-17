package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/torque/internal/config"
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
    runtime_kind: subprocess

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
	assert.Equal(t, "subprocess", def.RuntimeKind, "fixture has runtime_kind: subprocess explicit")

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

func TestProfileNames(t *testing.T) {
	profiles := config.ProfileMap{
		"zeta":    {},
		"alpha":   {},
		"default": {},
	}
	assert.Equal(t, []string{"alpha", "default", "zeta"}, config.ProfileNames(profiles))
	assert.Equal(t, []string{}, config.ProfileNames(nil))
}

func TestValidateProfileName(t *testing.T) {
	profiles := config.ProfileMap{
		"default": {},
		"fast":    {},
	}

	require.NoError(t, config.ValidateProfileName(profiles, "default"))

	err := config.ValidateProfileName(profiles, "typo")
	require.EqualError(t, err, "unknown agent_profile 'typo' in profiles.yaml agent_profiles registry — known: [default fast]")
}

func TestReloadableProfiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
agent_profiles:
  default:
    provider: claude
    model: claude-sonnet-4-20250514
`), 0644))

	reloaded := config.NewReloadableProfiles(path, nil)
	require.NoError(t, reloaded.Reload())

	first := reloaded.CurrentProfiles()
	assert.Equal(t, "claude-sonnet-4-20250514", first["default"].Model)

	// Returned snapshots are defensive copies.
	first["default"] = config.AgentProfile{Model: "mutated"}
	second := reloaded.CurrentProfiles()
	assert.Equal(t, "claude-sonnet-4-20250514", second["default"].Model)
}

// TestCatalogProviderID verifies the CLI-brand → catalog-provider alias
// map (CW-20260510-0100). profiles.yaml uses "claude" / "codex" because
// that's what the CLI invocation expects, but the models.dev catalog
// indexes by vendor namespace ("anthropic" / "openai"). Without this
// normalization the cost-backfill resolver silently misses every
// lookup. Verified against https://models.dev/api.json on 2026-05-10.
func TestCatalogProviderID(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"claude", "anthropic"},
		{"codex", "openai"},
		// Already-canonical provider ids pass through.
		{"anthropic", "anthropic"},
		{"openai", "openai"},
		{"google", "google"},
		// Unknown providers pass through (callers can detect aliasing
		// by checking input == output).
		{"opencode", "opencode"},
		{"copilot", "copilot"},
		{"", ""},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, config.CatalogProviderID(tc.in))
		})
	}
}

// TestLoadProfilesAliases verifies EDGE 3 of CW-20260517-0011: an
// agent_profile_aliases table lets a legacy name resolve to a renamed
// canonical profile. LoadProfiles folds aliases into the returned map so
// legacy ProfileMap-only callers keep working after a provider-honest
// rename.
func TestLoadProfilesAliases(t *testing.T) {
	yaml := `
agent_profiles:
  codex-long:
    executor: cli
    provider: codex
    command: codex
    model: gpt-5.4
    timeout_seconds: 10800

agent_profile_aliases:
  torque-backend: codex-long
  torque-frontend: codex-long
`
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0644))

	// LoadProfiles (legacy map-only): aliases fold in as extra keys.
	profiles, err := config.LoadProfiles(path)
	require.NoError(t, err)
	assert.Len(t, profiles, 3, "1 canonical + 2 aliases")
	assert.Equal(t, "gpt-5.4", profiles["codex-long"].Model)
	assert.Equal(t, "gpt-5.4", profiles["torque-backend"].Model, "legacy alias resolves")
	assert.Equal(t, "gpt-5.4", profiles["torque-frontend"].Model, "legacy alias resolves")

	// LoadProfilesFile keeps canonical and alias tables separate.
	pf, err := config.LoadProfilesFile(path)
	require.NoError(t, err)
	assert.Len(t, pf.Profiles, 1, "only canonical profiles")
	assert.Equal(t, "codex-long", pf.Aliases["torque-backend"])
}

// TestLoadProfilesAliasDangling rejects an alias that points at a
// missing canonical profile — an operator typo that would otherwise
// surface much later as a confusing empty-provider error.
func TestLoadProfilesAliasDangling(t *testing.T) {
	yaml := `
agent_profiles:
  codex-long:
    executor: cli
    provider: codex
agent_profile_aliases:
  torque-backend: codex-lonng
`
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0644))

	_, err := config.LoadProfiles(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "torque-backend")
	assert.Contains(t, err.Error(), "codex-lonng")
}

// TestResolveProfile covers EDGE 1 of CW-20260517-0011: ResolveProfile
// returns a registry-named, value-listing error on a miss instead of
// GetProfileOrDefault's silent zero value.
func TestResolveProfile(t *testing.T) {
	profiles := config.ProfileMap{
		"default":    {Executor: "cli", Provider: "claude-code", Model: "claude-sonnet-4-5"},
		"codex-long": {Executor: "cli", Provider: "codex", Model: "gpt-5.4"},
	}

	// Named profile resolves.
	p, err := config.ResolveProfile(profiles, "codex-long")
	require.NoError(t, err)
	assert.Equal(t, "gpt-5.4", p.Model)

	// Empty name resolves to default.
	p, err = config.ResolveProfile(profiles, "")
	require.NoError(t, err)
	assert.Equal(t, "claude-sonnet-4-5", p.Model)

	// Unknown name errors, naming the registry and listing valid values.
	_, err = config.ResolveProfile(profiles, "nanite.backend.main")
	require.Error(t, err)
	var upErr *config.UnknownProfileError
	require.ErrorAs(t, err, &upErr)
	assert.Equal(t, "nanite.backend.main", upErr.Name)
	assert.Equal(t, []string{"codex-long", "default"}, upErr.Known)
	assert.Contains(t, err.Error(), "agent_profiles registry")
	assert.Contains(t, err.Error(), "profiles.yaml")
	assert.Contains(t, err.Error(), "codex-long")
	assert.Contains(t, err.Error(), "Tether catalog", "error must disambiguate the three registries")

	// Builtin substrate profile resolves without a user config.
	p, err = config.ResolveProfile(config.ProfileMap{}, "reviewer-end-agent")
	require.NoError(t, err)
	assert.Equal(t, "cli", p.Executor)

	// Empty registry, no default → error names the empty registry.
	_, err = config.ResolveProfile(config.ProfileMap{}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
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
