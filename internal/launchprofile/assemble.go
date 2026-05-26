package launchprofile

import (
	"maps"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/torque/internal/config"
)

// TaskLaunchOverlay carries the per-task dynamic inputs that the stable
// LaunchProfile does not own. Applied on top of a CompiledLaunchProfile by
// BuildLaunchPlan to produce the final agentlaunch.LaunchPlan.
//
// Torque retains authoritative ownership of these values — task bundle
// planting, MCP loopback wiring, workspace/worktree resolution, and the
// composed boot prompt all live here rather than on LaunchProfile so the
// stable launch family stays operator-portable.
//
// The caller (typically agent.Boot) maps the Torque RuntimeKind onto the
// agentlaunch.RuntimeKind taxonomy and pre-composes the NativeFile surface
// before invoking BuildLaunchPlan; those concerns are agent-package
// internals (factory.go, task_context.go) and would otherwise close a
// cycle through this package.
type TaskLaunchOverlay struct {
	// SessionID is Torque's local agent session id for this boot. Stamped
	// onto the launch plan's Metadata.Annotations for forensic tooling.
	SessionID string

	// Role is the resolved human-readable role tag. Defaults to
	// CompiledLaunchProfile.Profile.Role at the call site when the caller
	// has no override.
	Role string

	// AgentFilePath is the verbatim Options.AgentFile path (empty when no
	// agent-file is set). Recorded as AgentSpec.RoleFile for provenance —
	// agentlaunch does not read it back, but it lands in the persisted
	// plan.
	AgentFilePath string

	// RuntimeKind is the already-mapped agentlaunch runtime kind. The
	// caller in the agent package owns the Torque→agentlaunch RuntimeKind
	// translation (mapRuntimeKind).
	RuntimeKind agentlaunch.RuntimeKind

	// ProviderID is the already-mapped agentlaunch provider id (e.g.
	// "claude" for Torque's "claude-code"). The caller owns the
	// torque-provider→agentlaunch-provider translation (mapProviderID).
	ProviderID string

	// ProjectID / Workdir / WorkspaceDir / BuildDirRoot come from
	// Options + the resolved WorkspaceLayout.
	ProjectID    string
	Workdir      string
	WorkspaceDir string
	BuildDirRoot string

	// SystemPrompt is the composed system prompt body (agent-file persona
	// + Options.SystemPrompt + inherited project context). Planted by the
	// shared launcher into CLAUDE.md / AGENTS.md.
	SystemPrompt string

	// KickoffMD is the kickoff markdown body the spawned agent receives
	// as its first turn payload.
	KickoffMD string

	// LoopbackURL is the task-scoped MCP loopback URL. Empty when the
	// loopback is disabled (the test path).
	LoopbackURL string

	// PermissionMode is the resolved permission posture for the spawn.
	// Empty for non-claude providers; the caller decides whether to set
	// it. agentlaunch's compile guard reads this on claude launches.
	PermissionMode string

	// Injection is the runtime-bootdir injection spec the caller resolved
	// from the agent runtime's bootdir builder. Carries the planted
	// task-bundle NativeFiles + per-runtime injection metadata. Required.
	// The caller (typically agent.Boot) composes the bundle and feeds it
	// through runtimebootdir.BuildInjection before assembly so this
	// package stays free of runtime-package dependencies.
	Injection agentlaunch.InjectionSpec
}

