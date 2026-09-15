package agent_boot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// plantedPermissionMode boots a long-lived claude-code session against the
// given profile and returns the permissions.defaultMode value from the
// planted .claude/settings.json. Long-lived mode keeps the boot dir alive
// past Boot so the planted file can be read.
func plantedPermissionMode(t *testing.T, cd *composedDeps, profileName string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:       "CW-TEST-PERM-" + profileName,
		AgentProfile: profileName,
		Workdir:      t.TempDir(),
		Mode:         agent.ModeLongLived,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.NotEmpty(t, sess.BootDir)
	start := cd.Runtime.lastStartOpts.Load()
	require.NotNil(t, start)
	settingsPath := filepath.Join(sess.BootDir, ".claude", "settings.json")
	var explicitSettings string
	for i, arg := range start.ExtraArgs {
		if arg == "--settings" && i+1 < len(start.ExtraArgs) {
			explicitSettings = start.ExtraArgs[i+1]
		}
	}
	assert.Equal(t, settingsPath, explicitSettings, "headless Claude must explicitly load its configured permissions")

	raw, err := os.ReadFile(filepath.Join(sess.BootDir, ".claude", "settings.json"))
	require.NoError(t, err, "planted settings.json must exist")
	var settings map[string]any
	require.NoError(t, json.Unmarshal(raw, &settings),
		"planted settings.json must be valid JSON; got:\n%s", string(raw))
	perms, ok := settings["permissions"].(map[string]any)
	require.True(t, ok, "planted settings.json must carry a permissions object; got:\n%s", string(raw))
	mode, _ := perms["defaultMode"].(string)
	return mode
}

// TestBoot_PermissionMode_DefaultPlantsAcceptEdits is the CW-20260517-0038
// Variation 3 regression: a non-dev claude-code profile with no
// permission_mode must plant permissions.defaultMode=acceptEdits — the
// safe, non-interactive middle ground — instead of leaving the spawned
// claude in `default` mode where it hangs on the first approval prompt.
func TestBoot_PermissionMode_DefaultPlantsAcceptEdits(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")

	assert.Equal(t, "acceptEdits", plantedPermissionMode(t, cd, "torque-backend"),
		"a profile with no permission_mode must plant the default acceptEdits posture")
}

// TestBoot_PermissionMode_ExplicitModePlanted asserts an explicit
// permission_mode on the profile is honored verbatim in the planted file.
func TestBoot_PermissionMode_ExplicitModePlanted(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")
	cd.Deps.Profiles = config.ProfileMap{
		"plan-mode": config.AgentProfile{
			Executor:       "cli",
			Provider:       "claude-code",
			PermissionMode: string(config.PermissionModePlan),
		},
	}

	assert.Equal(t, "plan", plantedPermissionMode(t, cd, "plan-mode"),
		"an explicit permission_mode must be planted verbatim")
}

// TestBoot_PermissionMode_DevFlagStillBypasses is the backward-compat
// guard: a profile carrying the legacy --dangerously-skip-permissions arg
// must still plant bypassPermissions. The dev path sets SkipPermissions on
// the adapter, claudeSettingsStub plants bypassPermissions, and Torque's
// post-processing is a no-op (it must not downgrade the dev posture).
func TestBoot_PermissionMode_DevFlagStillBypasses(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")
	cd.Deps.Profiles = config.ProfileMap{
		"dev-mode": config.AgentProfile{
			Executor: "cli",
			Provider: "claude-code",
			Args:     []string{"--dangerously-skip-permissions"},
		},
	}

	assert.Equal(t, "bypassPermissions", plantedPermissionMode(t, cd, "dev-mode"),
		"the legacy --dangerously-skip-permissions arg must still plant bypassPermissions")
}
