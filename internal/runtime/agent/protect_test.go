package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/substrate/llm-core/contracts/runtimes"
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

func TestProtectEnvRecognized(t *testing.T) {
	for v, want := range map[string]bool{"": true, "1": true, "on": true, "yes": true, "TRUE": true, "0": true, "false": true, "OFF": true, " no ": true, "disable": false, "disabled": false, "2": false} {
		t.Setenv(ProtectEnv, v)
		assert.Equal(t, want, ProtectEnvRecognized(), "%s=%q", ProtectEnv, v)
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

		// The skip fails closed: only a positively identified read-only or
		// workspace-write sandbox leaves a launch unwrapped.
		{"--yolo, exec", config.AgentProfile{Provider: "codex", Args: []string{"--yolo"}}, "subprocess-per-turn", "danger-full-access", false},
		{"--yolo, app-server", config.AgentProfile{Provider: "codex", Args: []string{"--yolo"}}, "jsonrpc-stdio", "danger-full-access", false},
		{"--yolo after a sandbox flag", config.AgentProfile{Provider: "codex", Args: []string{"--sandbox", "read-only", "--yolo"}}, "jsonrpc-stdio", "danger-full-access", false},
		{"--sandbox=danger-full-access", config.AgentProfile{Provider: "codex", Args: []string{"--sandbox=danger-full-access"}}, "jsonrpc-stdio", "danger-full-access", false},
		{"-s danger-full-access", config.AgentProfile{Provider: "codex", Args: []string{"-s", "danger-full-access"}}, "subprocess-per-turn", "danger-full-access", false},
		{"an unknown sandbox mode", config.AgentProfile{Provider: "codex", Args: []string{"--sandbox", "banana"}}, "jsonrpc-stdio", "banana", false},
		{"-c default_permissions", config.AgentProfile{Provider: "codex", Args: []string{"-c", `default_permissions="full"`}}, "jsonrpc-stdio", "", false},
		{"--config=default_permissions", config.AgentProfile{Provider: "codex", Args: []string{`--config=default_permissions="full"`}}, "jsonrpc-stdio", "", false},
		{"-c sandbox_workspace_write writable_roots", config.AgentProfile{Provider: "codex", Args: []string{"-c", `sandbox_workspace_write.writable_roots=["/home/u/.config/torque"]`}}, "jsonrpc-stdio", "", false},
		{"-c sandbox_workspace_write network_access", config.AgentProfile{Provider: "codex", Args: []string{"-c", "sandbox_workspace_write.network_access=true"}}, "jsonrpc-stdio", "", false},
		{"--add-dir", config.AgentProfile{Provider: "codex", Args: []string{"--add-dir", "/home/u/.config/torque"}}, "jsonrpc-stdio", "", false},
		{"--add-dir=", config.AgentProfile{Provider: "codex", Args: []string{"--add-dir=/home/u/.torque"}}, "subprocess-per-turn", "", false},
		{"--profile", config.AgentProfile{Provider: "codex", Args: []string{"--profile", "full"}}, "jsonrpc-stdio", "", false},
		{"-p", config.AgentProfile{Provider: "codex", Args: []string{"-p", "full"}}, "subprocess-per-turn", "", false},
		{"an unknown sandbox-ish flag", config.AgentProfile{Provider: "codex", Args: []string{"--sandbox-policy", "none"}}, "jsonrpc-stdio", "", false},
		{"an unknown permission flag", config.AgentProfile{Provider: "codex", Args: []string{"--permission-profile=wide"}}, "subprocess-per-turn", "", false},
		{"an unknown bypass flag", config.AgentProfile{Provider: "codex", Args: []string{"--bypass-everything"}}, "jsonrpc-stdio", "", false},

		// CW-20261001-0256. Attached short options: clap accepts -s=<mode> and
		// -s<mode>, and Torque reads neither, so the launch is wrapped.
		{"-s=danger-full-access", config.AgentProfile{Provider: "codex", Args: []string{"-s=danger-full-access"}}, "subprocess-per-turn", "", false},
		{"-sdanger-full-access", config.AgentProfile{Provider: "codex", Args: []string{"-sdanger-full-access"}}, "subprocess-per-turn", "", false},
		{"-s=read-only is attached too", config.AgentProfile{Provider: "codex", Args: []string{"-s=read-only"}}, "subprocess-per-turn", "", false},
		{"an attached -c", config.AgentProfile{Provider: "codex", Args: []string{`-csandbox_mode="danger-full-access"`}}, "subprocess-per-turn", "", false},
		// A bypass flag is sticky: codex ranks it above every other selector,
		// wherever it appears and whatever else is selected.
		{"--yolo before --sandbox read-only", config.AgentProfile{Provider: "codex", Args: []string{"--yolo", "--sandbox", "read-only"}}, "subprocess-per-turn", "danger-full-access", false},
		{"--sandbox read-only before --yolo", config.AgentProfile{Provider: "codex", Args: []string{"--sandbox", "read-only", "--yolo"}}, "subprocess-per-turn", "danger-full-access", false},
		{"--yolo before -c sandbox_mode read-only", config.AgentProfile{Provider: "codex", Args: []string{"--yolo", "-c", `sandbox_mode="read-only"`}}, "subprocess-per-turn", "danger-full-access", false},
		{"-c sandbox_mode read-only before --yolo", config.AgentProfile{Provider: "codex", Args: []string{"-c", `sandbox_mode="read-only"`, "--yolo"}}, "jsonrpc-stdio", "danger-full-access", false},
		{"bypass flag after a confining -c", config.AgentProfile{Provider: "codex", Args: []string{"-c", `sandbox_mode="read-only"`, "--dangerously-bypass-approvals-and-sandbox"}}, "subprocess-per-turn", "danger-full-access", false},
		// Selectors that disagree wrap, in either order: codex ranks --sandbox
		// above -c, but Torque does not rely on that.
		{"--sandbox danger-full-access, then -c read-only", config.AgentProfile{Provider: "codex", Args: []string{"--sandbox", "danger-full-access", "-c", `sandbox_mode="read-only"`}}, "subprocess-per-turn", "", false},
		{"-c read-only, then --sandbox danger-full-access", config.AgentProfile{Provider: "codex", Args: []string{"-c", `sandbox_mode="read-only"`, "--sandbox", "danger-full-access"}}, "subprocess-per-turn", "", false},
		{"--sandbox read-only, then -c danger-full-access", config.AgentProfile{Provider: "codex", Args: []string{"--sandbox", "read-only", "-c", `sandbox_mode="danger-full-access"`}}, "subprocess-per-turn", "", false},
		{"--sandbox read-only with --sandbox workspace-write (codex refuses the repeat)", config.AgentProfile{Provider: "codex", Args: []string{"--sandbox", "read-only", "--sandbox", "workspace-write"}}, "subprocess-per-turn", "", false},
		{"--full-auto with --sandbox read-only", config.AgentProfile{Provider: "codex", Args: []string{"--full-auto", "--sandbox", "read-only"}}, "subprocess-per-turn", "", false},
		{"--full-auto with -c danger-full-access", config.AgentProfile{Provider: "codex", Args: []string{"--full-auto", "-c", `sandbox_mode="danger-full-access"`}}, "subprocess-per-turn", "", false},
		// A selector with no value is not understood.
		{"--sandbox as the last argument", config.AgentProfile{Provider: "codex", Args: []string{"--enable", "x", "--sandbox"}}, "subprocess-per-turn", "", false},
		{"-s as the last argument", config.AgentProfile{Provider: "codex", Args: []string{"-s"}}, "subprocess-per-turn", "", false},
		{"-c as the last argument", config.AgentProfile{Provider: "codex", Args: []string{"-c"}}, "jsonrpc-stdio", "", false},

		// What is positively understood still skips.
		{"--sandbox and -c agree", config.AgentProfile{Provider: "codex", Args: []string{"--sandbox", "read-only", "-c", `sandbox_mode="read-only"`}}, "subprocess-per-turn", "read-only", true},
		{"--sandbox and --full-auto agree", config.AgentProfile{Provider: "codex", Args: []string{"--sandbox", "workspace-write", "--full-auto"}}, "subprocess-per-turn", "workspace-write", true},
		{"within -c the last override wins (read-only last)", config.AgentProfile{Provider: "codex", Args: []string{"-c", `sandbox_mode="danger-full-access"`, "-c", `sandbox_mode="read-only"`}}, "subprocess-per-turn", "read-only", true},
		{"within -c the last override wins (danger-full-access last)", config.AgentProfile{Provider: "codex", Args: []string{"-c", `sandbox_mode="read-only"`, "-c", `sandbox_mode="danger-full-access"`}}, "subprocess-per-turn", "danger-full-access", false},
		{"--approve-for-me is workspace-write, as codex reports", config.AgentProfile{Provider: "codex", Args: []string{"--approve-for-me"}}, "subprocess-per-turn", "workspace-write", true},
		{"--sandbox read-only", config.AgentProfile{Provider: "codex", Args: []string{"--sandbox", "read-only"}}, "subprocess-per-turn", "read-only", true},
		{"-s workspace-write", config.AgentProfile{Provider: "codex", Args: []string{"-s", "workspace-write"}}, "jsonrpc-stdio", "workspace-write", true},
		{"--sandbox=workspace-write", config.AgentProfile{Provider: "codex", PermissionMode: "bypassPermissions", Args: []string{"--sandbox=workspace-write"}}, "jsonrpc-stdio", "workspace-write", true},
		{"-c sandbox_mode read-only", config.AgentProfile{Provider: "codex", Args: []string{"-c", `sandbox_mode="read-only"`}}, "jsonrpc-stdio", "read-only", true},
		{"--config=sandbox_mode", config.AgentProfile{Provider: "codex", Args: []string{`--config=sandbox_mode="workspace-write"`}}, "subprocess-per-turn", "workspace-write", true},
		{"--full-auto", config.AgentProfile{Provider: "codex", PermissionMode: "bypassPermissions", Args: []string{"--full-auto"}}, "jsonrpc-stdio", "workspace-write", true},
		{"args that touch no sandbox", config.AgentProfile{Provider: "codex", Args: []string{"--enable", "test_feature", "-c", `model="gpt-5.5"`, "-c", `approval_policy="never"`}}, "jsonrpc-stdio", "workspace-write", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, own := codexOwnSandbox(tc.profile, tc.kind)
			assert.Equal(t, tc.mode, mode)
			assert.Equal(t, tc.own, own)
		})
	}
}

