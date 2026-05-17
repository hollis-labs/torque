package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeProfiles is a small helper: writes content to a temp profiles.yaml
// and returns its path.
func writeProfiles(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// TestPermissionMode_ParsedFromYAML asserts the permission_mode key is
// unmarshalled onto AgentProfile.PermissionMode (CW-20260517-0038 V3).
func TestPermissionMode_ParsedFromYAML(t *testing.T) {
	path := writeProfiles(t, `
agent_profiles:
  plan-only:
    executor: cli
    provider: claude-code
    command: claude
    permission_mode: plan
`)
	profiles, err := config.LoadProfiles(path)
	require.NoError(t, err)

	prof, ok := profiles["plan-only"]
	require.True(t, ok)
	assert.Equal(t, "plan", prof.PermissionMode)
	assert.Equal(t, config.PermissionModePlan, prof.ResolvedPermissionMode())
}

// TestPermissionMode_DefaultWhenUnset pins the unset-means-acceptEdits
// contract: a profile with no permission_mode key resolves to the safe
// non-interactive middle ground.
func TestPermissionMode_DefaultWhenUnset(t *testing.T) {
	path := writeProfiles(t, `
agent_profiles:
  no-mode:
    executor: cli
    provider: claude-code
    command: claude
`)
	profiles, err := config.LoadProfiles(path)
	require.NoError(t, err)

	prof := profiles["no-mode"]
	assert.Equal(t, "", prof.PermissionMode, "raw field stays empty when the key is absent")
	assert.Equal(t, config.PermissionModeAcceptEdits, prof.ResolvedPermissionMode(),
		"unset permission_mode must resolve to acceptEdits")
	assert.Equal(t, config.PermissionModeAcceptEdits, config.DefaultPermissionMode)
}

// TestPermissionMode_AllValidValues confirms each of the four
// settings-schema modes round-trips through load + resolution.
func TestPermissionMode_AllValidValues(t *testing.T) {
	for _, mode := range []config.PermissionMode{
		config.PermissionModeDefault,
		config.PermissionModeAcceptEdits,
		config.PermissionModePlan,
		config.PermissionModeBypass,
	} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			path := writeProfiles(t, `
agent_profiles:
  p:
    executor: cli
    provider: claude-code
    command: claude
    permission_mode: `+string(mode)+`
`)
			profiles, err := config.LoadProfiles(path)
			require.NoError(t, err)
			assert.Equal(t, mode, profiles["p"].ResolvedPermissionMode())
		})
	}
}

// TestPermissionMode_InvalidValueRejected asserts a typo'd permission_mode
// is a load-time error that names the profile, the bad value, and the
// valid set — consistent with the registry-naming error style.
func TestPermissionMode_InvalidValueRejected(t *testing.T) {
	path := writeProfiles(t, `
agent_profiles:
  bad:
    executor: cli
    provider: claude-code
    command: claude
    permission_mode: accept-edits
`)
	_, err := config.LoadProfiles(path)
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, `"bad"`, "error must name the offending profile")
	assert.Contains(t, msg, `"accept-edits"`, "error must echo the invalid value")
	assert.Contains(t, msg, "acceptEdits", "error must list the valid values")
	assert.Contains(t, msg, "bypassPermissions")
	assert.Contains(t, msg, "profiles.yaml", "error must name the file")

	// LoadProfilesFile (the alias-aware loader) must reject it too.
	_, err = config.LoadProfilesFile(path)
	require.Error(t, err)
}
