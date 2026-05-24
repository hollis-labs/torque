package agent

import (
	"fmt"
	"strings"

	"github.com/hollis-labs/go-agent-launch/agentlaunch"
	runtimebootdir "github.com/hollis-labs/go-agent-runtime/bootdir"
	"github.com/hollis-labs/torque/internal/agentfile"
	"github.com/hollis-labs/torque/internal/config"
)

// buildLaunchPlanInput bundles every Torque-side value the LaunchPlan
// constructor needs, gathered into a struct rather than a long
// parameter list.
type buildLaunchPlanInput struct {
	// Profile is the resolved torque agent profile (provider, args,
	// model, env policy).
	Profile config.AgentProfile

	// AgentProfile is the torque profile lookup key — also the agent
	// display name the per-provider boot-file renderers reference.
	AgentProfile string

	// Role is the human-readable role tag.
	Role string

	// SessionID is Torque's local agent session id for this boot.
	SessionID string

	// AgentFile is the parsed agent-file (nil when none supplied).
	AgentFile *agentfile.AgentFile

	// AgentFilePath is the verbatim Options.AgentFile path (empty when
	// no agent-file is set). Recorded as AgentSpec.RoleFile for
	// provenance — go-agent-launch does not read it back, but it lands
	// in the persisted plan for forensics.
	AgentFilePath string

	// RuntimeKind is the already-resolved Torque runtime kind. Mapped
	// to the agentlaunch.RuntimeKind taxonomy by mapRuntimeKind.
	RuntimeKind RuntimeKind

	// ProjectID / Workdir / WorkspaceDir / BuildDirRoot come from
	// Options + the Stage 1 WorkspaceLayout.
	ProjectID    string
	Workdir      string
	WorkspaceDir string
	BuildDirRoot string

	// SystemPrompt / KickoffMD are the composed boot bodies.
	SystemPrompt string
	KickoffMD    string

	// LoopbackURL is the task-scoped MCP loopback URL (empty when the
	// loopback is disabled — the test path).
	LoopbackURL string

	// Options carries task identity and context fields used to plant
	// worker-readable boot context files.
	Options Options
}

