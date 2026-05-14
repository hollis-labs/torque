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

	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// TestBoot_ApiKeyHelperPath_ThreadsIntoSettings asserts that
// Dependencies.ApiKeyHelperPath flows into the planted
// .claude/settings.json as `apiKeyHelper: <path>`. Closes
// CW-20260509-0016: bare-mode subscription users (no
// ANTHROPIC_API_KEY in env) point apiKeyHelper at a small helper that
// reads the macOS keychain and emits the OAuth access token; without
// this wiring the planted settings.json would have no apiKeyHelper
// field and bare mode would fall back to env-var-only auth.
//
// The test reads the planted file directly (not the spawned process's
// argv) because the field is consumed by the file Render closure, not
// by BuildArgs; the SettingsPath argv flag points the subprocess at
// the planted file, but the helper field lives inside that file.
func TestBoot_ApiKeyHelperPath_ThreadsIntoSettings(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{}, "claude")

	// Boot revalidates ApiKeyHelperPath at dispatch time (executable-
	// regular-file check). Use a real fake helper file so the validation
	// succeeds and the field threads through to the planted settings.json.
	helperDir := t.TempDir()
	helperPath := filepath.Join(helperDir, "torque-apikey-helper")
	require.NoError(t, os.WriteFile(helperPath, []byte("#!/bin/sh\necho fake\n"), 0o755))
	cd.Deps.ApiKeyHelperPath = helperPath

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:       "CW-TEST-AKH-001",
		AgentProfile: "torque-backend",
		Workdir:      t.TempDir(),
		Mode:         agent.ModeOneShot,
		Description:  "ignored",
	})
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.NotEmpty(t, sess.BootDir, "Boot must populate BootDir on the returned session")

	// ModeOneShot tears down the boot dir at the end of Boot; capture
	// the planted file path before the tempdir is reaped. The
	// fakeSession's Stop runs synchronously inside Boot, so by here
	// the dir may already be gone — defensive read with stat first.
	settingsPath := filepath.Join(sess.BootDir, ".claude", "settings.json")
	if _, statErr := os.Stat(settingsPath); statErr != nil {
		// Boot dir was already cleaned up. Re-run with ModeLongLived
		// so the dir survives Boot — we still want to assert the
		// planted contents. Production deployment uses ModeOneShot
		// for scheduler-dispatched workers, so the bug we're guarding
		// against would manifest at plant time regardless of mode;
		// long-lived just gives us a stable read window.
		sess2, err := cd.Manager.Boot(ctx, agent.Options{
			TaskID:       "CW-TEST-AKH-002",
			AgentProfile: "torque-backend",
			Workdir:      t.TempDir(),
			Mode:         agent.ModeLongLived,
			Description:  "ignored",
		})
		require.NoError(t, err)
		require.NotNil(t, sess2)
		settingsPath = filepath.Join(sess2.BootDir, ".claude", "settings.json")
	}

	raw, err := os.ReadFile(settingsPath)
	require.NoError(t, err, "planted settings.json must exist at %s", settingsPath)

	// Parse the JSON instead of substring-matching so the assertion
	// survives formatting/order changes in go-providers' settings.json
	// renderer (whitespace, key order, future additions).
	var settings map[string]any
	require.NoError(t, json.Unmarshal(raw, &settings),
		"planted settings.json must be valid JSON; got:\n%s", string(raw))
	assert.Equal(t, helperPath, settings["apiKeyHelper"],
		"planted settings.json apiKeyHelper field should match Deps.ApiKeyHelperPath; got:\n%s", string(raw))
	// Sanity: the existing stub keys still ride along.
	assert.Contains(t, settings, "mcpServers")
	assert.Contains(t, settings, "approvedTools")
}

// TestBoot_ApiKeyHelperPath_AbsentWhenDepsEmpty pins the negative case:
// when Dependencies.ApiKeyHelperPath is empty (the default), the
// planted settings.json contains NO apiKeyHelper field. Backward-
// compat with the v0.9.1 stub shape — bare mode then requires
// ANTHROPIC_API_KEY in env per the existing CW-20260509-0011 contract.
func TestBoot_ApiKeyHelperPath_AbsentWhenDepsEmpty(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{}, "claude")
	// Deps.ApiKeyHelperPath defaults to "" — explicit assert for clarity.
	require.Empty(t, cd.Deps.ApiKeyHelperPath)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:       "CW-TEST-AKH-003",
		AgentProfile: "torque-backend",
		Workdir:      t.TempDir(),
		Mode:         agent.ModeLongLived,
		Description:  "ignored",
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	raw, err := os.ReadFile(filepath.Join(sess.BootDir, ".claude", "settings.json"))
	require.NoError(t, err)

	var settings map[string]any
	require.NoError(t, json.Unmarshal(raw, &settings),
		"planted settings.json must be valid JSON; got:\n%s", string(raw))
	_, hasHelper := settings["apiKeyHelper"]
	assert.False(t, hasHelper,
		"planted settings.json must NOT contain apiKeyHelper when Deps.ApiKeyHelperPath is empty; got:\n%s", string(raw))
}
