package executorcli

import (
	"fmt"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

// CommandSpec holds the resolved command and arguments to execute.
type CommandSpec struct {
	Command string
	Args    []string
	// UseStreamJSON indicates stdout should be parsed as NDJSON stream.
	UseStreamJSON bool
}

// buildCommandSpec resolves a CommandSpec from the given profile and job.
// Provider-specific logic selects the binary and flags; the profile Command
// field overrides the binary if set.
func buildCommandSpec(profile config.AgentProfile, job *executor.ExecutionJob) (CommandSpec, error) {
	if profile.Command == "" && profile.Provider == "" {
		return CommandSpec{}, fmt.Errorf("profile has neither command nor provider set")
	}

	var spec CommandSpec

	switch profile.Provider {
	case "claude":
		spec = buildClaudeSpec(profile, job)
	case "codex":
		spec = buildCodexSpec(profile, job)
	case "copilot":
		spec = buildCopilotSpec(profile, job)
	case "gemini":
		spec = buildGeminiSpec(profile, job)
	default:
		spec = buildGenericSpec(profile, job)
	}

	// Profile-level command overrides the binary.
	if profile.Command != "" {
		spec.Command = profile.Command
	}

	// Profile args prepended before generated args.
	if len(profile.Args) > 0 {
		spec.Args = append(profile.Args, spec.Args...)
	}

	// Output format: "print" disables stream-json; anything else (default) uses it.
	if profile.OutputFormat == "print" {
		spec.UseStreamJSON = false
	}

	return spec, nil
}

// buildClaudeSpec builds a CommandSpec for the Anthropic Claude CLI.
func buildClaudeSpec(profile config.AgentProfile, job *executor.ExecutionJob) CommandSpec {
	cmd := "claude"
	args := []string{"--print"}

	if profile.Model != "" {
		args = append(args, "--model", profile.Model)
	}

	useStream := profile.OutputFormat != "print"
	if useStream {
		args = append(args, "--output-format", "stream-json")
	}

	args = append(args, job.Description)

	return CommandSpec{
		Command:       cmd,
		Args:          args,
		UseStreamJSON: useStream,
	}
}

// buildCodexSpec builds a CommandSpec for the OpenAI Codex CLI.
func buildCodexSpec(profile config.AgentProfile, job *executor.ExecutionJob) CommandSpec {
	cmd := "codex"
	args := []string{}

	if profile.Model != "" {
		args = append(args, "--model", profile.Model)
	}

	args = append(args, job.Description)

	return CommandSpec{
		Command:       cmd,
		Args:          args,
		UseStreamJSON: false,
	}
}

// buildCopilotSpec builds a CommandSpec for the GitHub Copilot CLI.
func buildCopilotSpec(profile config.AgentProfile, job *executor.ExecutionJob) CommandSpec {
	cmd := "gh"
	args := []string{"copilot", "suggest", "-t", "shell", job.Description}

	return CommandSpec{
		Command:       cmd,
		Args:          args,
		UseStreamJSON: false,
	}
}

// buildGeminiSpec builds a CommandSpec for the Google Gemini CLI.
func buildGeminiSpec(profile config.AgentProfile, job *executor.ExecutionJob) CommandSpec {
	cmd := "gemini"
	args := []string{}

	if profile.Model != "" {
		args = append(args, "--model", profile.Model)
	}

	args = append(args, job.Description)

	return CommandSpec{
		Command:       cmd,
		Args:          args,
		UseStreamJSON: false,
	}
}

// buildGenericSpec builds a CommandSpec for an unknown/generic provider.
// The profile must supply a Command override.
func buildGenericSpec(profile config.AgentProfile, job *executor.ExecutionJob) CommandSpec {
	cmd := profile.Command
	if cmd == "" {
		cmd = profile.Provider
	}
	args := []string{job.Description}

	return CommandSpec{
		Command:       cmd,
		Args:          args,
		UseStreamJSON: false,
	}
}
