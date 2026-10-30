package launchprofile

import (
	"fmt"

	"github.com/hollis-labs/torque/internal/config"
)

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
// Resolve never returns an error — it is the lenient form for display and
// snapshot callers, and falls back to config.GetProfileOrDefault for a name
// that does not resolve. Dispatch and boot paths must use ResolveStrict so an
// unknown profile fails loudly instead of running as a zero profile.
func Resolve(req ResolveRequest) CompiledLaunchProfile {
	if c, err := ResolveStrict(req); err == nil {
		return c
	}
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

// ResolveStrict is Resolve for the dispatch and boot paths: a profile name that
// is neither a builtin nor a key (or alias) in the agent_profiles registry
// returns an error naming the profile and the valid ones, instead of a zero
// AgentProfile that fails later as "profile has empty provider". Empty
// selectors resolve to the `default` profile (profiles.yaml's own, else the
// builtin).
func ResolveStrict(req ResolveRequest) (CompiledLaunchProfile, error) {
	profile, provenance := selectLaunchProfile(req)
	agentProf, err := config.ResolveProfile(config.CurrentProfiles(req.Source), profile.AgentProfile)
	if err != nil {
		field, name := "agent_profile", req.LegacyAgentProfile
		if req.LaunchProfile != "" {
			field, name = "launch_profile", req.LaunchProfile
		}
		return CompiledLaunchProfile{}, fmt.Errorf("%s %q does not resolve: %w", field, name, err)
	}
	return CompiledLaunchProfile{
		Profile:          profile,
		AgentProfile:     agentProf,
		AgentProfileName: profile.AgentProfile,
		Provenance:       provenance,
	}, nil
}

// ValidateSelectors reports whether a launch_profile / agent_profile pair
// resolves. Empty values are valid (they mean `default`). Used by the service
// layer at task and template writes.
func ValidateSelectors(source config.ProfileSource, launchProfile, agentProfile string) error {
	_, err := ResolveStrict(ResolveRequest{LaunchProfile: launchProfile, LegacyAgentProfile: agentProfile, Source: source})
	return err
}
