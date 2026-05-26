package launchprofile

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/hollis-labs/torque/internal/config"
)

// staticSource is a fixed-snapshot ProfileSource for tests. Avoids the
// ReloadableProfiles indirection so a test can compare exactly the
// AgentProfile resolution path.
type staticSource struct{ m config.ProfileMap }

func (s staticSource) CurrentProfiles() config.ProfileMap { return s.m }

func TestResolve_ExplicitLaunchProfile_BuiltinHit(t *testing.T) {
	src := staticSource{m: config.ProfileMap{
		"orchestrator": {Executor: "cli", Provider: "opencode", TimeoutSeconds: 1800},
	}}
	got := Resolve(ResolveRequest{
		LaunchProfile: "orchestrator.default",
		Source:        src,
	})
	assert.Equal(t, "orchestrator.default", got.Profile.ID)
	assert.Equal(t, "orchestrator", got.Profile.Role)
	assert.Equal(t, "orchestrator", got.AgentProfileName)
	assert.Equal(t, "opencode", got.AgentProfile.Provider)
	assert.Equal(t, ProvenanceExplicit, got.Provenance)
}

func TestResolve_ExplicitLaunchProfile_PassthroughForUnknown(t *testing.T) {
	src := staticSource{m: config.ProfileMap{
		"codex-gpt5-long": {Executor: "cli", Provider: "codex"},
	}}
	got := Resolve(ResolveRequest{
		LaunchProfile: "codex-gpt5-long",
		Source:        src,
	})
	assert.Equal(t, "codex-gpt5-long", got.Profile.ID)
	assert.Equal(t, "codex-gpt5-long", got.AgentProfileName)
	assert.Equal(t, "codex", got.AgentProfile.Provider)
	assert.Equal(t, ProvenanceExplicit, got.Provenance)
}

func TestResolve_LegacyAgentProfile_MapsToBuiltin(t *testing.T) {
	src := staticSource{m: config.ProfileMap{}}
	got := Resolve(ResolveRequest{
		LegacyAgentProfile: "orchestrator",
		Source:             src,
	})
	assert.Equal(t, "orchestrator.default", got.Profile.ID)
	assert.Equal(t, "orchestrator", got.AgentProfileName)
	assert.Equal(t, ProvenanceLegacyMap, got.Provenance)
}

func TestResolve_LegacyAgentProfile_Passthrough(t *testing.T) {
	src := staticSource{m: config.ProfileMap{
		"codex-gpt5-long": {Executor: "cli", Provider: "codex"},
	}}
	got := Resolve(ResolveRequest{
		LegacyAgentProfile: "codex-gpt5-long",
		Source:             src,
	})
	assert.Equal(t, "codex-gpt5-long", got.Profile.ID)
	assert.Equal(t, "codex-gpt5-long", got.AgentProfileName)
	assert.Equal(t, "codex", got.AgentProfile.Provider)
	assert.Equal(t, ProvenanceLegacyPassthrough, got.Provenance)
}

func TestResolve_LaunchProfileWinsOverLegacy(t *testing.T) {
	src := staticSource{m: config.ProfileMap{
		"planner":      {Executor: "cli", Provider: "opencode", TimeoutSeconds: 600},
		"orchestrator": {Executor: "cli", Provider: "opencode", TimeoutSeconds: 1800},
	}}
	got := Resolve(ResolveRequest{
		LaunchProfile:      "planner.default",
		LegacyAgentProfile: "orchestrator", // ignored when launch_profile is supplied
		Source:             src,
	})
	assert.Equal(t, "planner.default", got.Profile.ID)
	assert.Equal(t, "planner", got.AgentProfileName)
	assert.Equal(t, ProvenanceExplicit, got.Provenance)
}

func TestResolve_BothEmpty_ReturnsDefault(t *testing.T) {
	got := Resolve(ResolveRequest{})
	assert.Equal(t, BuiltinDefault, got.Profile.ID)
	assert.Equal(t, ProvenanceDefault, got.Provenance)
}

func TestMapLegacyAgentProfile(t *testing.T) {
	cases := []struct {
		in     string
		wantID string
		wantOK bool
	}{
		{"orchestrator", "orchestrator.default", true},
		{"planner", "planner.default", true},
		{"reviewer-end-agent", "reviewer.code", true},
		{"default", BuiltinDefault, true},
		{"", BuiltinDefault, true},
		{"codex-gpt5-long", "", false},
		{"unknown", "", false},
	}
	for _, tc := range cases {
		gotID, gotOK := MapLegacyAgentProfile(tc.in)
		assert.Equal(t, tc.wantID, gotID, "in=%s", tc.in)
		assert.Equal(t, tc.wantOK, gotOK, "in=%s", tc.in)
	}
}

func TestBuiltinProfiles_ContainsExpectedSet(t *testing.T) {
	got := BuiltinProfiles()
	want := []string{
		"orchestrator.default",
		"planner.default",
		"worker.implementer",
		"reviewer.code",
		BuiltinDefault,
	}
	for _, id := range want {
		_, ok := got[id]
		assert.True(t, ok, "builtin %q missing from registry", id)
	}
}

func TestBuiltinProfiles_DefensiveCopy(t *testing.T) {
	got := BuiltinProfiles()
	got["intruder"] = LaunchProfile{ID: "intruder"}
	again := BuiltinProfiles()
	_, ok := again["intruder"]
	assert.False(t, ok, "mutation of returned map leaked into the registry")
}
