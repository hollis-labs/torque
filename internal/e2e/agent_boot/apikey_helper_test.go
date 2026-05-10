package agent_boot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/clockwork-manifold/internal/runtime/agent"
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
	cd.Deps.ApiKeyHelperPath = "/usr/local/bin/clockwork-apikey-helper"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:       "CW-TEST-AKH-001",
		AgentProfile: "clockwork-backend",
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
			AgentProfile: "clockwork-backend",
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
	got := string(raw)

	assert.Contains(t, got, `"apiKeyHelper": "/usr/local/bin/clockwork-apikey-helper"`,
		"planted settings.json should contain the apiKeyHelper field; got:\n%s", got)
	// Sanity: the existing stub keys still ride along.
	assert.Contains(t, got, `"mcpServers"`)
	assert.Contains(t, got, `"approvedTools"`)
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
		AgentProfile: "clockwork-backend",
		Workdir:      t.TempDir(),
		Mode:         agent.ModeLongLived,
		Description:  "ignored",
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	raw, err := os.ReadFile(filepath.Join(sess.BootDir, ".claude", "settings.json"))
	require.NoError(t, err)
	got := string(raw)
	assert.False(t, strings.Contains(got, "apiKeyHelper"),
		"planted settings.json must NOT contain apiKeyHelper when Deps.ApiKeyHelperPath is empty; got:\n%s", got)
}
