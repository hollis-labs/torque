package executorcli

import (
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/agentfile"
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

	spec, err := buildCommandSpec(profile, job, nil)
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

	spec, err := buildCommandSpec(profile, job, nil)
	require.NoError(t, err)
	assert.Contains(t, spec.Args, "--verbose", "stream-json mode must pass --verbose")
	assert.Contains(t, spec.Args, "--output-format")
	assert.Contains(t, spec.Args, "stream-json")
}

// Bug 0025: claude needs --json-schema to produce a structured result event.
func TestBuildCommandSpec_ClaudeStreamJSONHasJSONSchema(t *testing.T) {
	profile := config.AgentProfile{Provider: "claude"}
	job := &executor.ExecutionJob{TaskID: "t", Description: "x"}

	spec, err := buildCommandSpec(profile, job, nil)
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

	spec, err := buildCommandSpec(profile, job, nil)
	require.NoError(t, err)
	require.NotEmpty(t, spec.Args)
	assert.Equal(t, "the prompt", spec.Args[len(spec.Args)-1])
}

// Print-mode profile should NOT add --verbose / --json-schema / --output-format.
func TestBuildCommandSpec_ClaudePrintNoStreamFlags(t *testing.T) {
	profile := config.AgentProfile{Provider: "claude", OutputFormat: "print"}
	job := &executor.ExecutionJob{TaskID: "t", Description: "x"}

	spec, err := buildCommandSpec(profile, job, nil)
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

	spec, err := buildCommandSpec(profile, job, nil)
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

	spec, err := buildCommandSpec(profile, job, nil)
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

	spec, err := buildCommandSpec(profile, job, nil)
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

	spec, err := buildCommandSpec(profile, job, nil)
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

	spec, err := buildCommandSpec(profile, job, nil)
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

	spec, err := buildCommandSpec(profile, job, nil)
	require.NoError(t, err)
	assert.Equal(t, "my-agent", spec.Command)
}

func TestBuildCommandSpec_NeitherCommandNorProvider(t *testing.T) {
	profile := config.AgentProfile{}
	job := &executor.ExecutionJob{
		TaskID:      "task-6",
		Description: "run task",
	}

	_, err := buildCommandSpec(profile, job, nil)
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

	spec, err := buildCommandSpec(profile, job, nil)
	require.NoError(t, err)
	assert.Equal(t, "--dangerously-skip-permissions", spec.Args[0])
}

// Bug CW-20260417-0009: task.system_prompt must reach the CLI invocation.
// For claude, this maps to --append-system-prompt <value>, placed after the
// model flag and before the positional prompt (job.Description).
func TestBuildCommandSpec_ClaudeSystemPrompt_AppendFlag(t *testing.T) {
	profile := config.AgentProfile{Provider: "claude", Model: "claude-3-5-sonnet"}
	job := &executor.ExecutionJob{
		TaskID:       "t",
		Description:  "do the thing",
		SystemPrompt: "You are a careful, literal test agent.",
	}

	spec, err := buildCommandSpec(profile, job, nil)
	require.NoError(t, err)

	// Flag pair must appear and in that order.
	foundIdx := -1
	for i := 0; i+1 < len(spec.Args); i++ {
		if spec.Args[i] == "--append-system-prompt" {
			assert.Equal(t, "You are a careful, literal test agent.", spec.Args[i+1])
			foundIdx = i
			break
		}
	}
	require.GreaterOrEqual(t, foundIdx, 0, "expected --append-system-prompt pair in args: %v", spec.Args)

	// Positional prompt (description) must remain last.
	assert.Equal(t, "do the thing", spec.Args[len(spec.Args)-1])

	// Flag must be before the positional description.
	descIdx := len(spec.Args) - 1
	assert.Less(t, foundIdx+1, descIdx, "--append-system-prompt value should come before description")
}

// When SystemPrompt is empty, the CLI invocation must not gain the flag —
// profile.Args alone should drive behavior.
func TestBuildCommandSpec_ClaudeSystemPrompt_EmptyOmitsFlag(t *testing.T) {
	profile := config.AgentProfile{Provider: "claude"}
	job := &executor.ExecutionJob{TaskID: "t", Description: "do the thing"}

	spec, err := buildCommandSpec(profile, job, nil)
	require.NoError(t, err)
	assert.NotContains(t, spec.Args, "--append-system-prompt",
		"empty SystemPrompt must not introduce --append-system-prompt")
}