// buildLaunchPlan assembles an agentlaunch.LaunchPlan from Torque
// inputs. It is the single translation point between Torque's Boot
// inputs and the shared go-agent-launch contract.
//
// Design notes:
//
//   - Provider.Flags is left EMPTY. Torque keeps its own per-turn
//     BuildArgs closure (the adapter rebuilds argv every turn); the
//     shared launcher's composeArgv would otherwise bake profile.Args
//     into prepared.Argv and Torque would have to strip them back out.
//     By passing no Flags, prepared.Argv carries ONLY the bootdir-
//     derived ProjectDirArg that providerplant appends — which is
//     exactly the slice Torque threads into StartOptions.ExtraArgs
//     for the non-bare adapters. profile.Args still flows into the
//     per-turn argv via composeBuildArgs, unchanged.
//
//   - BootProfile is supplied inline (BootPrompt = composed system
//     prompt, BootContent = kickoff markdown).
//
//   - Workspace.Mode is WorkspacePersistent — Torque owns the
//     workspace dir tree (Stage 1's WorkspaceCreate); the launcher
//     must not allocate a fresh one. TempPrefix points the launcher's
//     per-launch bootdir allocator at Torque's $TMPDIR/torque-boot
//     root so forensic tooling still finds planted dirs.
//
//   - apiKeyHelper is NOT routed through InjectionSpec — it is a
//     provider-adapter-specific secret-bearing path. Torque keeps
//     threading it onto the ClaudeAdapter directly (see boot.go); the
//     planted .claude/settings.json carries it because the adapter's
//     BootDirSpec render closure reads the adapter receiver.
//
//   - MuxCommand/MuxArgs/MuxEnv are NOT set on the plan — they are
//     filled onto PreparedLaunch.PlantContext after Prepare (mirrors
//     Tether's shared_launch.go), keeping daemon-scoped runtime values
//     out of the persisted-at-rest plan.
func buildLaunchPlan(in buildLaunchPlanInput) (agentlaunch.LaunchPlan, error) {
	rtKind, err := mapRuntimeKind(in.RuntimeKind)
	if err != nil {
		return agentlaunch.LaunchPlan{}, err
	}

	var labels map[string]string
	if in.Role != "" {
		labels = map[string]string{"torque.role": in.Role}
	}

	// LaunchPlan.Validate rejects an empty Project.ID. Torque sessions
	// may be unscoped (opts.ProjectID == ""); mirror WorkspaceCreate's
	// "unscoped" projectKey fallback so the plan validates. The original
	// (possibly empty) ID still lands on the session row's soft-FK
	// elsewhere — this fallback is launch-plan-local.
	projectID := in.ProjectID
	if projectID == "" {
		projectID = "unscoped"
	}

	injection, _, err := runtimebootdir.BuildInjection(runtimebootdir.Request{
		Provider:    mapProviderID(in.Profile.Provider),
		Runtime:     rtKind,
		NativeFiles: nativeFilesForLaunch(in),
	})
	if err != nil {
		return agentlaunch.LaunchPlan{}, err
	}

	// Permission posture — claude only. Threaded onto the plan so the
	// go-agent-launch v0.3.4 compile guard (ErrHeadlessClaudeNeedsPermission)
	// is a real backstop for the headless Mode below: a claude launch that
	// reaches Compile with no posture fails fast, instead of dispatching a
	// run structurally guaranteed to hang on the first approval prompt.
	// This is guard input only — the posture is actually delivered to the
	// spawned agent by the adapter (factory.go: ClaudeAdapter.PermissionMode),
	// since Torque plants via providerplant.WithAdapter, not DefaultResolver.
	// codex is left empty: the guard exempts it (go-providers defaults codex
	// approval_policy to the non-interactive "never").
	var permission string
	if in.Profile.Provider == "claude-code" {
		if profileIsDevMode(in.Profile) {
			permission = string(config.PermissionModeBypass)
		} else {
			permission = string(in.Profile.ResolvedPermissionMode())
		}
	}

	return agentlaunch.LaunchPlan{
		Project: agentlaunch.ProjectSpec{
			ID:   projectID,
			Root: in.Workdir,
		},
		Agent: agentlaunch.AgentSpec{
			ID:       in.AgentProfile,
			Name:     in.AgentProfile,
			RoleFile: in.AgentFilePath,
			Labels:   labels,
		},
		Provider: agentlaunch.ProviderSpec{
			// ID is the matrix-known provider id. Torque's "claude-code"
			// provider is the streaming-stdio shape of matrix provider
			// "claude" — normalize it here so matrix.Lookup resolves.
			ID:            mapProviderID(in.Profile.Provider),
			ModelOverride: in.Profile.Model,
			Permission:    permission,
			// Flags intentionally empty — see the function godoc.
		},
		Runtime: rtKind,
		Workspace: agentlaunch.WorkspaceSpec{
			Mode:         agentlaunch.WorkspacePersistent,
			Workdir:      in.Workdir,
			WorkspaceDir: in.WorkspaceDir,
			TempPrefix:   in.BuildDirRoot,
		},
		BootProfile: agentlaunch.BootProfileRef{
			Inline: &agentlaunch.BootProfileInline{
				BootPrompt:  in.SystemPrompt,
				BootContent: in.KickoffMD,
				BootMode:    agentlaunch.BootModePlanted,
			},
		},
		MCP: agentlaunch.MCPSpec{
			LoopbackURL: in.LoopbackURL,
		},
		Injection: injection,
		// Torque autonomous dispatch is headless — no human at a TTY.
		// `background` (not `interactive`) is the honest lifecycle stance
		// and arms the v0.3.4 ErrHeadlessClaudeNeedsPermission compile guard.
		Mode: agentlaunch.LaunchBackground,
		Metadata: agentlaunch.Metadata{
			Annotations: map[string]string{
				"torque.agent_profile": in.AgentProfile,
				"torque.role":          in.Role,
			},
		},
	}, nil
}

