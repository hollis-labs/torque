package bootstrap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// A Torque path pointed at /, the home directory or one of its ancestors is
// never protected: that would leave the agent nothing writable.
func TestSafeControlPlaneDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	under := filepath.Join(home, ".local", "share", "torque")
	got := safeControlPlaneDirs([]string{"/", home, filepath.Dir(home), under, ""})
	assert.Equal(t, []string{under}, got)
}

func TestProtectControlPlane(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Setenv("HOME", home)
	data := filepath.Join(home, ".local", "share", "torque")
	state := filepath.Join(home, ".local", "state", "torque")
	conf := filepath.Join(home, ".config", "torque")
	dotTorque := filepath.Join(home, ".torque")
	for _, d := range []string{filepath.Join(data, "workspaces", "default"), state, conf, filepath.Join(dotTorque, "workspaces")} {
		require.NoError(t, os.MkdirAll(d, 0o700))
	}
	cfg := &config.Config{
		DataDir:      data,
		DBPath:       filepath.Join(data, "workspaces", "default", "main.db"),
		StateDir:     state,
		ConfigDir:    conf,
		ProfilesPath: filepath.Join(conf, "profiles.yaml"),
		Concurrency:  config.ConcurrencyConfig{QueueDBPath: filepath.Join(state, "queue.db")},
	}

	t.Run("on by default", func(t *testing.T) {
		t.Setenv(agent.ProtectEnv, "")
		deps := &agent.Dependencies{}
		ProtectControlPlane(deps, cfg)
		assert.ElementsMatch(t, []string{data, state, conf, dotTorque}, deps.ProtectedPaths)
	})
	t.Run("kill switch", func(t *testing.T) {
		t.Setenv(agent.ProtectEnv, "0")
		deps := &agent.Dependencies{ProtectedPaths: []string{data}}
		ProtectControlPlane(deps, cfg)
		assert.Empty(t, deps.ProtectedPaths)
	})
}
