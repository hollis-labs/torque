package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

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
	PTY              bool     `yaml:"pty,omitempty"`
	EnvStripPrefixes []string `yaml:"env_strip_prefixes,omitempty"`

	// API-specific fields
	Temperature  float64  `yaml:"temperature,omitempty"`
	MaxTokens    int      `yaml:"max_tokens,omitempty"`
	SystemPrompt string   `yaml:"system_prompt,omitempty"`
	Tools        []string `yaml:"tools,omitempty"`
	BaseURL      string   `yaml:"base_url,omitempty"`
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
// and finally returning a zero-value profile if neither exists.
func GetProfileOrDefault(profiles ProfileMap, name string) AgentProfile {
	if name != "" {
		if p, ok := profiles[name]; ok {
			return p
		}
	}
	if p, ok := profiles["default"]; ok {
		return p
	}
	return AgentProfile{}
}
