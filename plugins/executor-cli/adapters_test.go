package executorcli

import (
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCommandSpec_Claude(t *testing.T) {
	profile := config.AgentProfile{
		Provider: "claude",
		Model:    "claude-3-5-sonnet",
	}
	job := &executor.ExecutionJob{
		TaskID:      "task-1",
		Description: "do the thing",
	}

	spec, err := buildCommandSpec(profile, job)
	require.NoError(t, err)
	assert.Equal(t, "claude", spec.Command)
	assert.True(t, spec.UseStreamJSON, "claude defaults to stream-json")
	assert.Contains(t, spec.Args, "--model")
	assert.Contains(t, spec.Args, "claude-3-5-sonnet")
	assert.Contains(t, spec.Args, "do the thing")
}

// Bug 0015: claude requires --verbose when using --output-format stream-json.
func TestBuildCommandSpec_ClaudeStreamJSONHasVerbose(t *testing.T) {
	profile := config.AgentProfile{Provider: "claude"}
	job := &executor.ExecutionJob{TaskID: "t", Description: "x"}

	spec, err := buildCommandSpec(profile, job)
	require.NoError(t, err)
	assert.Contains(t, spec.Args, "--verbose", "stream-json mode must pass --verbose")
	assert.Contains(t, spec.Args, "--output-format")
	assert.Contains(t, spec.Args, "stream-json")
}

// Bug 0025: claude needs --json-schema to produce a structured result event.
func TestBuildCommandSpec_ClaudeStreamJSONHasJSONSchema(t *testing.T) {
	profile := config.AgentProfile{Provider: "claude"}
	job := &executor.ExecutionJob{TaskID: "t", Description: "x"}

	spec, err := buildCommandSpec(profile, job)
	require.NoError(t, err)
	assert.Contains(t, spec.Args, "--json-schema", "stream-json mode must pass --json-schema")
	// The schema constant follows --json-schema.
	for i, arg := range spec.Args {
		if arg == "--json-schema" {
			require.Less(t, i+1, len(spec.Args), "expected schema value after --json-schema")
			assert.Equal(t, executor.AgentOutputSchema, spec.Args[i+1])
			return
		}
	}
	t.Fatal("unreachable")
}

func TestBuildCommandSpec_ClaudeStreamJSONPromptIsLast(t *testing.T) {
	profile := config.AgentProfile{Provider: "claude"}
	job := &executor.ExecutionJob{TaskID: "t", Description: "the prompt"}

	spec, err := buildCommandSpec(profile, job)
	require.NoError(t, err)
	require.NotEmpty(t, spec.Args)
	assert.Equal(t, "the prompt", spec.Args[len(spec.Args)-1])
}

// Print-mode profile should NOT add --verbose / --json-schema / --output-format.
func TestBuildCommandSpec_ClaudePrintNoStreamFlags(t *testing.T) {
	profile := config.AgentProfile{Provider: "claude", OutputFormat: "print"}
	job := &executor.ExecutionJob{TaskID: "t", Description: "x"}

	spec, err := buildCommandSpec(profile, job)
	require.NoError(t, err)
	assert.NotContains(t, spec.Args, "--verbose")
	assert.NotContains(t, spec.Args, "--output-format")
	assert.NotContains(t, spec.Args, "--json-schema")
}

func TestBuildCommandSpec_ClaudePrint(t *testing.T) {
	profile := config.AgentProfile{
		Provider:     "claude",
		OutputFormat: "print",
	}
	job := &executor.ExecutionJob{
		TaskID:      "task-1",
		Description: "do the thing",
	}

	spec, err := buildCommandSpec(profile, job)
	require.NoError(t, err)
	assert.False(t, spec.UseStreamJSON, "print mode disables stream-json")
}

func TestBuildCommandSpec_ClaudeCommandOverride(t *testing.T) {
	profile := config.AgentProfile{
		Provider: "claude",
		Command:  "/usr/local/bin/claude",
	}
	job := &executor.ExecutionJob{
		TaskID:      "task-1",
		Description: "do the thing",
	}

	spec, err := buildCommandSpec(profile, job)
	require.NoError(t, err)
	assert.Equal(t, "/usr/local/bin/claude", spec.Command)
}

func TestBuildCommandSpec_Codex(t *testing.T) {
	profile := config.AgentProfile{
		Provider: "codex",
		Model:    "gpt-4o",
	}
	job := &executor.ExecutionJob{
		TaskID:      "task-2",
		Description: "build the feature",
	}

	spec, err := buildCommandSpec(profile, job)
	require.NoError(t, err)
	assert.Equal(t, "codex", spec.Command)
	assert.False(t, spec.UseStreamJSON)
	assert.Contains(t, spec.Args, "build the feature")
}

func TestBuildCommandSpec_Copilot(t *testing.T) {
	profile := config.AgentProfile{
		Provider: "copilot",
	}
	job := &executor.ExecutionJob{
		TaskID:      "task-3",
		Description: "fix the bug",
	}

	spec, err := buildCommandSpec(profile, job)
	require.NoError(t, err)
	assert.Equal(t, "gh", spec.Command)
	assert.False(t, spec.UseStreamJSON)
}

func TestBuildCommandSpec_Gemini(t *testing.T) {
	profile := config.AgentProfile{
		Provider: "gemini",
		Model:    "gemini-pro",
	}
	job := &executor.ExecutionJob{
		TaskID:      "task-4",
		Description: "analyze code",
	}

	spec, err := buildCommandSpec(profile, job)
	require.NoError(t, err)
	assert.Equal(t, "gemini", spec.Command)
	assert.Contains(t, spec.Args, "gemini-pro")
}

func TestBuildCommandSpec_Generic(t *testing.T) {
	profile := config.AgentProfile{
		Command: "my-agent",
	}
	job := &executor.ExecutionJob{
		TaskID:      "task-5",
		Description: "run task",
	}

	spec, err := buildCommandSpec(profile, job)
	require.NoError(t, err)
	assert.Equal(t, "my-agent", spec.Command)
}

func TestBuildCommandSpec_NeitherCommandNorProvider(t *testing.T) {
	profile := config.AgentProfile{}
	job := &executor.ExecutionJob{
		TaskID:      "task-6",
		Description: "run task",
	}

	_, err := buildCommandSpec(profile, job)
	assert.Error(t, err)
}

func TestBuildCommandSpec_ProfileArgsPreprended(t *testing.T) {
	profile := config.AgentProfile{
		Provider: "claude",
		Args:     []string{"--dangerously-skip-permissions"},
	}
	job := &executor.ExecutionJob{
		TaskID:      "task-7",
		Description: "do work",
	}

	spec, err := buildCommandSpec(profile, job)
	require.NoError(t, err)
	assert.Equal(t, "--dangerously-skip-permissions", spec.Args[0])
}

func TestResolveProfile(t *testing.T) {
	profiles := config.ProfileMap{
		"default": {Provider: "claude"},
		"fast":    {Provider: "codex", Model: "gpt-4o-mini"},
	}

	t.Run("named profile", func(t *testing.T) {
		job := &executor.ExecutionJob{AgentProfile: "fast"}
		p := resolveProfile(profiles, job)
		assert.Equal(t, "codex", p.Provider)
		assert.Equal(t, "gpt-4o-mini", p.Model)
	})

	t.Run("falls back to default", func(t *testing.T) {
		job := &executor.ExecutionJob{AgentProfile: "nonexistent"}
		p := resolveProfile(profiles, job)
		assert.Equal(t, "claude", p.Provider)
	})

	t.Run("empty name falls back to default", func(t *testing.T) {
		job := &executor.ExecutionJob{}
		p := resolveProfile(profiles, job)
		assert.Equal(t, "claude", p.Provider)
	})
}