// BuildLaunchPlan is the single shared seam between Torque's launch-profile
// model and the agentlaunch.LaunchPlan contract. It takes a resolved stable
// launch family (CompiledLaunchProfile) plus the per-task dynamic overlay
// (TaskLaunchOverlay) and produces the LaunchPlan launcher.Compile expects.
//
// Both code paths that boot agents — scheduler-dispatched task runs and
// direct HTTP session launches — funnel through here, so the launch shape
// is defined exactly once.
//
// Design notes (mirror the pre-refactor buildLaunchPlan):
//
//   - Provider.Flags is left EMPTY. Torque keeps its own per-turn
//     BuildArgs closure (the adapter rebuilds argv every turn); the
//     shared launcher's composeArgv would otherwise bake profile.Args
//     into prepared.Argv and Torque would have to strip them back out.
//
//   - BootProfile is supplied inline (BootPrompt = composed system
//     prompt, BootContent = kickoff markdown).
//
//   - Workspace.Mode is WorkspacePersistent — Torque owns the workspace
//     dir tree (WorkspaceCreate); the launcher must not allocate a fresh
//     one. TempPrefix points the launcher's per-launch bootdir allocator
//     at Torque's $TMPDIR/torque-boot root so forensic tooling still
//     finds planted dirs.
//
//   - apiKeyHelper is NOT routed through InjectionSpec — it is a
//     provider-adapter-specific secret-bearing path. Torque keeps
//     threading it onto the ClaudeAdapter directly (boot.go).
//
//   - MuxCommand/MuxArgs/MuxEnv are NOT set on the plan — they are
//     filled onto PreparedLaunch.PlantContext after Prepare, keeping
//     daemon-scoped runtime values out of the persisted-at-rest plan.
func BuildLaunchPlan(compiled CompiledLaunchProfile, overlay TaskLaunchOverlay) agentlaunch.LaunchPlan {
	// Labels carry only the role tag — short, runtime-meaningful
	// identifiers the launcher / agentkit downstream may grep on.
	// LaunchProfile.Annotations are documented as plan-metadata
	// annotations (forensic / catalog material) and ride on
	// Metadata.Annotations below, not here, so a noisy annotation set
	// cannot inject label keys the launcher doesn't expect.
	var labels map[string]string
	if overlay.Role != "" {
		labels = map[string]string{"torque.role": overlay.Role}
	}

	// LaunchPlan.Validate rejects an empty Project.ID. Torque sessions
	// may be unscoped (opts.ProjectID == ""); mirror WorkspaceCreate's
	// "unscoped" projectKey fallback so the plan validates. The original
	// (possibly empty) ID still lands on the session row's soft-FK
	// elsewhere — this fallback is launch-plan-local.
	projectID := overlay.ProjectID
	if projectID == "" {
		projectID = "unscoped"
	}

	// Annotations — record both the legacy agent_profile (for back-
	// compat forensic queries) and the new launch_profile. Caller-
	// supplied LaunchProfile.Annotations are merged in last so an
	// operator-set annotation can override the substrate defaults if
	// they truly mean to.
	annotations := map[string]string{
		"torque.agent_profile":  compiled.AgentProfileName,
		"torque.launch_profile": compiled.Profile.ID,
		"torque.role":           overlay.Role,
	}
	maps.Copy(annotations, compiled.Profile.Annotations)

	return agentlaunch.LaunchPlan{
		Project: agentlaunch.ProjectSpec{
			ID:   projectID,
			Root: overlay.Workdir,
		},
		Agent: agentlaunch.AgentSpec{
			ID:       compiled.AgentProfileName,
			Name:     compiled.AgentProfileName,
			RoleFile: overlay.AgentFilePath,
			Labels:   labels,
		},
		Provider: agentlaunch.ProviderSpec{
			ID:            overlay.ProviderID,
			ModelOverride: compiled.AgentProfile.Model,
			Permission:    overlay.PermissionMode,
			// Flags intentionally empty — see the function godoc.
		},
		Runtime: overlay.RuntimeKind,
		Workspace: agentlaunch.WorkspaceSpec{
			Mode:         agentlaunch.WorkspacePersistent,
			Workdir:      overlay.Workdir,
			WorkspaceDir: overlay.WorkspaceDir,
			TempPrefix:   overlay.BuildDirRoot,
		},
		BootProfile: agentlaunch.BootProfileRef{
			Inline: &agentlaunch.BootProfileInline{
				BootPrompt:  overlay.SystemPrompt,
				BootContent: overlay.KickoffMD,
				BootMode:    agentlaunch.BootModePlanted,
			},
		},
		MCP: agentlaunch.MCPSpec{
			LoopbackURL: overlay.LoopbackURL,
		},
		Injection: overlay.Injection,
		// Torque autonomous dispatch is headless — no human at a TTY.
		// `background` (not `interactive`) is the honest lifecycle
		// stance and arms the agentlaunch compile guard for headless
		// claude permission posture.
		Mode: agentlaunch.LaunchBackground,
		Metadata: agentlaunch.Metadata{
			Annotations: annotations,
		},
	}
}

// AgentProfileForName looks up a single config.AgentProfile by registry
// name. Convenience wrapper around config.GetProfileOrDefault — re-exported
// here so call sites that already depend on this package do not need to
// import internal/config just for the helper.
func AgentProfileForName(source config.ProfileSource, name string) config.AgentProfile {
	return config.GetProfileOrDefault(source, name)
}
