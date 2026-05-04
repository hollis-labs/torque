// Package orchestrator implements the V0 sequential plan-execution
// agent (CW-20260503-0018, S2.2). The trigger (S2.1) calls
// BuildLaunchRequest with a plan_id and hands the result to
// sessionmgr.Manager.Launch — the LLM agent inside the spawned
// session reads its system_prompt (this package's template) and walks
// the plan via MCP loopback through clockwork_task_* / planner /
// reviewer surfaces.
//
// V0 is sequential: one child task active at a time, no fan-out, no
// on-demand architect. Parallel/DAG/cost-budget are V2 of
// CW-20260421-0018.
package orchestrator

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/clockwork-manifold/internal/runtime/sessionmgr"
)

const (
	// Profile is the agent_profile name the trigger stamps on the
	// orchestrator session's LaunchRequest. Resolves through
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
	SessionMetaRole = "role"
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

// LaunchOptions parametrizes BuildLaunchRequest. Caller supplies the
// plan id; the rest defaults to V0 canonical values, overridable for
// future V1 callers (custom workdir, additional env, longer prompt).
type LaunchOptions struct {
	// PlanID is the kind=plan task the orchestrator walks. Required.
	PlanID string

	// Workdir is the spawned process's cwd (boot dir for claude /
	// project root for opencode). Caller picks. Required.
	Workdir string

	// ProjectID / TaskID propagate to the session row's soft-FKs so
	// `clockwork_session_list project_id=...` returns this session.
	// TaskID is typically the plan id; provided separately for
	// future V1 cases where the plan is not the session's task FK.
	ProjectID string
	TaskID    string

	// Env is the spawned process's environment additions. The trigger
	// passes through caller-provided env; tests pass nil.
	Env []string

	// Template lets the caller pass a pre-resolved template, short-
	// circuiting LoadTemplate. Tests use this to assert the launch's
	// system_prompt without touching the user's ~/.clockwork.
	Template string
}

// BuildLaunchRequest returns a sessionmgr.LaunchRequest ready for
// Manager.Launch. The trigger composes this with the daemon's
// sessionmgr instance.
//
// The system prompt is the resolved orchestrator template prefixed
// with a one-line preamble that names the plan id — even if the
// agent forgets to call clockwork_session_get, it still has the id
// in immediate context.
func BuildLaunchRequest(opts LaunchOptions) (sessionmgr.LaunchRequest, error) {
	if opts.PlanID == "" {
		return sessionmgr.LaunchRequest{}, fmt.Errorf("orchestrator: BuildLaunchRequest requires PlanID")
	}
	if opts.Workdir == "" {
		return sessionmgr.LaunchRequest{}, fmt.Errorf("orchestrator: BuildLaunchRequest requires Workdir")
	}
	template := opts.Template
	if template == "" {
		template, _ = LoadTemplate()
	}

	preamble := "Your target plan_id is `" + opts.PlanID + "`. Read it via clockwork_session_get to confirm.\n\n"
	systemPrompt := preamble + template

	return sessionmgr.LaunchRequest{
		AgentProfile: Profile,
		Workdir:      opts.Workdir,
		ProjectID:    opts.ProjectID,
		TaskID:       opts.TaskID,
		SystemPrompt: systemPrompt,
		Env:          opts.Env,
		SessionMeta: map[string]string{
			SessionMetaRole:   SessionMetaRoleValue,
			SessionMetaPlanID: opts.PlanID,
		},
	}, nil
}

// LoadTemplate returns the orchestrator template content + the
// resolved path. Lookup order: $CLOCKWORK_AGENT_TEMPLATE_DIR/
// default-orchestrator.md, then $HOME/.clockwork/agent-templates/
// default-orchestrator.md, then the embedded fallback ("<embedded>"
// path token). Shared dir convention with the planner template (S2.4).
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
