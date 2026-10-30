package launchprofile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
)

// CW-20261030-0207: an absent profile resolves to the default profile, never
// a zero AgentProfile; an unknown one fails naming the profile.
func TestResolveStrict_NoProfileResolvesToBuiltinDefault(t *testing.T) {
	got, err := ResolveStrict(ResolveRequest{Source: staticSource{m: config.ProfileMap{}}})
	require.NoError(t, err)
	assert.Equal(t, BuiltinDefault, got.Profile.ID)
	assert.Equal(t, ProvenanceDefault, got.Provenance)
	assert.NotEmpty(t, got.AgentProfile.Provider, "default must not be a zero profile")
}

func TestResolveStrict_WorkerImplementerResolvesToDefault(t *testing.T) {
	got, err := ResolveStrict(ResolveRequest{LaunchProfile: "worker.implementer", Source: staticSource{m: config.ProfileMap{}}})
	require.NoError(t, err)
	assert.Equal(t, "default", got.AgentProfileName)
	assert.NotEmpty(t, got.AgentProfile.Provider)
}

func TestResolveStrict_OperatorDefaultWins(t *testing.T) {
	src := staticSource{m: config.ProfileMap{"default": {Executor: "cli", Provider: "codex"}}}
	got, err := ResolveStrict(ResolveRequest{Source: src})
	require.NoError(t, err)
	assert.Equal(t, "codex", got.AgentProfile.Provider)
}

func TestResolveStrict_UnknownProfileFailsLoudly(t *testing.T) {
	src := staticSource{m: config.ProfileMap{"implementer": {Provider: "claude-code"}}}
	for name, req := range map[string]ResolveRequest{
		"agent_profile":  {LegacyAgentProfile: "tangent-worker-codex"},
		"launch_profile": {LaunchProfile: "tangent-codex-app-server-worktree"},
	} {
		req.Source = src
		_, err := ResolveStrict(req)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), name)
		assert.Contains(t, err.Error(), "implementer", "lists the valid profiles")
	}
	// The lenient Resolve still never errors.
	assert.NotPanics(t, func() { Resolve(ResolveRequest{LegacyAgentProfile: "nope", Source: src}) })
}

func TestValidateSelectors(t *testing.T) {
	src := staticSource{m: config.ProfileMap{"implementer": {Provider: "claude-code"}}}
	assert.NoError(t, ValidateSelectors(src, "", ""))
	assert.NoError(t, ValidateSelectors(src, "", "implementer"))
	assert.NoError(t, ValidateSelectors(src, "worker.implementer", ""))
	assert.Error(t, ValidateSelectors(src, "", "typo"))
}
