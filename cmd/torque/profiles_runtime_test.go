package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveProfilesPathDefaultsToTorqueDataDir(t *testing.T) {
	cfg := &config.Config{DataDir: filepath.Join(t.TempDir(), "dogfood")}
	t.Setenv("TORQUE_PROFILES_PATH", "")

	path, err := resolveProfilesPath(cfg)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(cfg.DataDir, "profiles.yaml"), path)
}

func TestReconcileLegacyProfilesPathPromotesCanonicalAndLinksLegacy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	canonical := filepath.Join(home, ".torque", "dogfood", "profiles.yaml")
	legacy := filepath.Join(home, ".clockwork", "dogfood", "profiles.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(legacy), 0o755))
	require.NoError(t, os.WriteFile(legacy, []byte("agent_profiles:\n  default:\n    provider: codex\n"), 0o644))

	require.NoError(t, reconcileLegacyProfilesPath(canonical))

	got, err := os.ReadFile(canonical)
	require.NoError(t, err)
	assert.Contains(t, string(got), "provider: codex")

	target, err := os.Readlink(legacy)
	require.NoError(t, err)
	assert.Equal(t, canonical, target)
}

func TestWatchProfilesReloadsUpdatedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
agent_profiles:
  default:
    provider: claude
    model: claude-sonnet-4-20250514
`), 0o644))

	profiles := config.NewReloadableProfiles(path, nil)
	require.NoError(t, profiles.Reload())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchProfiles(ctx, profiles, 10*time.Millisecond)

	require.NoError(t, os.WriteFile(path, []byte(`
agent_profiles:
  default:
    provider: codex
    model: gpt-5.4
`), 0o644))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := config.GetProfileOrDefault(profiles, "default"); got.Provider == "codex" {
			assert.Equal(t, "gpt-5.4", got.Model)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("profiles watcher did not reload updated file")
}
