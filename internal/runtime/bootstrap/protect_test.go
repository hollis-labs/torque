package bootstrap

import (
	"net"
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

// A candidate that is unset is skipped, a file is skipped, and a missing one
// is created 0700.
func TestResolveControlPlane(t *testing.T) {
	home := fakeHome(t)
	existing := filepath.Join(home, ".local", "share", "torque")
	require.NoError(t, os.MkdirAll(existing, 0o700))
	missing := filepath.Join(home, ".torque")
	file := filepath.Join(home, "profiles.yaml")
	require.NoError(t, os.WriteFile(file, nil, 0o600))

	got, refusal := resolveControlPlane([]controlPlaneDir{
		{"unset", ""}, {"data", existing}, {"dot-torque", missing}, {"file", file},
	})
	require.Empty(t, refusal)
	assert.ElementsMatch(t, []string{existing, missing}, got)
	st, err := os.Stat(missing)
	require.NoError(t, err, "a missing control-plane directory is created before an agent can plant into it")
	assert.Equal(t, os.FileMode(0o700), st.Mode().Perm())
}

// A candidate that is a shared directory, or contains one, refuses protection
// rather than being skipped: protecting it would make it read-only for every
// agent, and skipping it would leave the state in it unprotected.
func TestResolveControlPlane_SharedDirectoryRefuses(t *testing.T) {
	home := fakeHome(t)
	for _, tc := range []struct{ name, dir string }{
		{"the root", "/"},
		{"the home directory", home},
		{"an ancestor of the home directory", filepath.Dir(home)},
		{"/tmp", "/tmp"},
		{"/var/tmp", "/var/tmp"},
		{"/var, which contains /var/tmp", "/var"},
		{"the temp dir", os.TempDir()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, refusal := resolveControlPlane([]controlPlaneDir{{"main database dir", tc.dir}})
			assert.Empty(t, got)
			assert.Contains(t, refusal, "main database dir")
			assert.Contains(t, refusal, "shared directory")
		})
	}

	t.Run("a directory inside the temp dir is fine", func(t *testing.T) {
		inside := filepath.Join(os.TempDir(), "torque-protect-test-"+filepath.Base(home))
		t.Cleanup(func() { _ = os.RemoveAll(inside) })
		got, refusal := resolveControlPlane([]controlPlaneDir{{"data dir", inside}})
		require.Empty(t, refusal)
		assert.Len(t, got, 1)
	})
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
	t.Run("mux loses its torque server", func(t *testing.T) {
		t.Setenv(agent.ProtectEnv, "")
		deps := &agent.Dependencies{MuxCommand: "/bin/mux", MuxArgs: append([]string(nil), defaultMuxArgs...)}
		ProtectControlPlane(deps, cfg)
		assert.True(t, deps.MuxOmitsTorque, "the kickoff is told mux serves no torque tools")
		assert.Equal(t, "/bin/mux", deps.MuxCommand)
		assert.Equal(t, []string{"mcp", "--proxy", "--servers", "vanta,cerberus", "--token", "local-dev", "--scopes", "session.write,message.write"}, deps.MuxArgs)
		assert.Equal(t, "vanta,torque,cerberus", defaultMuxArgs[3], "the package default is untouched")
	})
	t.Run("tokenless daemon retains torque through remote relay", func(t *testing.T) {
		t.Setenv(agent.ProtectEnv, "")
		deps := &agent.Dependencies{MuxCommand: "/bin/renamed-proxy", MuxArgs: append([]string(nil), defaultMuxArgs...)}
		ProtectControlPlaneWithRemote(deps, cfg, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8990}, false)
		assert.NotEmpty(t, deps.ProtectedPaths)
		assert.Empty(t, deps.ProtectRefusal)
		assert.False(t, deps.MuxOmitsTorque)
		assert.Equal(t, "/bin/renamed-proxy", deps.MuxCommand)
		assert.Equal(t, defaultMuxArgs, deps.MuxArgs)
		assert.Contains(t, deps.MuxEnv, "TORQUE_MCP_REMOTE=http://127.0.0.1:8990/mcp")
	})
	t.Run("token daemon still omits torque", func(t *testing.T) {
		t.Setenv(agent.ProtectEnv, "")
		deps := &agent.Dependencies{MuxCommand: "/bin/mux", MuxArgs: append([]string(nil), defaultMuxArgs...)}
		ProtectControlPlaneWithRemote(deps, cfg, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8990}, true)
		assert.True(t, deps.MuxOmitsTorque)
		assert.Empty(t, deps.MuxEnv)
	})

	t.Run("a refused protection leaves mux alone", func(t *testing.T) {
		t.Setenv(agent.ProtectEnv, "")
		bad := *cfg
		bad.DBPath = "/tmp/x.db"
		deps := &agent.Dependencies{MuxCommand: "/bin/mux", MuxArgs: append([]string(nil), defaultMuxArgs...)}
		ProtectControlPlane(deps, &bad)
		require.NotEmpty(t, deps.ProtectRefusal)
		assert.False(t, deps.MuxOmitsTorque)
		assert.Equal(t, defaultMuxArgs, deps.MuxArgs)
	})
	t.Run("kill switch", func(t *testing.T) {
		t.Setenv(agent.ProtectEnv, "0")
		deps := &agent.Dependencies{ProtectedPaths: []string{data}, ProtectRefusal: "stale", MuxCommand: "/bin/mux", MuxArgs: append([]string(nil), defaultMuxArgs...)}
		ProtectControlPlane(deps, cfg)
		assert.Empty(t, deps.ProtectedPaths)
		assert.Empty(t, deps.ProtectRefusal)
		assert.False(t, deps.MuxOmitsTorque, "unprotected agents keep mux's torque server")
		assert.Equal(t, defaultMuxArgs, deps.MuxArgs)
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
	// ~/.torque is a file, so it cannot be protected, and every other
	// candidate is unset.
	require.NoError(t, os.WriteFile(filepath.Join(home, ".torque"), nil, 0o600))
	deps := &agent.Dependencies{}
	ProtectControlPlane(deps, &config.Config{})
	assert.Empty(t, deps.ProtectedPaths)
	assert.Contains(t, deps.ProtectRefusal, "no Torque control-plane directory could be write-protected")
	assert.Contains(t, deps.ProtectRefusal, agent.ProtectEnv+"=0")
}

// TORQUE_DB_PATH=/tmp/x.db would make /tmp read-only to every agent: protection
// refuses every launch and says so, naming the kill switch, for a DB, queue
// or override directory alike.
func TestProtectControlPlane_SharedDatabaseDirectoryRefuses(t *testing.T) {
	home := fakeHome(t)
	t.Setenv(agent.ProtectEnv, "")
	conf := filepath.Join(home, ".config", "torque")
	require.NoError(t, os.MkdirAll(conf, 0o700))
	for _, tc := range []struct {
		name string
		cfg  config.Config
		want string
	}{
		{"main DB in /tmp", config.Config{ConfigDir: conf, DBPath: "/tmp/x.db"}, "main database dir"},
		{"main DB directly in $HOME", config.Config{ConfigDir: conf, DBPath: filepath.Join(home, "main.db")}, "main database dir"},
		{"queue DB in /var/tmp", config.Config{ConfigDir: conf, Concurrency: config.ConcurrencyConfig{QueueDBPath: "/var/tmp/queue.db"}}, "queue database dir"},
		{"data dir is $HOME", config.Config{ConfigDir: conf, DataDir: home}, "data dir"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			deps := &agent.Dependencies{}
			ProtectControlPlane(deps, &cfg)
			assert.Empty(t, deps.ProtectedPaths)
			assert.Contains(t, deps.ProtectRefusal, tc.want)
			assert.Contains(t, deps.ProtectRefusal, "shared directory")
			assert.Contains(t, deps.ProtectRefusal, agent.ProtectEnv+"=0")
		})
	}
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
