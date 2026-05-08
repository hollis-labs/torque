package agent

import (
	"fmt"
	"strings"

	"github.com/hollis-labs/clockwork-manifold/internal/agentfile"
)

// composeSystemPrompt assembles the system prompt for the spawned agent.
// Stack order: agent-file persona first, options-supplied task framing
// second, inherited project context last (when present in opts.Metadata).
//
// Forked from internal/runtime/cliexec/prompt.go's composeSystemPrompt.
// Behavior change: source is now Options instead of executor.ExecutionJob,
// so ModeLongLived callers (orchestrator / planner / reviewer-end-agent)
// thread their template content through Options.SystemPrompt instead of
// LaunchRequest.SystemPrompt.
func composeSystemPrompt(opts Options, agent *agentfile.AgentFile) string {
	parts := make([]string, 0, 3)
	if agent != nil && strings.TrimSpace(agent.SystemPrompt) != "" {
		parts = append(parts, strings.TrimSpace(agent.SystemPrompt))
	}
	if strings.TrimSpace(opts.SystemPrompt) != "" {
		parts = append(parts, strings.TrimSpace(opts.SystemPrompt))
	}
	if inherited := inheritedProjectContextPrompt(opts.Metadata); inherited != "" {
		parts = append(parts, inherited)
	}
	return strings.Join(parts, "\n\n")
}

// composeUserPrompt returns the prompt body fed to the agent for ModeOneShot.
// Currently this is the OneShotPrompt (when set) or Description verbatim;
// system framing rides on the planted boot.md / system prompt instead of
// being prepended in-line.
func composeUserPrompt(opts Options) string {
	if opts.OneShotPrompt != "" {
		return opts.OneShotPrompt
	}
	return opts.Description
}

// inheritedProjectContextPrompt extracts project-context hints stamped by
// the orchestrator at executor enqueue time. Forked from cliexec/prompt.go;
// shape unchanged.
func inheritedProjectContextPrompt(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	raw, ok := metadata["project_context"]
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
