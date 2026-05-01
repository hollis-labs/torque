package cliexec

import (
	"fmt"
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
	if inherited := inheritedProjectContextPrompt(job); inherited != "" {
		parts = append(parts, inherited)
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

func inheritedProjectContextPrompt(job *executor.ExecutionJob) string {
	if job.Metadata == nil {
		return ""
	}
	raw, ok := job.Metadata["project_context"]
	if !ok {
		return ""
	}
	ctx, ok := raw.(map[string]any)
	if !ok {
		return ""
	}

	lines := []string{"Inherited project context:"}
	if v, ok := ctx["repo_path"].(string); ok && strings.TrimSpace(v) != "" {
		lines = append(lines, fmt.Sprintf("- Project path: %s", v))
	}
	if v, ok := ctx["agent_path"].(string); ok && strings.TrimSpace(v) != "" {
		lines = append(lines, fmt.Sprintf("- Agent path: %s", v))
	}
	appendStringList := func(label, key string) {
		items, ok := ctx[key].([]any)
		if !ok || len(items) == 0 {
			return
		}
		values := make([]string, 0, len(items))
		for _, item := range items {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				values = append(values, s)
			}
		}
		if len(values) > 0 {
			lines = append(lines, fmt.Sprintf("- %s: %s", label, strings.Join(values, ", ")))
		}
	}
	appendStringList("Read paths", "read_paths")
	appendStringList("Write paths", "write_paths")
	appendStringList("Additional context", "context_paths")
	appendStringList("Rules", "rules")
	if rawArtifacts, ok := ctx["artifacts"].([]any); ok && len(rawArtifacts) > 0 {
		summaries := make([]string, 0, len(rawArtifacts))
		for _, item := range rawArtifacts {
			artifact, ok := item.(map[string]any)
			if !ok {
				continue
			}
			path, _ := artifact["file_path"].(string)
			title, _ := artifact["title"].(string)
			switch {
			case title != "" && path != "":
				summaries = append(summaries, fmt.Sprintf("%s (%s)", title, path))
			case path != "":
				summaries = append(summaries, path)
			case title != "":
				summaries = append(summaries, title)
			}
		}
		if len(summaries) > 0 {
			lines = append(lines, fmt.Sprintf("- Project artifacts: %s", strings.Join(summaries, ", ")))
		}
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}
