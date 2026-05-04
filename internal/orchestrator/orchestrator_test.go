package orchestrator_test

import (
	"strings"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// AC1+AC2+AC3: BuildLaunchRequest produces the canonical sessionmgr
// LaunchRequest with profile=orchestrator, plan_id stamped in
// SessionMeta, and the boot template prefixed with a one-line plan_id
// preamble.
func TestOrchestrator_BuildLaunchRequest(t *testing.T) {
	req, err := orchestrator.BuildLaunchRequest(orchestrator.LaunchOptions{
		PlanID:    "CW-PLAN-001",
		Workdir:   "/tmp/orchestrator",
		ProjectID: "PRJ-1",
		TaskID:    "CW-PLAN-001",
		Template:  "STUB TEMPLATE BODY",
	})
	require.NoError(t, err)

	assert.Equal(t, orchestrator.Profile, req.AgentProfile)
	assert.Equal(t, "/tmp/orchestrator", req.Workdir)
	assert.Equal(t, "PRJ-1", req.ProjectID)
	assert.Equal(t, "CW-PLAN-001", req.TaskID)

	// Preamble surfaces plan_id even if the agent skips
	// clockwork_session_get.
	assert.True(t, strings.HasPrefix(req.SystemPrompt, "Your target plan_id is `CW-PLAN-001`."),
		"preamble must lead with plan_id; got: %q", req.SystemPrompt[:64])
	assert.Contains(t, req.SystemPrompt, "STUB TEMPLATE BODY")

	// SessionMeta carries the role tag (so List filters work) and
	// the plan_id (so the agent can recover it).
	assert.Equal(t, orchestrator.SessionMetaRoleValue, req.SessionMeta[orchestrator.SessionMetaRole])
	assert.Equal(t, "CW-PLAN-001", req.SessionMeta[orchestrator.SessionMetaPlanID])
}

func TestOrchestrator_BuildLaunchRequestRequiresPlanID(t *testing.T) {
	_, err := orchestrator.BuildLaunchRequest(orchestrator.LaunchOptions{
		Workdir: "/tmp",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PlanID")
}

func TestOrchestrator_BuildLaunchRequestRequiresWorkdir(t *testing.T) {
	_, err := orchestrator.BuildLaunchRequest(orchestrator.LaunchOptions{
		PlanID: "CW-PLAN-001",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Workdir")
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
