package orchestrator_test

import (
	"strings"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/orchestrator"
	"github.com/stretchr/testify/assert"
)

// SystemPromptForPlan composes the resolved orchestrator template with a
// plan_id preamble. Replaces the previous BuildLaunchRequest test surface
// (which returned a sessionmgr.LaunchRequest, since folded into agent.Boot
// at the call site in planstart per CW-20260508-0001).
func TestOrchestrator_SystemPromptForPlan(t *testing.T) {
	prompt := orchestrator.SystemPromptForPlan("CW-PLAN-001", "STUB TEMPLATE BODY")

	// Preamble surfaces plan_id even if the agent skips clockwork_session_get.
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

// Polling protocol must explicitly forbid bash/curl/raw-HTTP loops
// against the per-task MCP loopback (CW-20260509-0036). The orchestrator
// agent has been observed reaching for `while curl ...` loops with the
// loopback port from `.mcp.json`; that path returns `Invalid session ID`
// because the MCP endpoint requires session negotiation, hanging the
// orchestrator. The fix is loud, explicit anti-bash language in the
// template — guard it with a test so it doesn't silently regress.
func TestOrchestrator_TemplateForbidsBashPolling(t *testing.T) {
	t.Setenv(orchestrator.TemplateEnvVar, "/nonexistent/path")
	t.Setenv("HOME", "/nonexistent/home")

	content, _ := orchestrator.LoadTemplate()

	assert.Contains(t, content, "Polling protocol",
		"template must contain a Polling protocol section")
	assert.Contains(t, content, "clockwork_task_get",
		"template must name the MCP tool to use for polling")
	assert.Contains(t, content, "curl",
		"template must explicitly mention `curl` to forbid it")
	assert.Contains(t, content, "wget",
		"template must explicitly mention `wget` to forbid it")
	assert.Contains(t, content, "raw HTTP",
		"template must call out raw HTTP as a forbidden polling shape")
	assert.Contains(t, content, "bash",
		"template must explicitly forbid `bash` polling loops")
	assert.Contains(t, content, "while",
		"template must explicitly forbid bash `while` polling loops")
	assert.Contains(t, content, "until",
		"template must explicitly forbid bash `until` polling loops")
	assert.Contains(t, content, "Invalid session ID",
		"template must explain WHY raw HTTP fails against loopback")
	assert.Contains(t, content, "127.0.0.1",
		"template must call out the loopback address pattern as forbidden")
}

// TestOrchestrator_TemplateForbidsSessionListForChildMonitoring guards the
// CW-20260510-0064 fix: the orchestrator must NEVER poll child liveness via
// clockwork_session_list / clockwork_session_get — those tools surface
// substrate process state (PID=0, ExitCode=null for live adapter-mode
// sessions), and an LLM reading them as "crashed" produces false-negative
// child-crash diagnoses that abort the plan. Lock the deny-list in.
func TestOrchestrator_TemplateForbidsSessionListForChildMonitoring(t *testing.T) {
	t.Setenv(orchestrator.TemplateEnvVar, "/nonexistent/path")
	t.Setenv("HOME", "/nonexistent/home")

	content, _ := orchestrator.LoadTemplate()

	assert.Contains(t, content, "clockwork_session_list",
		"template must explicitly name clockwork_session_list in the deny-list")
	assert.Contains(t, content, "clockwork_session_get",
		"template must explicitly name clockwork_session_get in the deny-list")
	// Sanity: the deny-list rationale must teach the wire shape the daemon
	// produces post-PR-#41 — ExitCode/EndedAt are OMITTED (absent) from the
	// JSON for live adapter-mode sessions, not present-as-null. The agent
	// must resist hallucinating a crash from "missing" fields the way the
	// pre-fix template warned against null-as-crash.
	assert.Contains(t, content, "PID",
		"template must mention PID as a substrate-internal field (may be 0 between turns)")
	assert.Contains(t, content, "absent (omitted)",
		"template must teach that ExitCode/EndedAt are omitted (not null) on the wire for live sessions")
	assert.Contains(t, content, "Terminal=false",
		"template must point to Terminal=false as the authoritative still-alive signal")
}

// TestOrchestrator_TemplateEscalationPreconditionTaskStatusGate guards the
// CW-20260510-0064 fix to the Escalation section: before declaring a child
// crashed, the orchestrator MUST verify task.status ∈
// {failed, blocked, cancelled} via clockwork_task_get. A child with
// status=doing and a recent updated_at is NOT crashed regardless of session-
// shaped signals.
func TestOrchestrator_TemplateEscalationPreconditionTaskStatusGate(t *testing.T) {
	t.Setenv(orchestrator.TemplateEnvVar, "/nonexistent/path")
	t.Setenv("HOME", "/nonexistent/home")

	content, _ := orchestrator.LoadTemplate()

	// Anchor the precondition wording so it can't silently drift.
	assert.Contains(t, content, "Hard precondition",
		"escalation must announce the precondition as Hard")
	assert.Contains(t, content, "failed, blocked, cancelled",
		"escalation must enumerate the canonical failure-state set the task FSM must reach before escalating")
	assert.Contains(t, content, "is NOT crashed",
		"escalation must explicitly assert that doing+recent updated_at means NOT crashed")
	assert.Contains(t, content, "FORBIDDEN",
		"escalation must mark inferring crash from session_list/session_get as FORBIDDEN")
	assert.Contains(t, content, "[system/end-agent]",
		"escalation must reference the [system/end-agent] failed comment as the corroborating crash signal")
}

func TestOrchestrator_TemplateIncludesHITLCheckpointProtocol(t *testing.T) {
	t.Setenv(orchestrator.TemplateEnvVar, "/nonexistent/path")
	t.Setenv("HOME", "/nonexistent/home")

	content, _ := orchestrator.LoadTemplate()

	assert.Contains(t, content, "HITL checkpoint protocol",
		"template must teach the orchestrator typed HITL checkpoint usage")
	assert.Contains(t, content, "clockwork_task_checkpoint_emit",
		"template must name the checkpoint emit MCP tool")
	assert.Contains(t, content, "pr_review",
		"template must name the PR review workflow")
	assert.Contains(t, content, "approval",
		"template must name the approval workflow")
	assert.Contains(t, content, "message",
		"template must name the message workflow")
	assert.Contains(t, content, "task.metadata.checkpoint_responses",
		"template must include the redispatch response preflight")
	assert.NotContains(t, content, "Boot auto-inject",
		"template should not promise boot-time response injection")
}
