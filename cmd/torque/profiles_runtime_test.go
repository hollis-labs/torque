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

// While Torque write-protects the profiles directory, the watcher reloads
// only while that path is still the directory recorded at startup
// (CW-20261001-0141). One moved aside and recreated with other profiles is
// refused, and the last good profiles stay live.
func TestWatchProfilesRefusesReplacedDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "torque")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	path := filepath.Join(dir, "profiles.yaml")
	write := func(provider string) {
		require.NoError(t, os.WriteFile(path, []byte("agent_profiles:\n  default:\n    provider: "+provider+"\n"), 0o600))
	}
	write("claude")
	profiles := config.NewReloadableProfiles(path, nil)
	id, err := config.RecordDirIdentity(dir)
	require.NoError(t, err)
	profiles.SetReloadGuard(id.Check)
	require.NoError(t, profiles.Reload())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchProfiles(ctx, profiles, 10*time.Millisecond)

	// Control: an edit in place reloads.
	write("codex")
	require.Eventually(t, func() bool { return config.GetProfileOrDefault(profiles, "default").Provider == "codex" }, 2*time.Second, 10*time.Millisecond)

	require.NoError(t, os.Rename(dir, dir+".moved"))
	require.NoError(t, os.MkdirAll(dir, 0o700))
	write("opencode")
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, "codex", config.GetProfileOrDefault(profiles, "default").Provider, "a replaced profiles directory is not reloaded")
	assert.Error(t, profiles.Reload(), "an explicit reload is refused too")
	assert.Equal(t, "codex", config.GetProfileOrDefault(profiles, "default").Provider)

	// With the protected directory back in place the watcher resumes, and picks
	// up what changed in the meantime.
	require.NoError(t, os.RemoveAll(dir))
	require.NoError(t, os.Rename(dir+".moved", dir))
	write("agy")
	require.Eventually(t, func() bool { return config.GetProfileOrDefault(profiles, "default").Provider == "agy" }, 2*time.Second, 10*time.Millisecond, "reloading resumes once the protected directory is back")
}
