package launchprofile

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/torque/internal/config"
)

func TestBuildLaunchPlan_StampsAnnotationsAndIdentity(t *testing.T) {
	compiled := CompiledLaunchProfile{
		Profile: LaunchProfile{
			ID:           "orchestrator.default",
			Role:         "orchestrator",
			AgentProfile: "orchestrator",
			Annotations:  map[string]string{"torque.family": "orchestrator"},
		},
		AgentProfile: config.AgentProfile{
			Provider: "opencode",
			Model:    "opencode/big-pickle",
		},
		AgentProfileName: "orchestrator",
		Provenance:       ProvenanceExplicit,
	}
	overlay := TaskLaunchOverlay{
		SessionID:      "sess-1",
		Role:           "orchestrator",
		AgentFilePath:  "agents/orchestrator.md",
		RuntimeKind:    agentlaunch.RuntimeSubprocess,
		ProviderID:     "opencode",
		ProjectID:      "p-1",
		Workdir:        "/repo",
		WorkspaceDir:   "/ws/sess-1",
		BuildDirRoot:   "/tmp/torque-boot",
		SystemPrompt:   "system",
		KickoffMD:      "kickoff",
		LoopbackURL:    "http://127.0.0.1:1/mcp",
		PermissionMode: "",
	}

	plan := BuildLaunchPlan(compiled, overlay)

	assert.Equal(t, "p-1", plan.Project.ID)
	assert.Equal(t, "/repo", plan.Project.Root)
	assert.Equal(t, "orchestrator", plan.Agent.ID)
	assert.Equal(t, "orchestrator", plan.Agent.Name)
	assert.Equal(t, "agents/orchestrator.md", plan.Agent.RoleFile)
	// Labels carry ONLY the role tag — LaunchProfile.Annotations are
	// metadata annotations and must not bleed into AgentSpec.Labels.
	assert.Equal(t, "orchestrator", plan.Agent.Labels["torque.role"])
	_, family := plan.Agent.Labels["torque.family"]
	assert.False(t, family,
		"LaunchProfile.Annotations must not leak into AgentSpec.Labels")
	assert.Equal(t, "opencode", plan.Provider.ID)
	assert.Equal(t, "opencode/big-pickle", plan.Provider.ModelOverride)
	assert.Equal(t, agentlaunch.RuntimeSubprocess, plan.Runtime)
	assert.Equal(t, agentlaunch.WorkspacePersistent, plan.Workspace.Mode)
	assert.Equal(t, "/repo", plan.Workspace.Workdir)
	assert.Equal(t, "/ws/sess-1", plan.Workspace.WorkspaceDir)
	assert.Equal(t, "/tmp/torque-boot", plan.Workspace.TempPrefix)
	assert.NotNil(t, plan.BootProfile.Inline)
	assert.Equal(t, "system", plan.BootProfile.Inline.BootPrompt)
	assert.Equal(t, "kickoff", plan.BootProfile.Inline.BootContent)
	assert.Equal(t, "http://127.0.0.1:1/mcp", plan.MCP.LoopbackURL)
	assert.Equal(t, agentlaunch.LaunchBackground, plan.Mode)

	annotations := plan.Metadata.Annotations
	assert.Equal(t, "orchestrator", annotations["torque.agent_profile"])
	assert.Equal(t, "orchestrator.default", annotations["torque.launch_profile"])
	assert.Equal(t, "orchestrator", annotations["torque.role"])
	assert.Equal(t, "orchestrator", annotations["torque.family"])
}

func TestBuildLaunchPlan_UnscopedProjectFallback(t *testing.T) {
	compiled := CompiledLaunchProfile{
		Profile:          LaunchProfile{ID: BuiltinDefault, AgentProfile: "default"},
		AgentProfileName: "default",
	}
	overlay := TaskLaunchOverlay{
		Workdir: "/repo",
	}
	plan := BuildLaunchPlan(compiled, overlay)
	assert.Equal(t, "unscoped", plan.Project.ID,
		"empty ProjectID must materialize the 'unscoped' fallback "+
			"so agentlaunch.LaunchPlan.Validate accepts the plan")
}

func TestBuildLaunchPlan_PermissionMode_OnlyWhenSupplied(t *testing.T) {
	compiled := CompiledLaunchProfile{
		Profile:          LaunchProfile{ID: BuiltinDefault, AgentProfile: "default"},
		AgentProfileName: "default",
	}
	overlay := TaskLaunchOverlay{
		PermissionMode: "acceptEdits",
		Workdir:        "/repo",
		ProjectID:      "p",
	}
	plan := BuildLaunchPlan(compiled, overlay)
	assert.Equal(t, "acceptEdits", plan.Provider.Permission)
}
