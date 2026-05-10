package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// CatalogProviderID returns the models.dev catalog provider id for a
// profile-style provider name. CLI brands ("claude", "codex") don't match
// the upstream catalog's vendor namespacing ("anthropic", "openai") so
// callers that hand the profile's Provider straight to a catalog lookup
// silently miss every time. This helper centralizes the alias map so the
// cost resolver and the precheck capability gate normalize identically.
//
// Returns the input unchanged when no alias is known — callers that want
// strict matching can compare result == input to detect aliasing.
//
// Verified against https://models.dev/api.json on 2026-05-10. Extend
// this map when adding new CLI providers.
func CatalogProviderID(provider string) string {
	switch provider {
	case "claude":
		return "anthropic"
	case "codex":
		return "openai"
	default:
		return provider
	}
}

// AgentProfile defines the configuration for an executor agent.
// It supports both CLI and API executors with a unified structure.
type AgentProfile struct {
	// Common fields
	Executor string `yaml:"executor"` // "cli" or "api"
	Provider string `yaml:"provider"` // claude, codex, copilot, gemini, anthropic, openai, ollama, etc.
	Model    string `yaml:"model"`

	// CLI-specific fields
	Command          string   `yaml:"command,omitempty"`
	Args             []string `yaml:"args,omitempty"`
	OutputFormat     string   `yaml:"output_format,omitempty"` // "stream-json" or "print"
	TimeoutSeconds   int      `yaml:"timeout_seconds,omitempty"`
	MaxAgentDepth    int      `yaml:"max_agent_depth,omitempty"`
	// PTY is the operator-side PTY override. nil (yaml absent) → the agent
	// substrate's per-Mode + per-provider matrix decides. Explicit `true`
	// forces PTY (still subject to ModeOneShot's subprocess-only constraint).
	// Explicit `false` forces subprocess-per-turn even on providers/Modes
	// the matrix would PTY-enable. *bool gives the ternary semantics yaml
	// otherwise can't express against a `bool` field.
	PTY              *bool    `yaml:"pty,omitempty"`
	EnvStripPrefixes []string `yaml:"env_strip_prefixes,omitempty"`

	// API-specific fields
	Temperature  float64  `yaml:"temperature,omitempty"`
	MaxTokens    int      `yaml:"max_tokens,omitempty"`
	SystemPrompt string   `yaml:"system_prompt,omitempty"`
	Tools        []string `yaml:"tools,omitempty"`
	BaseURL      string   `yaml:"base_url,omitempty"`
	APIKey       string   `yaml:"api_key,omitempty"`
}

// ProfileMap is a named collection of agent profiles.
type ProfileMap map[string]AgentProfile

// profilesFile is the top-level YAML structure.
type profilesFile struct {
	AgentProfiles ProfileMap `yaml:"agent_profiles"`
}

// LoadProfiles reads agent profiles from a YAML file.
func LoadProfiles(path string) (ProfileMap, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load profiles %s: %w", path, err)
	}

	var f profilesFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse profiles %s: %w", path, err)
	}

	if f.AgentProfiles == nil {
		return ProfileMap{}, nil
	}

	return f.AgentProfiles, nil
}

// GetProfileOrDefault returns the named profile, falling back to "default",
// substrate-builtin profiles (CW-20260503-0019: reviewer-end-agent), and
// finally a zero-value profile if none of those resolve.
//
// User-supplied profiles.yaml (which the project loads from disk) ALWAYS
// wins over the builtin fallback — operators who want a different model
// or executor for a substrate-spawned internal task can simply add the
// matching name to their config. The fallback is the safety net so the
// scheduler's lifecycle hooks still dispatch on a fresh install where
// the user hasn't yet configured the substrate's automation roles.
func GetProfileOrDefault(profiles ProfileMap, name string) AgentProfile {
	if name != "" {
		if p, ok := profiles[name]; ok {
			return p
		}
		if p, ok := builtinProfiles[name]; ok {
			return p
		}
	}
	if p, ok := profiles["default"]; ok {
		return p
	}
	return AgentProfile{}
}

// builtinProfiles is the substrate's stock set of internal-task agent
// profiles. The lifecycle hooks stamp these names on tasks they create
// (kind=internal end-agents, future system / project-manager agents);
// without a fallback the picker's empty-profile guard would reject those
// tasks on every dispatch when a fresh install has no profiles.yaml.
//
// Add an entry here when shipping a new substrate-spawned internal-task
// role; users can still override by naming the same key in profiles.yaml.
// builtinProfiles defaults assume opencode provider — the open-question
// "what model id should the cost-backfill resolver see?" only matters when
// the provider is in the models.dev catalog (anthropic, openai, google,
// ...). opencode fronts multiple providers and the catalog has no
// `opencode` entry, so the backfill gate at cost.go:152 short-circuits on
// these regardless of whether Model is populated. Real user installs
// override these in profiles.yaml — those overrides set provider=claude
// AND model=claude-sonnet-4-5 (or similar), which IS in the catalog and
// hits the backfill. CW-20260510-0100 fixed the dogfood profiles.yaml;
// the builtin fallback stays opencode so a fresh-install user without a
// configured profiles.yaml still gets a working substrate-spawned agent
// (cost stays cost_source='unknown' for them, which is honest).
var builtinProfiles = ProfileMap{
	// Reviewer end-agent (CW-20260503-0019, S2.3, V1). Stamped on the
	// kind=internal task the lifecycle hook creates when a kind=agent
	// task transitions to `review`. cli + opencode mirrors the dogfood
	// default; users can override (different model, longer timeout) via
	// profiles.yaml.
	"reviewer-end-agent": {
		Executor:       "cli",
		Provider:       "opencode",
		TimeoutSeconds: 600,
	},

	// Planner (CW-20260503-0020, S2.4, V0). Spawned by the Orchestrator
	// at plan-execute boot to refine a kind=plan task before phase
	// walk. Short timeout — V0 is a single read-then-write pass over
	// the plan + child tasks, no iterative loops.
	"planner": {
		Executor:       "cli",
		Provider:       "opencode",
		TimeoutSeconds: 600,
	},

	// Orchestrator (CW-20260503-0018, S2.2, V0). Long-lived session
	// per plan — walks phases sequentially, dispatches children one at
	// a time, waits for the substrate-spawned reviewer to clear each.
	// Larger timeout because the session lifetime spans the entire
	// plan; cliexec / sessionmgr's own per-tick budget enforcement
	// applies inside.
	"orchestrator": {
		Executor:       "cli",
		Provider:       "opencode",
		TimeoutSeconds: 1800,
	},
}
