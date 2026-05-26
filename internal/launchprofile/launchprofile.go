// Package launchprofile is Torque's first-class launch-definition layer.
//
// A LaunchProfile is the user-facing "what kind of agent am I launching?"
// selector that tasks/templates/sessions carry. At boot it resolves into a
// CompiledLaunchProfile (the stable launch family — provider/runner defaults,
// role, base provider/runtime config), which BuildLaunchPlan combines with a
// TaskLaunchOverlay (per-task dynamic values — workdir, task bundle, MCP
// loopback) into the shared agentlaunch.LaunchPlan consumed by
// launcher.Compile / launcher.Prepare / providerplant.Plant.
//
// The split exists so the stable 80-90% of agent boots can be expressed once
// per launch family while Torque keeps authoritative ownership of task-
// scoped dynamic overlays. The package is Torque-native — it does not bolt
// Tether/Nanite catalogs into Torque, though future revisions can extend
// LaunchProfile to express richer planted-context / tool / procedure
// surfaces without changing this contract.
//
// Compatibility: the legacy `agent_profile` field is honored on every
// surface. When a request supplies only `agent_profile`, the resolver maps
// it to a builtin LaunchProfile (or synthesizes a pass-through profile that
// wraps the legacy name verbatim).
package launchprofile

import "github.com/hollis-labs/torque/internal/config"

// LaunchProfile is the user-facing launch family selector. It captures the
// stable launch shape — what role the agent serves, which low-level
// provider/runtime config to compile, default metadata to stamp into the
// session.
//
// Per-task dynamic inputs (workdir, task ID, planted task bundle, MCP
// loopback URL, …) flow in via TaskLaunchOverlay and are layered on top
// during BuildLaunchPlan.
type LaunchProfile struct {
	// ID is the canonical launch profile identifier (e.g.
	// "orchestrator.default", "worker.implementer"). Stable across the
	// API/GUI/persistence surface.
	ID string

	// DisplayName is the human-readable name shown in catalogs and the GUI.
	DisplayName string

	// Description is operator-facing prose for catalogs / tooltips.
	Description string

	// Role is the human-readable role tag (orchestrator, planner, worker,
	// reviewer-end-agent, executor). Flows into the spawned session's
	// SessionMeta and into the loopback MCP tool-surface selector — kind=agent
	// workers get the restricted self-task subset; orchestrator-class roles
	// get the full cross-task surface.
	Role string

	// AgentProfile is the low-level provider/runtime config key. Phase 1
	// delegates to the legacy agent_profiles registry (profiles.yaml) for the
	// provider/model/args/runtime details; LaunchProfile.AgentProfile is the
	// lookup key into that registry. Future revisions may carry these fields
	// inline.
	AgentProfile string

	// Annotations is free-form key=value metadata stamped into the launch
	// plan's Metadata.Annotations surface for forensic tooling. Optional.
	Annotations map[string]string
}

// CompiledLaunchProfile is the resolved, defaulted form of a LaunchProfile
// after Resolve(). Carries the stable launch family info plus the resolved
// underlying config.AgentProfile — enough for BuildLaunchPlan to assemble
// an agentlaunch.LaunchPlan once the per-task overlay is layered on.
type CompiledLaunchProfile struct {
	// Profile is the resolved LaunchProfile object — either a builtin, an
	// operator-supplied profile, or a synthetic pass-through one (legacy
	// agent_profile compatibility path).
	Profile LaunchProfile

	// AgentProfile is the resolved low-level config carrying
	// provider/model/args/runtime details. Comes from the agent_profiles
	// registry keyed by Profile.AgentProfile, falling back to defaults.
	AgentProfile config.AgentProfile

	// AgentProfileName is the registry key used to look up AgentProfile.
	// Stamped into launch plan metadata for forensic tooling.
	AgentProfileName string

	// Provenance records how this CompiledLaunchProfile was produced. Used
	// by logs and tests to verify the precedence rules.
	Provenance ResolveProvenance
}

// ResolveProvenance describes how a CompiledLaunchProfile was produced.
// Useful for log lines and resolver tests.
type ResolveProvenance string

const (
	// ProvenanceExplicit means a launch_profile value was supplied and
	// resolved (either against the builtin registry or as a synthetic
	// pass-through to an operator-configured agent_profile).
	ProvenanceExplicit ResolveProvenance = "explicit"

	// ProvenanceLegacyMap means launch_profile was empty and the legacy
	// agent_profile value mapped onto a builtin launch profile.
	ProvenanceLegacyMap ResolveProvenance = "legacy_map"

	// ProvenanceLegacyPassthrough means launch_profile was empty and the
	// legacy agent_profile value did not match a builtin — a synthetic
	// LaunchProfile wrapping the agent_profile verbatim is returned.
	ProvenanceLegacyPassthrough ResolveProvenance = "legacy_passthrough"

	// ProvenanceDefault means neither launch_profile nor agent_profile
	// was supplied — the "default" builtin is returned.
	ProvenanceDefault ResolveProvenance = "default"
)
