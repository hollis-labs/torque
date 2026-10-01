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

// fakeHome points HOME at a fresh real directory.
func fakeHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Setenv("HOME", home)
	return home
}

// A candidate that is unset is skipped; one that is /, the home directory or
// an ancestor of it is never protected, since that would leave the agent
// nothing writable; a missing one is created 0700; a file is skipped.
func TestResolveControlPlane(t *testing.T) {
	home := fakeHome(t)
	existing := filepath.Join(home, ".local", "share", "torque")
	require.NoError(t, os.MkdirAll(existing, 0o700))
	missing := filepath.Join(home, ".torque")
	file := filepath.Join(home, "profiles.yaml")
	require.NoError(t, os.WriteFile(file, nil, 0o600))

	got, refusal := resolveControlPlane([]controlPlaneDir{
		{"root", "/"}, {"home", home}, {"home's parent", filepath.Dir(home)},
		{"unset", ""}, {"data", existing}, {"dot-torque", missing}, {"file", file},
	})
	require.Empty(t, refusal)
	assert.ElementsMatch(t, []string{existing, missing}, got)
	st, err := os.Stat(missing)
	require.NoError(t, err, "a missing control-plane directory is created before an agent can plant into it")
	assert.Equal(t, os.FileMode(0o700), st.Mode().Perm())
}

// A control-plane directory reached through a symlink in a directory the
// agent's uid can write refuses protection: the agent could re-point the
// link and redirect Torque to a directory of its own.
func TestResolveControlPlane_RepointableSymlinkRefuses(t *testing.T) {
	home := fakeHome(t)
	real := filepath.Join(home, "elsewhere", "config")
	require.NoError(t, os.MkdirAll(filepath.Join(real, "torque"), 0o700))
	link := filepath.Join(home, ".config")
	require.NoError(t, os.Symlink(real, link))

	got, refusal := resolveControlPlane([]controlPlaneDir{{"config dir", filepath.Join(link, "torque")}})
	assert.Empty(t, got)
	assert.Contains(t, refusal, link)
	assert.Contains(t, refusal, "re-point")
}

