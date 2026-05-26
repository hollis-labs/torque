package launchprofile

import "github.com/hollis-labs/torque/internal/config"

// ResolveRequest bundles every input Resolve needs. Callers populate the
// LaunchProfile field with the preferred user-facing selector, and
// LegacyAgentProfile with the deprecated fallback (still honored during
// migration).
type ResolveRequest struct {
	// LaunchProfile is the user-facing launch family ID (preferred). Set
	// from the task/template/session row when present.
	LaunchProfile string

	// LegacyAgentProfile is the deprecated agent_profile value. Honored
	// when LaunchProfile is empty for backward compatibility.
	LegacyAgentProfile string

	// Source is the operator-configured agent_profiles registry (loaded
	// from profiles.yaml). Resolve uses it to materialize the underlying
	// config.AgentProfile via config.GetProfileOrDefault.
	Source config.ProfileSource
}

// Resolve picks the LaunchProfile that should govern a Boot call, then
// resolves the underlying config.AgentProfile via the agent_profiles
// registry. Precedence:
//
//  1. Explicit LaunchProfile that hits the builtin catalog → builtin
//     profile, provenance=explicit.
//  2. Explicit LaunchProfile that does not hit the catalog → synthesized
//     pass-through (treated as a direct agent_profile lookup key).
//     Provenance=explicit. This lets operators reference a profiles.yaml
//     entry directly via launch_profile without registering a builtin
//     LaunchProfile for it.
//  3. Empty LaunchProfile + LegacyAgentProfile that matches a known legacy
//     name → mapped builtin, provenance=legacy_map.
//  4. Empty LaunchProfile + LegacyAgentProfile that does not match a known
//     legacy name → synthesized pass-through wrapping the legacy name,
//     provenance=legacy_passthrough.
//  5. Both empty → "default" builtin, provenance=default.
//
// Resolve never returns an error — every input shape produces a valid
// CompiledLaunchProfile. Misconfigured agent_profile values surface later
// at config.GetProfileOrDefault's log line and at adapterFor in the boot
// path, matching the legacy resolver's behavior.
func Resolve(req ResolveRequest) CompiledLaunchProfile {
	profile, provenance := selectLaunchProfile(req)
	agentProf := config.GetProfileOrDefault(req.Source, profile.AgentProfile)
	return CompiledLaunchProfile{
		Profile:          profile,
		AgentProfile:     agentProf,
		AgentProfileName: profile.AgentProfile,
		Provenance:       provenance,
	}
}

// selectLaunchProfile applies the precedence rules above. Kept separate
// from Resolve so unit tests can verify the selection without touching the
// agent_profiles registry.
func selectLaunchProfile(req ResolveRequest) (LaunchProfile, ResolveProvenance) {
	if req.LaunchProfile != "" {
		if p, ok := LookupBuiltin(req.LaunchProfile); ok {
			return p, ProvenanceExplicit
		}
		return passthroughProfile(req.LaunchProfile), ProvenanceExplicit
	}

	if req.LegacyAgentProfile != "" {
		if id, ok := MapLegacyAgentProfile(req.LegacyAgentProfile); ok {
			if p, builtin := LookupBuiltin(id); builtin {
				return p, ProvenanceLegacyMap
			}
		}
		return passthroughProfile(req.LegacyAgentProfile), ProvenanceLegacyPassthrough
	}

	// Both empty — fall back to the default builtin.
	if p, ok := LookupBuiltin(BuiltinDefault); ok {
		return p, ProvenanceDefault
	}

	// Defensive fallback — the builtin map always contains BuiltinDefault,
	// but synthesize a zero-value profile if some future refactor removes
	// it, so Resolve still produces a non-empty CompiledLaunchProfile.
	return LaunchProfile{ID: BuiltinDefault, AgentProfile: "default"}, ProvenanceDefault
}
