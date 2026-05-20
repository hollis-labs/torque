package agent

import (
	"fmt"
	"strings"

	"github.com/hollis-labs/torque/internal/agentfile"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
)

// composeSystemPrompt assembles the system prompt for the spawned agent.
// Stack order:
//
//  1. agent-file persona (when supplied)
//  2. default-worker boot contract — ONLY for ModeLongLived worker-class
//     sessions (kind=agent dispatches; CW-20260519-0095 Phase 2). The
//     orchestrator / planner / reviewer-end-agent already have their own
//     baked-in templates and skip this prepend.
//  3. options-supplied task framing (per-task SystemPrompt)
//  4. checkpoint-redispatch protocol
//  5. inherited project context (when present in opts.Metadata)
//
// Forked from internal/runtime/cliexec/prompt.go's composeSystemPrompt.
// Behavior change vs. cliexec: source is now Options instead of
// executor.ExecutionJob, so ModeLongLived callers (orchestrator / planner
// / reviewer-end-agent) thread their template content through
// Options.SystemPrompt instead of LaunchRequest.SystemPrompt; AND
// ModeLongLived worker-class sessions get the substrate-side worker
// contract prepended automatically (the bit that historically was
// missing — the worker's boot context told it nothing about completion).
func composeSystemPrompt(opts Options, agent *agentfile.AgentFile) string {
	parts := make([]string, 0, 5)
	if agent != nil && strings.TrimSpace(agent.SystemPrompt) != "" {
		parts = append(parts, strings.TrimSpace(agent.SystemPrompt))
	}
	if shouldApplyWorkerTemplate(opts) {
		if tmpl := strings.TrimSpace(scheduler.DefaultWorkerTemplate()); tmpl != "" {
			parts = append(parts, tmpl)
		}
	}
	if strings.TrimSpace(opts.SystemPrompt) != "" {
		parts = append(parts, strings.TrimSpace(opts.SystemPrompt))
	}
	parts = append(parts, checkpointRedispatchPrompt)
	if inherited := inheritedProjectContextPrompt(opts.Metadata); inherited != "" {
		parts = append(parts, inherited)
	}
	return strings.Join(parts, "\n\n")
}

// shouldApplyWorkerTemplate reports whether opts describes a ModeLongLived
// worker-class session that should receive the default-worker contract.
// Two conditions, both must hold:
//
//   - opts.Mode is ModeLongLived (the contract talks about session
//     resident-until-self-signals — irrelevant for ModeOneShot dispatches).
//   - The effective role is NOT in the orchestrator-class set
//     (orchestrator / planner / reviewer-end-agent already carry their
//     own templates; double-stacking would be confusing and inflate the
//     context window for no benefit).
//
// Returns false for ModeSubagent and ModeBackground today — those are
// nested / fire-and-forget shapes whose contracts differ from the
// worker's. If a future caller wants the same treatment, lift the
// condition rather than overloading worker semantics here.
func shouldApplyWorkerTemplate(opts Options) bool {
	if opts.Mode != ModeLongLived {
		return false
	}
	role := opts.Role
	if role == "" {
		role = opts.AgentProfile
	}
	return !isOrchestratorClassRoleForPrompt(role)
}

// isOrchestratorClassRoleForPrompt mirrors bootstrap/loopback.go's
// isOrchestratorClassRole. Duplicated here on purpose: the agent package
// cannot import bootstrap (bootstrap imports agent). Keep the list in
// sync with the loopback's identical check — both functions answer the
// same question ("is this role one that gets the full cross-task
// surface + its own canned template?"). If you add a role to one, add
// it to the other.
func isOrchestratorClassRoleForPrompt(role string) bool {
	switch role {
	case "orchestrator", "planner", "reviewer-end-agent":
		return true
	}
	return false
}

const checkpointRedispatchPrompt = `Checkpoint redispatch protocol:
- At the start of each task dispatch, read your current task record through the torque loopback and inspect task.metadata.checkpoint_responses.
- Treat each entry as a typed HITL checkpoint response keyed by correlation_id. Handle any response you have not already incorporated before starting unrelated work.
- Boot does not inline checkpoint responses into your prompt. Read task metadata explicitly, then record what you handled in the task's normal audit trail so later redispatches do not repeat it.`

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