func TestProtectControlPlane(t *testing.T) {
	home := fakeHome(t)
	data := filepath.Join(home, ".local", "share", "torque")
	state := filepath.Join(home, ".local", "state", "torque")
	conf := filepath.Join(home, ".config", "torque")
	dotTorque := filepath.Join(home, ".torque")
	for _, d := range []string{filepath.Join(data, "workspaces", "default"), state, conf} {
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

	t.Run("on by default, creating a missing ~/.torque", func(t *testing.T) {
		t.Setenv(agent.ProtectEnv, "")
		deps := &agent.Dependencies{}
		ProtectControlPlane(deps, cfg)
		assert.ElementsMatch(t, []string{data, state, conf, dotTorque}, deps.ProtectedPaths)
		assert.Empty(t, deps.ProtectRefusal)
		assert.DirExists(t, dotTorque)
	})
	t.Run("kill switch", func(t *testing.T) {
		t.Setenv(agent.ProtectEnv, "0")
		deps := &agent.Dependencies{ProtectedPaths: []string{data}, ProtectRefusal: "stale"}
		ProtectControlPlane(deps, cfg)
		assert.Empty(t, deps.ProtectedPaths)
		assert.Empty(t, deps.ProtectRefusal)
	})
	t.Run("the overridden profiles file and template dirs are protected", func(t *testing.T) {
		t.Setenv(agent.ProtectEnv, "")
		profilesDir := filepath.Join(home, "ops", "profiles")
		agentTemplates := filepath.Join(home, "ops", "agent-templates")
		endAgentTemplates := filepath.Join(home, "ops", "end-agent-templates")
		t.Setenv("TORQUE_AGENT_TEMPLATE_DIR", agentTemplates)
		t.Setenv("TORQUE_END_AGENT_TEMPLATE_DIR", endAgentTemplates)
		profiles := config.NewReloadableProfiles(filepath.Join(profilesDir, "profiles.yaml"), config.ProfileMap{})
		deps := &agent.Dependencies{Profiles: profiles}
		ProtectControlPlane(deps, cfg)
		assert.Subset(t, deps.ProtectedPaths, []string{profilesDir, agentTemplates, endAgentTemplates})

		// The profiles reload is guarded by the directory's identity.
		require.NoError(t, profiles.ReloadAllowed())
		require.NoError(t, os.Rename(profilesDir, profilesDir+".moved"))
		require.NoError(t, os.MkdirAll(profilesDir, 0o700))
		assert.Error(t, profiles.ReloadAllowed(), "a replaced profiles directory is refused")
	})
	t.Run("a repointable symlink refuses every launch", func(t *testing.T) {
		t.Setenv(agent.ProtectEnv, "")
		t.Setenv("TORQUE_END_AGENT_TEMPLATE_DIR", filepath.Join(home, "templates-link"))
		require.NoError(t, os.Symlink(filepath.Join(home, ".torque"), filepath.Join(home, "templates-link")))
		deps := &agent.Dependencies{}
		ProtectControlPlane(deps, cfg)
		assert.Empty(t, deps.ProtectedPaths)
		assert.Contains(t, deps.ProtectRefusal, "templates-link")
		assert.Contains(t, deps.ProtectRefusal, agent.ProtectEnv+"=0")
	})
}

// With nothing to protect, protection refuses every launch rather than
// launching agents unprotected.
func TestProtectControlPlane_NothingToProtectRefuses(t *testing.T) {
	home := fakeHome(t)
	t.Setenv(agent.ProtectEnv, "")
	// ~/.torque is a file, so it cannot be protected; every other path is
	// the home directory, an ancestor of it, or unset.
	require.NoError(t, os.WriteFile(filepath.Join(home, ".torque"), nil, 0o600))
	deps := &agent.Dependencies{}
	ProtectControlPlane(deps, &config.Config{DataDir: home, ConfigDir: "/", StateDir: filepath.Dir(home)})
	assert.Empty(t, deps.ProtectedPaths)
	assert.Contains(t, deps.ProtectRefusal, "no Torque control-plane directory could be write-protected")
}

// While protection is on, the planted mux proxies no torque server: the
// `torque mcp` mux would spawn runs inside the agent's sandbox.
func TestMuxArgsWithoutServer(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
		ok   bool
	}{
		{"default", defaultMuxArgs, []string{"mcp", "--proxy", "--servers", "vanta,cerberus", "--token", "local-dev", "--scopes", "session.write,message.write"}, true},
		{"inline", []string{"mcp", "--proxy", "--servers=torque,vanta"}, []string{"mcp", "--proxy", "--servers=vanta"}, true},
		{"single dash", []string{"mcp", "-proxy", "-servers", "vanta, torque"}, []string{"mcp", "-proxy", "-servers", "vanta"}, true},
		{"no torque", []string{"mcp", "--proxy", "--servers", "vanta"}, []string{"mcp", "--proxy", "--servers", "vanta"}, true},
		{"no proxy serves mux's own tools", []string{"mcp"}, []string{"mcp"}, true},
		{"proxy without servers proxies every server", []string{"mcp", "--proxy"}, nil, false},
		{"only torque would leave every server", []string{"mcp", "--proxy", "--servers", "torque"}, nil, false},
		{"dangling servers flag", []string{"mcp", "--proxy", "--servers"}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := muxArgsWithoutServer(tc.in, "torque")
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
	assert.Equal(t, "vanta,torque,cerberus", defaultMuxArgs[3], "the package default is untouched")

	t.Run("protection drops torque from the planted mux", func(t *testing.T) {
		deps := &agent.Dependencies{MuxCommand: "/bin/mux", MuxArgs: append([]string(nil), defaultMuxArgs...)}
		muxWithoutTorque(deps)
		assert.True(t, deps.MuxOmitsTorque)
		assert.Equal(t, "/bin/mux", deps.MuxCommand)
		assert.Equal(t, "vanta,cerberus", deps.MuxArgs[3])
	})
	t.Run("mux that cannot be narrowed is not planted", func(t *testing.T) {
		deps := &agent.Dependencies{MuxCommand: "/bin/mux", MuxArgs: []string{"mcp", "--proxy"}}
		muxWithoutTorque(deps)
		assert.True(t, deps.MuxOmitsTorque)
		assert.Empty(t, deps.MuxCommand)
		assert.Empty(t, deps.MuxArgs)
	})
}
