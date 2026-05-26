package launchprofile

// legacyAgentProfileMap maps a legacy agent_profile value onto a builtin
// launch profile ID. Used by Resolve when only agent_profile is supplied.
// Unknown legacy values fall through to the synthetic-passthrough path,
// where Resolve materializes a LaunchProfile that wraps the legacy name
// verbatim — preserving the prior behavior for operator-configured custom
// profile names (e.g. "codex-gpt5-long" in profiles.yaml).
var legacyAgentProfileMap = map[string]string{
	"orchestrator":       "orchestrator.default",
	"planner":            "planner.default",
	"reviewer-end-agent": "reviewer.code",
	"default":            BuiltinDefault,
	"":                   BuiltinDefault,
}

// MapLegacyAgentProfile returns the builtin launch profile ID a legacy
// agent_profile value maps to, plus a boolean indicating whether the
// mapping is a known builtin (true) or should fall through to a synthetic
// pass-through (false).
//
// Callers in the resolver use the bool to decide whether to look up the
// builtin or synthesize a wrapper profile that keeps the legacy name as
// its AgentProfile lookup key.
func MapLegacyAgentProfile(agentProfile string) (string, bool) {
	id, ok := legacyAgentProfileMap[agentProfile]
	return id, ok
}

// passthroughProfile synthesizes a LaunchProfile that wraps an operator-
// configured agent_profile value Torque has no built-in launch family for.
// Used by Resolve on the legacy-passthrough path so a profile like
// "codex-gpt5-long" still produces a usable CompiledLaunchProfile without
// requiring the operator to first register a matching launch_profile.
func passthroughProfile(agentProfile string) LaunchProfile {
	return LaunchProfile{
		ID:           agentProfile,
		DisplayName:  agentProfile,
		Description:  "Pass-through launch profile wrapping legacy agent_profile " + agentProfile + ".",
		Role:         agentProfile,
		AgentProfile: agentProfile,
	}
}
