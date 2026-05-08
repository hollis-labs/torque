// Package orchestrator implements the V0 sequential plan-execution
// agent (CW-20260503-0018, S2.2). The trigger (S2.1) reads this package's
// template + constants and hands the resolved values to agent.Manager.Boot —
// the LLM agent inside the spawned session reads its system_prompt (this
// package's template) and walks the plan via MCP loopback through
// clockwork_task_* / planner / reviewer surfaces.
//
// Package layering: orchestrator deliberately does NOT import the agent
// package — the dependency goes the other way (planstart imports both).
// That keeps the dependency graph DAG-shaped after CW-20260508-0001's
// agent.Boot() unification (which folds mcpadapter ← planstart ← agent into
// a chain that would otherwise cycle).
//
// V0 is sequential: one child task active at a time, no fan-out, no
// on-demand architect. Parallel/DAG/cost-budget are V2 of
// CW-20260421-0018.
package orchestrator

import (
	_ "embed"
	"os"
	"path/filepath"
)

const (
	// Profile is the agent_profile name the trigger stamps on the
	// orchestrator session's Boot Options. Resolves through
	// config.GetProfileOrDefault — operators override in profiles.yaml;
	// the substrate ships a builtin so fresh installs work.
	Profile = "orchestrator"

	// CommentAuthor is the comment-author prefix the orchestrator uses
	// on every comment it posts to the plan task. Operator audits can
	// grep all orchestrator activity by this token.
	CommentAuthor = "[system/orchestrator]"

	// SessionMetaPlanID is the SessionMeta key the trigger stamps so
	// the spawned agent can read its target plan via
	// clockwork_session_get. Mirrors the planner's
	// metadata.planner.target_plan_id pattern but keyed in the
	// session's metadata since the orchestrator runs as a session, not
	// a kind=internal task.
	SessionMetaPlanID = "plan_id"

	// SessionMetaRole disambiguates orchestrator sessions from other
	// long-lived sessions when an operator queries clockwork_session_list.
	SessionMetaRole      = "role"
	SessionMetaRoleValue = "orchestrator"

	// TemplateEnvVar is the user override for the agent template
	// directory shared with the planner (S2.4). When unset the
	// resolver looks in $HOME/.clockwork/agent-templates/.
	TemplateEnvVar = "CLOCKWORK_AGENT_TEMPLATE_DIR"

	// TemplateName is the file the resolver reads from the configured
	// dir. Operators replace its contents to customize the V0 prompt.
	TemplateName = "default-orchestrator.md"
)

//go:embed templates/default-orchestrator.md
var embeddedTemplate string

// SystemPromptForPlan composes the orchestrator's system prompt for the
// given plan. The body is the resolved template (LoadTemplate) prefixed
// with a one-line preamble that names the plan id — even if the agent
// forgets to call clockwork_session_get, it still has the id in immediate
// context.
//
// Caller (planstart.Start) wraps this into agent.Options for the Boot
// call. Keeping the agent.Options assembly in planstart preserves the
// orchestrator → agent dependency-free shape (orchestrator owns prompt
// content, planstart owns the lifecycle handoff).
func SystemPromptForPlan(planID, template string) string {
	if template == "" {
		template, _ = LoadTemplate()
	}
	return "Your target plan_id is `" + planID + "`. Read it via clockwork_session_get to confirm.\n\n" + template
}

// LoadTemplate returns the orchestrator template content + the resolved
// path. Lookup order: $CLOCKWORK_AGENT_TEMPLATE_DIR/default-orchestrator.md,
// then $HOME/.clockwork/agent-templates/default-orchestrator.md, then the
// embedded fallback ("<embedded>" path token). Shared dir convention with
// the planner template (S2.4).
func LoadTemplate() (string, string) {
	dir := os.Getenv(TemplateEnvVar)
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, ".clockwork", "agent-templates")
		}
	}
	if dir != "" {
		path := filepath.Join(dir, TemplateName)
		if data, err := os.ReadFile(path); err == nil {
			return string(data), path
		}
	}
	return embeddedTemplate, "<embedded>"
}
