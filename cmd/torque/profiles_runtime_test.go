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

func TestResolveProfilesPathUsesConfigProfilesPath(t *testing.T) {
	want := filepath.Join(t.TempDir(), "torque", "profiles.yaml")
	cfg := &config.Config{ProfilesPath: want}
	t.Setenv("TORQUE_PROFILES_PATH", "")

	path, err := resolveProfilesPath(cfg)
	require.NoError(t, err)
	assert.Equal(t, want, path)
}

func TestResolveProfilesPathEnvOverrideWins(t *testing.T) {
	override := filepath.Join(t.TempDir(), "explicit-profiles.yaml")
	cfg := &config.Config{ProfilesPath: filepath.Join(t.TempDir(), "ignored.yaml")}
	t.Setenv("TORQUE_PROFILES_PATH", override)

	path, err := resolveProfilesPath(cfg)
	require.NoError(t, err)
	assert.Equal(t, override, path)
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