// The app-server bypass mode comes from the function factory.go plants it
// with, not a literal, so the two cannot drift.
func TestCodexOwnSandbox_BypassModeIsThePlantedOne(t *testing.T) {
	planted, err := codexBypassSandboxMode(runtimes.ModeJSONRPCStdio)
	require.NoError(t, err)
	assert.Equal(t, "danger-full-access", planted)

	profile := config.AgentProfile{Provider: "codex", PermissionMode: "bypassPermissions"}
	mode, own := codexOwnSandbox(profile, "jsonrpc-stdio")
	assert.Equal(t, planted, mode, "an app-server launch under bypassPermissions runs as the adapter was told to plant")
	assert.False(t, own)

	// An explicit confining -c still overrides what was planted.
	profile.Args = []string{"-c", `sandbox_mode="read-only"`}
	mode, own = codexOwnSandbox(profile, "jsonrpc-stdio")
	assert.Equal(t, "read-only", mode)
	assert.True(t, own)
}

// A launch whose args the skip does not positively understand is wrapped in
// Torque's sandbox, not left to codex's own.
func TestLaunchProtectedPaths_CodexSandboxArgsFailClosed(t *testing.T) {
	deps := &Dependencies{ProtectedPaths: []string{"/p"}}
	for _, args := range [][]string{
		{"--yolo"},
		{"-c", "default_permissions=full"},
		{"--sandbox-policy", "none"},
		{"--add-dir", "/p"},
	} {
		got, err := launchProtectedPaths(deps, config.AgentProfile{Provider: "codex", Args: args}, "jsonrpc-stdio", "s")
		require.NoError(t, err)
		assert.Equal(t, []string{"/p"}, got, "args %q", args)
	}
	got, err := launchProtectedPaths(deps, config.AgentProfile{Provider: "codex", Args: []string{"--sandbox", "read-only"}}, "jsonrpc-stdio", "s")
	require.NoError(t, err)
	assert.Empty(t, got)
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
