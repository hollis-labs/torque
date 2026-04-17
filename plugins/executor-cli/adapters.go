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
//
// Stream-json mode (default): passes `--verbose --output-format stream-json
// --json-schema <AgentOutputSchema>` so claude emits an NDJSON event stream
// terminated by a structured `result` event. The schema forces the final
// reply into a strict JSON shape (see executor.AgentResult) so completion
// is unambiguous — no more parsing CLOCKWORK_DONE out of freeform markdown.
//
// `--verbose` is mandatory: without it, `--output-format stream-json` aborts
// with a usage error (Bug CW-20260417-0015). The FE executor ships this same
// flag combo.
//
// Print mode (profile.output_format = "print"): plain `--print`, no
// stream-json parsing. Used for debugging / legacy profiles.
//
// `--dangerously-skip-permissions` is expected to come from profile.Args
// (prepended in buildCommandSpec) so local runs can control it per profile.
func buildClaudeSpec(profile config.AgentProfile, job *executor.ExecutionJob) CommandSpec {
	cmd := "claude"
	useStream := profile.OutputFormat != "print"

	var args []string
	if useStream {
		// NOTE: order is --print, --verbose, --output-format stream-json,
		// --json-schema <schema>, [--model M], <prompt>. FE uses `-p` which
		// is the short form; we use `--print` for consistency with the print
		// branch below.
		args = append(args, "--print", "--verbose",
			"--output-format", "stream-json",
			"--json-schema", executor.AgentOutputSchema)
	} else {
		args = append(args, "--print")
	}

	if profile.Model != "" {
		args = append(args, "--model", profile.Model)
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