// mapProviderID normalizes a Torque provider name to the provider id
// the go-agent-launch matrix recognizes. Torque models the claude
// streaming-stdio runtime as a distinct provider name ("claude-code")
// for adapter-factory purposes; the matrix models it as the
// streaming-stdio runtime of provider "claude". Every other name
// passes through verbatim.
func mapProviderID(torqueProvider string) string {
	if torqueProvider == "claude-code" {
		return "claude"
	}
	return torqueProvider
}

// mapRuntimeKind maps Torque's RuntimeKind onto the agentlaunch
// RuntimeKind taxonomy. The two enums are intentionally distinct types
// (Torque's predates the shared one); this is the single conversion
// point. An unrecognized kind is a hard error — Boot must not silently
// downgrade to subprocess.
//
// agentlaunch.RuntimeServeHTTP added in v0.4.0 alongside go-agent-sessions
// v0.10.0 (serve_http_session.go) + go-providers v0.23.0
// (NewOpencodeAdapterServeHTTP). Without this case, profiles opting into
// serve-http would fail Boot here with "unmappable runtime kind" before
// the session is started.
func mapRuntimeKind(k RuntimeKind) (agentlaunch.RuntimeKind, error) {
	switch k {
	case RuntimeKindSubprocess, "":
		return agentlaunch.RuntimeSubprocess, nil
	case RuntimeKindPTY:
		return agentlaunch.RuntimePTY, nil
	case RuntimeKindStreamingStdio:
		return agentlaunch.RuntimeStreamingStdio, nil
	case RuntimeKindJsonRpcStdio:
		return agentlaunch.RuntimeJsonRpcStdio, nil
	case RuntimeKindServeHTTP:
		return agentlaunch.RuntimeServeHTTP, nil
	default:
		return "", fmt.Errorf("unmappable runtime kind %q", string(k))
	}
}

// muxEnvSliceToMap converts Torque's Dependencies.MuxEnv ([]string of
// "KEY=VALUE" entries) into the map[string]string form
// PreparedPlantContext.MuxEnv expects. Malformed entries (no "=") and
// empty keys are dropped — providerplant flattens the map back to the
// sorted "KEY=VALUE" slice provider.PlantContext.MuxEnv uses, so a
// malformed pass-through would just be silently re-emitted; dropping is
// the cleaner contract. Nil/empty in → nil out.
func muxEnvSliceToMap(in []string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for _, kv := range in {
		key, val, found := strings.Cut(kv, "=")
		if !found || key == "" {
			continue
		}
		out[key] = val
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// mergePreparedEnv appends the bootdir-derived env amendments
// sessionshim.ToSessionLaunch resolved from PreparedLaunch.Env (e.g.
// CODEX_HOME, OPENCODE_CONFIG_DIR) onto Torque's composed env slice. The
// amendments are appended last so they take precedence; os/exec resolves a
// duplicate KEY to the last occurrence, and go-agent-sessions forwards the
// slice verbatim.
func mergePreparedEnv(base []string, amendments []string) []string {
	if len(amendments) == 0 {
		return base
	}
	out := append([]string(nil), base...)
	for _, kv := range amendments {
		key, _, found := strings.Cut(kv, "=")
		if !found || key == "" {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// nativeFilesForLaunch projects Torque-owned boot context onto the
// shared InjectionSpec.NativeFiles surface. Agent-file persona still flows
// into CLAUDE.md/AGENTS.md via BootPrompt; the raw context files here are
// task-local facts the worker should not have to rediscover over MCP.
//
// Secrets must NOT appear in NativeFile.Content — InjectionSpec is persisted at
// rest as part of the launch plan.
func nativeFilesForLaunch(in buildLaunchPlanInput) []agentlaunch.NativeFile {
	files := taskContextNativeFiles(in)
	if len(files) == 0 {
		return nil
	}
	return files
}
