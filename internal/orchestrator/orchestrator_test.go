package orchestrator_test

import (
	"strings"
	"testing"

	"github.com/hollis-labs/torque/internal/orchestrator"
	"github.com/hollis-labs/torque/internal/planner"
	"github.com/stretchr/testify/assert"
)

// SystemPromptForPlan composes the resolved orchestrator template with a
// plan_id preamble. Replaces the previous BuildLaunchRequest test surface
// (which returned a sessionmgr.LaunchRequest, since folded into agent.Boot
// at the call site in planstart per CW-20260508-0001).
func TestOrchestrator_SystemPromptForPlan(t *testing.T) {
	prompt := orchestrator.SystemPromptForPlan("CW-PLAN-001", "STUB TEMPLATE BODY")

	// Preamble surfaces plan_id even if the agent skips torque_session_get.
	assert.True(t, strings.HasPrefix(prompt, "Your target plan_id is `CW-PLAN-001`."),
		"preamble must lead with plan_id; got: %q", prompt[:64])
	assert.Contains(t, prompt, "STUB TEMPLATE BODY")
}

// SystemPromptForPlan with empty template falls back to LoadTemplate.
func TestOrchestrator_SystemPromptForPlan_LoadsTemplateWhenEmpty(t *testing.T) {
	t.Setenv(orchestrator.TemplateEnvVar, "/nonexistent/path")
	t.Setenv("HOME", "/nonexistent/home")

	prompt := orchestrator.SystemPromptForPlan("CW-PLAN-002", "")
	assert.True(t, strings.HasPrefix(prompt, "Your target plan_id is `CW-PLAN-002`."))
	assert.Contains(t, prompt, "Orchestrator")
}

// Embedded fallback resolves when the user's override dir is missing
// — fresh installs work without per-machine config.
func TestOrchestrator_LoadTemplateEmbeddedFallback(t *testing.T) {
	t.Setenv(orchestrator.TemplateEnvVar, "/nonexistent/path")
	t.Setenv("HOME", "/nonexistent/home")

	content, path := orchestrator.LoadTemplate()
	assert.NotEmpty(t, content)
	assert.Contains(t, content, "Orchestrator")
	assert.Equal(t, "<embedded>", path)
}

// The planner payload the orchestrator template hand-rolls must set
// on_done to the same policy internal/planner.BuildTask stamps. The
// planner is kind=internal, so no reviewer end-agent advances it — with
// the default on_done=review it stalls at `review` and the plan never
// proceeds past its first step (CW-20260518-0038).
//
// The expected string is derived from planner.OnDonePolicy, the single
// source of truth shared with BuildTask, so the two planner-creation
// paths cannot drift.
func TestOrchestrator_TemplatePlannerTaskTerminatesAtDone(t *testing.T) {
	t.Setenv(orchestrator.TemplateEnvVar, "/nonexistent/path")
	t.Setenv("HOME", "/nonexistent/home")

	content, _ := orchestrator.LoadTemplate()

	assert.Contains(t, content, `on_done="`+planner.OnDonePolicy+`"`,
		"planner payload must set on_done to the canonical policy")
}
