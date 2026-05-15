package agent

import (
	"fmt"
	"strings"

	"github.com/hollis-labs/go-agent-launch/agentlaunch"
	"github.com/hollis-labs/torque/internal/agentfile"
	"github.com/hollis-labs/torque/internal/config"
)

// buildLaunchPlanInput bundles every Torque-side value the LaunchPlan
// constructor needs. Extracted into a struct (rather than a long
// parameter list) so Stage 3's optional launch-profile path — which
// will populate BootProfile.CatalogPath instead of the inline body —
// can reuse the same constructor with a different BootProfile source.
type buildLaunchPlanInput struct {
	// Profile is the resolved torque agent profile (provider, args,
	// model, env policy).
	Profile config.AgentProfile

	// AgentProfile is the torque profile lookup key — also the agent
	// display name the per-provider boot-file renderers reference.
	AgentProfile string

	// Role is the human-readable role tag.
	Role string

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

	// LaunchProfile, when non-nil, is the resolved optional launch
	// profile (CW-20260515-0021). It supplies the BASE LaunchPlan;
	// buildLaunchPlan overlays every Torque runtime-critical field on
	// top of it. Nil is the default — the pure-inline path below runs
	// and the output is byte-identical to the pre-Stage-3 behavior.
	LaunchProfile *launchProfileSource
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
//     prompt, BootContent = kickoff markdown). Stage 3 swaps this for
//     a CatalogPath-resolved profile; the rest of the plan is stable.
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
	// Launch-profile path (CW-20260515-0021): when an optional launch
	// profile was resolved, start from its BASE plan and overlay Torque's
	// runtime-critical fields. When no launch profile is referenced
	// (in.LaunchProfile == nil) the pure-inline path below runs unchanged
	// — the output is byte-identical to the pre-Stage-3 behavior.
	if in.LaunchProfile != nil {
		return buildLaunchPlanFromProfile(in)
	}
	return buildInlineLaunchPlan(in)
}

// buildInlineLaunchPlan is the default, pure-inline LaunchPlan
// constructor. It is exactly the Stage 2 buildLaunchPlan body, extracted
// verbatim so the no-launch-profile path stays byte-identical when
// Stage 3's launch-profile branch is skipped.
func buildInlineLaunchPlan(in buildLaunchPlanInput) (agentlaunch.LaunchPlan, error) {
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
		Injection: agentlaunch.InjectionSpec{
			NativeFiles: nativeFilesForLaunch(in.AgentFile),
		},
		Mode: agentlaunch.LaunchInteractive,
		Metadata: agentlaunch.Metadata{
			Annotations: map[string]string{
				"torque.agent_profile": in.AgentProfile,
				"torque.role":          in.Role,
			},
		},
	}, nil
}

