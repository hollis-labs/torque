package launchprofile

import "maps"

// BuiltinDefault is the launch profile ID returned when neither
// launch_profile nor agent_profile is supplied on a Boot request.
const BuiltinDefault = "default"

// builtinProfiles is the substrate's stock set of launch families. They
// cover the organic boot shapes Torque already produces (orchestrator,
// planner, reviewer-end-agent, worker, default) so a fresh install can
// dispatch tasks without an external catalog.
//
// The AgentProfile values reference the legacy agent_profiles registry —
// operators register matching keys in profiles.yaml to override the
// provider/model/runtime details. When a key is missing from the registry
// the resolver falls back to config.GetProfileOrDefault's own builtins,
// then to the zero-value profile (matching the legacy behavior).
//
// Add an entry here when shipping a new substrate-spawned role.
var builtinProfiles = map[string]LaunchProfile{
	"orchestrator.default": {
		ID:           "orchestrator.default",
		DisplayName:  "Orchestrator",
		Description:  "Long-lived plan walker — dispatches phases sequentially and waits for reviewer-end-agent to clear each child.",
		Role:         "orchestrator",
		AgentProfile: "orchestrator",
	},
	"planner.default": {
		ID:           "planner.default",
		DisplayName:  "Planner",
		Description:  "Short-lived plan refinement pass before the orchestrator walks phases.",
		Role:         "planner",
		AgentProfile: "planner",
	},
	"worker.implementer": {
		ID:           "worker.implementer",
		DisplayName:  "Implementer",
		Description:  "Default executor for kind=agent worker tasks.",
		Role:         "executor",
		AgentProfile: "default",
	},
	"reviewer.code": {
		ID:           "reviewer.code",
		DisplayName:  "Code Reviewer",
		Description:  "Substrate-spawned reviewer end-agent for kind=agent → review transitions.",
		Role:         "reviewer-end-agent",
		AgentProfile: "reviewer-end-agent",
	},
	BuiltinDefault: {
		ID:           BuiltinDefault,
		DisplayName:  "Default",
		Description:  "Generic default executor — the safety-net launch family.",
		Role:         "executor",
		AgentProfile: "default",
	},
}

// BuiltinProfiles returns a defensive copy of the builtin catalog. Callers
// that need to enumerate launch families (listings, validation) use this.
func BuiltinProfiles() map[string]LaunchProfile {
	out := make(map[string]LaunchProfile, len(builtinProfiles))
	maps.Copy(out, builtinProfiles)
	return out
}

// LookupBuiltin returns the builtin LaunchProfile with the given ID and a
// boolean indicating whether the entry exists. Operator-overridable
// resolution lives in Resolve; this is the raw catalog accessor.
func LookupBuiltin(id string) (LaunchProfile, bool) {
	p, ok := builtinProfiles[id]
	return p, ok
}
