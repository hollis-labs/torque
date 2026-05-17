package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// plantSettings writes a .claude/settings.json with the given JSON content
// under a fresh boot dir and returns (bootDir, settingsPath).
func plantSettings(t *testing.T, content string) (string, string) {
	t.Helper()
	bootDir := t.TempDir()
	settingsPath := filepath.Join(bootDir, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(settingsPath), 0o700))
	require.NoError(t, os.WriteFile(settingsPath, []byte(content), 0o600))
	return bootDir, settingsPath
}

// readSettings parses the planted settings.json back into a map.
func readSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

// TestApplyPermissionMode_InjectsIntoEmptyStub is the core
// CW-20260517-0038 V3 case: a non-dev claude-code boot plants `{}` (no
// permissions block); post-processing must add permissions.defaultMode.
func TestApplyPermissionMode_InjectsIntoEmptyStub(t *testing.T) {
	bootDir, settingsPath := plantSettings(t, `{}`)

	require.NoError(t, applyPermissionMode(bootDir, config.PermissionModeAcceptEdits, false))

	settings := readSettings(t, settingsPath)
	perms, ok := settings["permissions"].(map[string]any)
	require.True(t, ok, "permissions block must be present after post-processing")
	assert.Equal(t, "acceptEdits", perms["defaultMode"])
}

// TestApplyPermissionMode_PreservesOtherKeys asserts the merge does not
// clobber sibling keys (e.g. apiKeyHelper) or an existing permissions
// sub-key.
func TestApplyPermissionMode_PreservesOtherKeys(t *testing.T) {
	bootDir, settingsPath := plantSettings(t, `{
  "apiKeyHelper": "/usr/local/bin/torque-apikey-helper",
  "permissions": {"allow": ["Read"]}
}`)

	require.NoError(t, applyPermissionMode(bootDir, config.PermissionModePlan, false))

	settings := readSettings(t, settingsPath)
	assert.Equal(t, "/usr/local/bin/torque-apikey-helper", settings["apiKeyHelper"],
		"apiKeyHelper must survive the merge")
	perms := settings["permissions"].(map[string]any)
	assert.Equal(t, "plan", perms["defaultMode"], "defaultMode must be set")
	assert.Equal(t, []any{"Read"}, perms["allow"], "existing permissions.allow must survive")
}

// TestApplyPermissionMode_EmptyModeDefaults asserts an empty mode argument
// falls back to the package default (acceptEdits).
func TestApplyPermissionMode_EmptyModeDefaults(t *testing.T) {
	bootDir, settingsPath := plantSettings(t, `{}`)

	require.NoError(t, applyPermissionMode(bootDir, "", false))

	perms := readSettings(t, settingsPath)["permissions"].(map[string]any)
	assert.Equal(t, "acceptEdits", perms["defaultMode"])
}

// TestApplyPermissionMode_DevBypassIsNoOp asserts the dev path
// (--dangerously-skip-permissions → SkipPermissions → adapter already
// planted bypassPermissions) is left untouched: post-processing must not
// fight the dev path or downgrade its mode.
func TestApplyPermissionMode_DevBypassIsNoOp(t *testing.T) {
	bootDir, settingsPath := plantSettings(t, `{
  "permissions": {"defaultMode": "bypassPermissions"}
}`)

	// devModeBypass=true → no-op even though a weaker mode is requested.
	require.NoError(t, applyPermissionMode(bootDir, config.PermissionModeAcceptEdits, true))

	perms := readSettings(t, settingsPath)["permissions"].(map[string]any)
	assert.Equal(t, "bypassPermissions", perms["defaultMode"],
		"dev-mode bypass must be preserved — post-processing must not downgrade it")
}

// TestApplyPermissionMode_NoBootDirIsNoOp asserts an empty bootDir (an
// adapter with no BootDirSpec) is a clean no-op.
func TestApplyPermissionMode_NoBootDirIsNoOp(t *testing.T) {
	require.NoError(t, applyPermissionMode("", config.PermissionModeAcceptEdits, false))
}

// TestApplyPermissionMode_MissingFileTreatedAsEmpty asserts a missing
// settings.json is treated as an empty object — the merge still produces a
// valid file rather than erroring.
func TestApplyPermissionMode_MissingFileTreatedAsEmpty(t *testing.T) {
	bootDir := t.TempDir() // no .claude/settings.json planted

	require.NoError(t, applyPermissionMode(bootDir, config.PermissionModeBypass, false))

	settingsPath := filepath.Join(bootDir, ".claude", "settings.json")
	perms := readSettings(t, settingsPath)["permissions"].(map[string]any)
	assert.Equal(t, "bypassPermissions", perms["defaultMode"])
}

// TestApplyPermissionMode_MalformedFileIsError asserts a settings.json
// that is not valid JSON is a hard error — Torque refuses to clobber a
// file it cannot safely parse.
func TestApplyPermissionMode_MalformedFileIsError(t *testing.T) {
	bootDir, _ := plantSettings(t, `{not valid json`)

	err := applyPermissionMode(bootDir, config.PermissionModeAcceptEdits, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse planted")
}