// buildLaunchPlanFromProfile builds the LaunchPlan for the optional
// launch-profile path (CW-20260515-0021). It starts from the launch
// profile's BASE plan and overlays every Torque runtime-critical field.
//
// # Precedence
//
// The launch profile contributes BASE values only; Torque always wins on
// the fields it owns. Concretely:
//
//   - From the launch profile (base): Provider.ID (when Torque's profile
//     does not name one), Runtime (when Torque's resolved kind is empty),
//     Workspace.Mode raw token, Mode, MCP.Allowlist, Metadata labels +
//     annotations.
//
//   - ALWAYS overlaid by Torque (a launch profile can NOT override
//     these — doing so would break loopback auth, workspace ownership,
//     or boot-prompt planting):
//
//     * BootProfile — Torque's composed system prompt + kickoff markdown
//       (inline, BootModePlanted). The launch profile's boot_profile
//       reference is intentionally dropped: Torque owns boot-prompt
//       composition (role + agent-file + project context).
//     * Workspace.Workdir / WorkspaceDir / TempPrefix — Stage 1's
//       WorkspaceLayout dirs. Workspace ownership stays with Torque.
//     * MCP.LoopbackURL — the task-scoped MCP loopback URL Torque
//       constructed + authorized.
//     * Project.ID / Project.Root — Torque's project + workdir.
//     * Agent.ID / Agent.Name / Agent.RoleFile — Torque's agent profile
//       identity + agent-file provenance.
//     * Provider.ModelOverride — Torque's profile model, when set.
//     * Provider.Flags — forced empty (see buildInlineLaunchPlan godoc;
//       Torque rebuilds argv per turn).
//     * Injection.NativeFiles — Torque's agent-file projection.
//
//   - The resolved runtime kind: Torque's resolveRuntimeKind chain
//     (Options override → profile.RuntimeKind → per-provider matrix)
//     wins. The launch profile's runtime only shows through when Torque
//     produced no explicit kind AND the per-provider default would also
//     be empty — which never happens today (the matrix always returns a
//     concrete kind). In practice Torque's runtime kind is authoritative.
//
// Secrets are never read from the launch profile: Injection.BootDirOverlay
// and Provider.Env from the base plan are deliberately discarded here, so
// a persisted-at-rest launch profile cannot smuggle secret-bearing env or
// file content into the boot dir.
func buildLaunchPlanFromProfile(in buildLaunchPlanInput) (agentlaunch.LaunchPlan, error) {
	rtKind, err := mapRuntimeKind(in.RuntimeKind)
	if err != nil {
		return agentlaunch.LaunchPlan{}, err
	}

	// Start from the launch profile's base plan (value copy — the source
	// plan is not mutated).
	plan := in.LaunchProfile.BasePlan

	// --- Provider ----------------------------------------------------
	// Torque's profile provider wins when it names one; otherwise the
	// launch profile's provider id stands. ModelOverride + Flags are
	// always Torque-owned.
	if mapped := mapProviderID(in.Profile.Provider); mapped != "" {
		plan.Provider.ID = mapped
	}
	plan.Provider.ModelOverride = in.Profile.Model
	plan.Provider.Flags = nil // see buildInlineLaunchPlan godoc.

	// --- Runtime -----------------------------------------------------
	// Torque's resolved runtime kind is authoritative. The launch
	// profile's runtime only survives if Torque produced an empty kind
	// (mapRuntimeKind maps RuntimeKindSubprocess|"" → RuntimeSubprocess,
	// so rtKind is always concrete here — this branch is defensive).
	if rtKind != "" {
		plan.Runtime = rtKind
	}
	if !plan.Runtime.Valid() {
		plan.Runtime = agentlaunch.RuntimeSubprocess
	}

	// --- Project -----------------------------------------------------
	// Torque owns the project identity + workdir. LaunchPlan.Validate
	// rejects an empty Project.ID; mirror the inline path's "unscoped"
	// fallback.
	projectID := in.ProjectID
	if projectID == "" {
		projectID = "unscoped"
	}
	plan.Project.ID = projectID
	plan.Project.Root = in.Workdir

	// --- Agent -------------------------------------------------------
	// Torque owns the agent identity + agent-file provenance. The launch
	// profile's labels (if any) are preserved, with Torque's role label
	// layered on top.
	plan.Agent.ID = in.AgentProfile
	plan.Agent.Name = in.AgentProfile
	plan.Agent.RoleFile = in.AgentFilePath
	if in.Role != "" {
		if plan.Agent.Labels == nil {
			plan.Agent.Labels = map[string]string{}
		} else {
			// Copy so the source plan's map is not mutated.
			cp := make(map[string]string, len(plan.Agent.Labels)+1)
			for k, v := range plan.Agent.Labels {
				cp[k] = v
			}
			plan.Agent.Labels = cp
		}
		plan.Agent.Labels["torque.role"] = in.Role
	}

	// --- Workspace ---------------------------------------------------
	// Torque owns the workspace dir tree (Stage 1's WorkspaceCreate).
	// The launch profile's workspace MODE is honored as a base value,
	// but the dirs themselves are always Torque's. Mode must be valid
	// (Validate rejects an unknown/empty mode); fall back to the
	// inline path's WorkspacePersistent when the profile left it unset.
	if !plan.Workspace.Mode.Valid() {
		plan.Workspace.Mode = agentlaunch.WorkspacePersistent
	}
	plan.Workspace.Workdir = in.Workdir
	plan.Workspace.WorkspaceDir = in.WorkspaceDir
	plan.Workspace.TempPrefix = in.BuildDirRoot

	// --- BootProfile -------------------------------------------------
	// Torque always owns boot-prompt composition. The launch profile's
	// boot_profile reference is dropped — Torque's composed system
	// prompt + kickoff markdown replace it inline.
	plan.BootProfile = agentlaunch.BootProfileRef{
		Inline: &agentlaunch.BootProfileInline{
			BootPrompt:  in.SystemPrompt,
			BootContent: in.KickoffMD,
			BootMode:    agentlaunch.BootModePlanted,
		},
	}

	// --- MCP ---------------------------------------------------------
	// The task-scoped loopback URL is always Torque's. The launch
	// profile's MCP allowlist is preserved as a base value.
	plan.MCP.LoopbackURL = in.LoopbackURL

	// --- Injection ---------------------------------------------------
	// Torque owns the native-file projection. The base plan's
	// Injection (BootDirOverlay / NativeFiles) is discarded so a
	// persisted-at-rest launch profile cannot smuggle secret-bearing
	// content into the boot dir.
	plan.Injection = agentlaunch.InjectionSpec{
		NativeFiles: nativeFilesForLaunch(in.AgentFile),
	}

	// --- Metadata ----------------------------------------------------
	// Preserve the launch profile's labels/annotations and layer
	// Torque's provenance annotations on top.
	if plan.Metadata.Annotations == nil {
		plan.Metadata.Annotations = map[string]string{}
	} else {
		cp := make(map[string]string, len(plan.Metadata.Annotations)+3)
		for k, v := range plan.Metadata.Annotations {
			cp[k] = v
		}
		plan.Metadata.Annotations = cp
	}
	plan.Metadata.Annotations["torque.agent_profile"] = in.AgentProfile
	plan.Metadata.Annotations["torque.role"] = in.Role
	plan.Metadata.Annotations["torque.launch_profile"] = "true"

	if err := plan.Validate(); err != nil {
		return agentlaunch.LaunchPlan{}, fmt.Errorf("%w: overlaid plan invalid: %v", ErrLaunchProfile, err)
	}
	return plan, nil
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
// providerplant.Plant resolved (prepared.Env — e.g. CODEX_HOME,
// OPENCODE_CONFIG_DIR) onto Torque's composed env slice. The amendments
// are appended last so they take precedence; os/exec resolves a
// duplicate KEY to the last occurrence, and go-agent-sessions forwards
// the slice verbatim. Iteration order of the map is non-deterministic
// but harmless — each key is distinct (the per-provider amendments
// never collide with each other) and last-wins holds regardless.
func mergePreparedEnv(base []string, amendments map[string]string) []string {
	if len(amendments) == 0 {
		return base
	}
	out := append([]string(nil), base...)
	for k, v := range amendments {
		if k == "" {
			continue
		}
		out = append(out, k+"="+v)
	}
	return out
}

// nativeFilesForLaunch projects Torque agent-file extras onto the
// shared InjectionSpec.NativeFiles surface. Today this is a no-op
// placeholder: Torque's agent-file persona already flows into the
// planted CLAUDE.md/AGENTS.md via the composed SystemPrompt (BootPrompt
// → PlantContext.SystemPrompt), so there is nothing to inject as a
// separate native file. The hook exists so Stage 3 (catalog launch
// profiles) and any future per-agent skill/extra-context-file feature
// can populate it without re-threading the plumbing.
//
// When this starts returning entries, secrets must NOT appear in
// NativeFile.Content — InjectionSpec is persisted at rest.
func nativeFilesForLaunch(_ *agentfile.AgentFile) []agentlaunch.NativeFile {
	return nil
}