// Non-claude providers don't expose a stable system-prompt flag surface;
// for those we prepend the system prompt as a preamble so task.system_prompt
// still reaches the agent. Empty-SystemPrompt path is a no-op.
func TestBuildCommandSpec_NonClaudeSystemPromptPreamble(t *testing.T) {
	cases := []struct {
		name string
		prof config.AgentProfile
	}{
		{"codex", config.AgentProfile{Provider: "codex"}},
		{"gemini", config.AgentProfile{Provider: "gemini"}},
		{"generic", config.AgentProfile{Command: "my-agent"}},
	}
	for _, tc := range cases {
		t.Run(tc.name+"_with_prompt", func(t *testing.T) {
			job := &executor.ExecutionJob{
				TaskID:       "t",
				Description:  "body",
				SystemPrompt: "SP",
			}
			spec, err := buildCommandSpec(tc.prof, job, nil)
			require.NoError(t, err)
			last := spec.Args[len(spec.Args)-1]
			assert.Contains(t, last, "SP", "expected preamble to include system prompt: %q", last)
			assert.Contains(t, last, "body", "expected preamble to retain description: %q", last)
		})
		t.Run(tc.name+"_empty_prompt", func(t *testing.T) {
			job := &executor.ExecutionJob{TaskID: "t", Description: "body"}
			spec, err := buildCommandSpec(tc.prof, job, nil)
			require.NoError(t, err)
			assert.Equal(t, "body", spec.Args[len(spec.Args)-1], "empty SystemPrompt must leave description unchanged")
		})
	}
}

// CW-20260417-0082: agent file system_prompt must reach the claude CLI as
// its own --append-system-prompt pair, stacked BEFORE the task system prompt
// so persona framing lands first and task preamble refines it.
func TestBuildCommandSpec_Claude_AgentFileSystemPromptStacked(t *testing.T) {
	profile := config.AgentProfile{Provider: "claude"}
	job := &executor.ExecutionJob{
		TaskID:       "t",
		Description:  "do the thing",
		SystemPrompt: "task SP",
	}
	agent := &agentfile.AgentFile{SystemPrompt: "agent SP"}

	spec, err := buildCommandSpec(profile, job, agent)
	require.NoError(t, err)

	// Both --append-system-prompt values must be present, in order:
	// agent first, then task.
	var prompts []string
	for i := 0; i+1 < len(spec.Args); i++ {
		if spec.Args[i] == "--append-system-prompt" {
			prompts = append(prompts, spec.Args[i+1])
		}
	}
	require.Equal(t, []string{"agent SP", "task SP"}, prompts,
		"expected agent prompt before task prompt; args=%v", spec.Args)

	// Description must remain the last positional arg.
	assert.Equal(t, "do the thing", spec.Args[len(spec.Args)-1])
}

func TestBuildCommandSpec_Claude_AgentFileWithoutTaskPrompt(t *testing.T) {
	profile := config.AgentProfile{Provider: "claude"}
	job := &executor.ExecutionJob{TaskID: "t", Description: "body"}
	agent := &agentfile.AgentFile{SystemPrompt: "only agent SP"}

	spec, err := buildCommandSpec(profile, job, agent)
	require.NoError(t, err)

	var prompts []string
	for i := 0; i+1 < len(spec.Args); i++ {
		if spec.Args[i] == "--append-system-prompt" {
			prompts = append(prompts, spec.Args[i+1])
		}
	}
	assert.Equal(t, []string{"only agent SP"}, prompts)
}

// Agent file model overrides profile.Model (v1).
func TestBuildCommandSpec_Claude_AgentFileModelOverride(t *testing.T) {
	profile := config.AgentProfile{Provider: "claude", Model: "claude-profile-default"}
	job := &executor.ExecutionJob{TaskID: "t", Description: "x"}
	agent := &agentfile.AgentFile{SystemPrompt: "sp", Model: "claude-opus-4-7"}

	spec, err := buildCommandSpec(profile, job, agent)
	require.NoError(t, err)

	// Only the agent-file model should appear after --model.
	found := false
	for i := 0; i+1 < len(spec.Args); i++ {
		if spec.Args[i] == "--model" {
			assert.Equal(t, "claude-opus-4-7", spec.Args[i+1])
			found = true
		}
	}
	assert.True(t, found, "--model not present; args=%v", spec.Args)
	assert.NotContains(t, spec.Args, "claude-profile-default")
}

// Nil agent file must leave behavior identical to the pre-0082 contract.
func TestBuildCommandSpec_Claude_NilAgentFileBackwardCompat(t *testing.T) {
	profile := config.AgentProfile{Provider: "claude", Model: "m"}
	job := &executor.ExecutionJob{TaskID: "t", Description: "d", SystemPrompt: "sp"}

	spec, err := buildCommandSpec(profile, job, nil)
	require.NoError(t, err)

	// Exactly one --append-system-prompt pair (task level).
	count := 0
	for i := 0; i+1 < len(spec.Args); i++ {
		if spec.Args[i] == "--append-system-prompt" {
			count++
		}
	}
	assert.Equal(t, 1, count, "expected exactly one --append-system-prompt; args=%v", spec.Args)
}

// Agent file with empty system prompt must not emit a bare
// --append-system-prompt (defensive — Load enforces non-empty, but buildClaudeSpec
// should be resilient if a future call-site passes a zero-value struct).
func TestBuildCommandSpec_Claude_AgentFileEmptySystemPromptSkipped(t *testing.T) {
	profile := config.AgentProfile{Provider: "claude"}
	job := &executor.ExecutionJob{TaskID: "t", Description: "d"}
	agent := &agentfile.AgentFile{} // no system_prompt

	spec, err := buildCommandSpec(profile, job, agent)
	require.NoError(t, err)
	assert.NotContains(t, spec.Args, "--append-system-prompt")
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
