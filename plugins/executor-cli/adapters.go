package executorcli

import (
	"fmt"

	"github.com/hollis-labs/clockwork-manifold/internal/agentfile"
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

// buildCommandSpec resolves a CommandSpec from the given profile, job, and
// optional agent file. Provider-specific logic selects the binary and flags;
// the profile Command field overrides the binary if set. A non-nil agent
// file can override profile.Model and contributes an extra system prompt
// that the claude provider stacks via --append-system-prompt.
func buildCommandSpec(profile config.AgentProfile, job *executor.ExecutionJob, agent *agentfile.AgentFile) (CommandSpec, error) {
	if profile.Command == "" && profile.Provider == "" {
		return CommandSpec{}, fmt.Errorf("profile has neither command nor provider set")
	}

	// Agent file v1: model override applies to every provider that honors
	// profile.Model. Tools are advisory only (logged at dispatch, not
	// enforced here — v2 will gate them via a wrapper process).
	if agent != nil && agent.Model != "" {
		profile.Model = agent.Model
	}

	var spec CommandSpec

	switch profile.Provider {
	case "claude":
		spec = buildClaudeSpec(profile, job, agent)
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

	// Output format override: "print" disables stream-json; "stream-json"
	// forces it on even for providers whose adapter defaults to print mode
	// (useful for generic profiles driving claude-compatible stream-json
	// producers like the test harness).
	switch profile.OutputFormat {
	case "print":
		spec.UseStreamJSON = false
	case "stream-json":
		spec.UseStreamJSON = true
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
//
// When a non-nil agent file is passed, its system_prompt is appended via its
// own --append-system-prompt pair immediately before the task-level prompt.
// Claude stacks every --append-system-prompt so both personas reach the
// agent (CW-20260417-0082). Order: [profile args] [agent sp] [task sp] [description].
func buildClaudeSpec(profile config.AgentProfile, job *executor.ExecutionJob, agent *agentfile.AgentFile) CommandSpec {
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

	// Agent file system prompt (CW-20260417-0082): stacked before the task
	// system prompt so the persona framing comes first and the task-specific
	// preamble refines it.
	if agent != nil && agent.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", agent.SystemPrompt)
	}

	// task.system_prompt (CW-20260417-0009): claude supports an explicit
	// --append-system-prompt flag for per-turn preamble without clobbering
	// profile-level Args. Placed after --model and before the positional
	// description so the final arg remains the prompt body.
	if job.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", job.SystemPrompt)
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

	args = append(args, prependSystemPrompt(job))

	return CommandSpec{
		Command:       cmd,
		Args:          args,
		UseStreamJSON: false,
	}
}

// buildCopilotSpec builds a CommandSpec for the GitHub Copilot CLI.
func buildCopilotSpec(profile config.AgentProfile, job *executor.ExecutionJob) CommandSpec {
	cmd := "gh"
	args := []string{"copilot", "suggest", "-t", "shell", prependSystemPrompt(job)}

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

	args = append(args, prependSystemPrompt(job))

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
	args := []string{prependSystemPrompt(job)}

	return CommandSpec{
		Command:       cmd,
		Args:          args,
		UseStreamJSON: false,
	}
}

// prependSystemPrompt returns the positional prompt body for providers that
// lack a stable per-turn system-prompt flag. When job.SystemPrompt is empty
// the description is returned verbatim; otherwise the system prompt is
// prepended as a preamble separated by a blank line so the agent sees it
// before the task body.
func prependSystemPrompt(job *executor.ExecutionJob) string {
	if job.SystemPrompt == "" {
		return job.Description
	}
	return "System: " + job.SystemPrompt + "\n\n" + job.Description
}
