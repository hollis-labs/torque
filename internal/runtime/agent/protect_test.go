package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
)

func TestProtectionEnabled(t *testing.T) {
	for v, want := range map[string]bool{"": true, "1": true, "on": true, "0": false, "false": false, "OFF": false, "no": false} {
		t.Setenv(ProtectEnv, v)
		assert.Equal(t, want, ProtectionEnabled(), "%s=%q", ProtectEnv, v)
	}
}

// Only existing directories, by their real path, deduplicated, with a
// directory inside another kept one dropped: go-sandbox refuses files,
// missing paths and symlinked entries.
func TestResolveProtectedPaths(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	data := filepath.Join(root, "data")
	inner := filepath.Join(data, "workspaces")
	state := filepath.Join(root, "state")
	require.NoError(t, os.MkdirAll(inner, 0o700))
	require.NoError(t, os.MkdirAll(state, 0o700))
	link := filepath.Join(root, "state-link")
	require.NoError(t, os.Symlink(state, link))
	file := filepath.Join(root, "main.db")
	require.NoError(t, os.WriteFile(file, nil, 0o600))

	got := ResolveProtectedPaths([]string{inner, data, link, state, file, filepath.Join(root, "missing"), "relative/dir", ""})
	assert.Equal(t, []string{data, state}, got)
	assert.Empty(t, ResolveProtectedPaths(nil))

	// A sibling that sorts between a directory and its child ("/a-b"
	// between "/a" and "/a/c") does not let the child through.
	a, ab, ac := filepath.Join(root, "a"), filepath.Join(root, "a-b"), filepath.Join(root, "a", "c")
	for _, d := range []string{ab, ac} {
		require.NoError(t, os.MkdirAll(d, 0o700))
	}
	assert.Equal(t, []string{a, ab}, ResolveProtectedPaths([]string{ac, ab, a}))
}

// Codex runs the agent's commands in its own OS sandbox unless the launch
// plants danger-full-access (an app-server launch under bypassPermissions)
// or the profile's args turn it off; a sandbox cannot nest inside Torque's.
func TestCodexOwnSandbox(t *testing.T) {
	cases := []struct {
		name    string
		profile config.AgentProfile
		kind    RuntimeKind
		mode    string
		own     bool
	}{
		{"app-server, unset posture", config.AgentProfile{Provider: "codex"}, "jsonrpc-stdio", "workspace-write", true},
		{"app-server, acceptEdits", config.AgentProfile{Provider: "codex", PermissionMode: "acceptEdits"}, "jsonrpc-stdio", "workspace-write", true},
		{"app-server, bypassPermissions", config.AgentProfile{Provider: "codex", PermissionMode: "bypassPermissions"}, "jsonrpc-stdio", "danger-full-access", false},
		{"exec, bypassPermissions keeps the planted sandbox", config.AgentProfile{Provider: "codex", PermissionMode: "bypassPermissions"}, "subprocess-per-turn", "workspace-write", true},
		{"exec, args bypass the sandbox", config.AgentProfile{Provider: "codex", Args: []string{"--dangerously-bypass-approvals-and-sandbox"}}, "subprocess-per-turn", "danger-full-access", false},
		{"args -c sandbox_mode", config.AgentProfile{Provider: "codex", Args: []string{"-c", `sandbox_mode="danger-full-access"`}}, "jsonrpc-stdio", "danger-full-access", false},
		{"args --sandbox", config.AgentProfile{Provider: "codex", PermissionMode: "bypassPermissions", Args: []string{"--sandbox", "read-only"}}, "jsonrpc-stdio", "read-only", true},
		{"claude", config.AgentProfile{Provider: "claude-code"}, "streaming-stdio", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, own := codexOwnSandbox(tc.profile, tc.kind)
			assert.Equal(t, tc.mode, mode)
			assert.Equal(t, tc.own, own)
		})
	}
}

func TestLaunchProtectedPaths(t *testing.T) {
	claude := config.AgentProfile{Provider: "claude-code"}
	got, err := launchProtectedPaths(&Dependencies{ProtectedPaths: []string{"/p"}}, claude, "streaming-stdio", "s")
	require.NoError(t, err)
	assert.Equal(t, []string{"/p"}, got)

	got, err = launchProtectedPaths(&Dependencies{ProtectedPaths: []string{"/p"}}, config.AgentProfile{Provider: "codex"}, "jsonrpc-stdio", "s")
	require.NoError(t, err)
	assert.Empty(t, got, "codex in its own sandbox is not wrapped in Torque's")

	got, err = launchProtectedPaths(&Dependencies{ProtectedPaths: []string{"/p"}}, config.AgentProfile{Provider: "codex", PermissionMode: "bypassPermissions"}, "jsonrpc-stdio", "s")
	require.NoError(t, err)
	assert.Equal(t, []string{"/p"}, got, "codex without its sandbox is")

	_, err = launchProtectedPaths(&Dependencies{ProtectRefusal: "nothing to protect"}, claude, "streaming-stdio", "s")
	require.EqualError(t, err, "nothing to protect", "protection that could not be set up refuses the launch")
}
