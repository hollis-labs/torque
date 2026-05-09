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
