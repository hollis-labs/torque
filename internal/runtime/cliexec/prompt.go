package cliexec

import (
	"strings"

	"github.com/hollis-labs/clockwork-manifold/internal/agentfile"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

// composeSystemPrompt merges the agent_file's system_prompt with the task's
// system_prompt into a single string passed to the adapter's BuildArgs. Stack
// order matches the legacy executor-cli behavior: agent persona framing first,
// task-specific preamble second, separated by a blank line.
//
// go-providers' adapters take a single systemPrompt string; agents that
// previously consumed two separate --append-system-prompt flags now receive
// the concatenation. Adapters that don't accept a system-prompt flag silently
// ignore the argument (per their per-adapter contract).
func composeSystemPrompt(job *executor.ExecutionJob, agent *agentfile.AgentFile) string {
	parts := make([]string, 0, 2)
	if agent != nil && strings.TrimSpace(agent.SystemPrompt) != "" {
		parts = append(parts, strings.TrimSpace(agent.SystemPrompt))
	}
	if strings.TrimSpace(job.SystemPrompt) != "" {
		parts = append(parts, strings.TrimSpace(job.SystemPrompt))
	}
	return strings.Join(parts, "\n\n")
}

// composePrompt returns the prompt body fed to the agent. Currently this is
// the task description verbatim; system framing rides on systemPrompt instead
// of being prepended in-line. Adapters that lack a system-prompt flag (codex,
// copilot) accept this as-is — operators can encode persona framing in the
// agent_file and rely on adapter-level conventions (AGENTS.md for codex,
// GEMINI_SYSTEM_MD for gemini) where supported.
func composePrompt(job *executor.ExecutionJob) string {
	return job.Description
}
