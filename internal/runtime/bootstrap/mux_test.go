package bootstrap

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveMuxCommand_Override pins the highest-priority resolution
// path: CLOCKWORK_MUX_COMMAND env var overrides every other resolver.
// Mirrors the operator-override convention from
// resolveApiKeyHelperPath. The path is normalized to absolute +
// symlink-resolved.
func TestResolveMuxCommand_Override(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-mux")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho fake\n"), 0o755))

	t.Setenv("CLOCKWORK_MUX_COMMAND", path)

	got := resolveMuxCommand()

	// resolveMuxCommand applies filepath.Abs + filepath.EvalSymlinks
	// for the "absolute path" contract. macOS resolves /var → /private/var;
	// mirror the same transform on the expected value.
	want := path
	if eval, err := filepath.EvalSymlinks(want); err == nil {
		want = eval
	}
	assert.Equal(t, want, got)
}

// TestResolveMuxCommand_Override_NonExecutable pins that an override
// pointing at a non-executable file is ignored. Defends against a
// misconfigured deployment where the env var is set but the binary
// wasn't chmod +x.
func TestResolveMuxCommand_Override_NonExecutable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "non-exec")
	require.NoError(t, os.WriteFile(path, []byte("not exec"), 0o644))

	t.Setenv("CLOCKWORK_MUX_COMMAND", path)
	// Suppress sibling-binary + PATH fallback so we get a deterministic
	// empty answer when the override is rejected.
	t.Setenv("PATH", "/this/path/does/not/exist")

	got := resolveMuxCommand()

	// The override path itself must not be returned when it's not
	// executable. Sibling-binary lookup may still hit (test binary's
	// sibling could rarely be named "mux"), so we assert the negative
	// rather than equality.
	assert.NotEqual(t, path, got, "non-executable override should be rejected")
}

// TestResolveMuxCommand_NoHelperReturnsEmpty pins the graceful-fallback
// contract: when no resolution path hits, return "" so the bootdir plant
// emits no `mux` MCP entry (CW-20260510-0110 back-compat fence).
func TestResolveMuxCommand_NoHelperReturnsEmpty(t *testing.T) {
	t.Setenv("CLOCKWORK_MUX_COMMAND", "")
	t.Setenv("PATH", "/this/path/does/not/exist")

	// Defensive: confirm the test binary's sibling dir genuinely lacks
	// a `mux` file (if a developer dropped one in, the assertion below
	// would erroneously fail). Skip on conflict — that's an env
	// precondition, not a test failure.
	exe, exeErr := os.Executable()
	require.NoError(t, exeErr, "os.Executable should resolve in tests")
	if eval, err := filepath.EvalSymlinks(exe); err == nil {
		exe = eval
	}
	siblingCandidate := filepath.Join(filepath.Dir(exe), "mux")
	if isExecutableFile(siblingCandidate) {
		t.Skipf("sibling mux exists at %s; skipping the negative-path assertion (env-clean precondition fails)", siblingCandidate)
	}

	got := resolveMuxCommand()
	assert.Empty(t, got, "resolveMuxCommand must return \"\" when no resolution path hits; got %q", got)
}

// TestResolveMuxConfig_DefaultArgs pins that resolveMuxConfig falls back
// to the canonical interactive-shell shape (defaultMuxArgs) when no
// CLOCKWORK_MUX_ARGS override is set. The shape mirrors the user's
// ~/.claude.json `mcpServers.mux` entry verbatim — agents that work
// interactively see the same aggregator surface when dispatched as a
// clockwork task.
func TestResolveMuxConfig_DefaultArgs(t *testing.T) {
	// Need a resolvable Mux for the args branch to fire; simulate via
	// an override pointing at a fake executable.
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-mux")
	require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755))

	t.Setenv("CLOCKWORK_MUX_COMMAND", fake)
	t.Setenv("CLOCKWORK_MUX_ARGS", "")

	got := resolveMuxConfig()
	assert.NotEmpty(t, got.Command, "Mux command should resolve via override")
	assert.True(t, reflect.DeepEqual(got.Args, defaultMuxArgs),
		"args should fall back to defaultMuxArgs; got %v", got.Args)
}

// TestResolveMuxConfig_ArgsOverride pins the CLOCKWORK_MUX_ARGS env
// override path. Whitespace-split with no quoting; operators with
// values containing spaces should use the future
// CLOCKWORK_MUX_ARGS_JSON path (filed as a follow-up).
func TestResolveMuxConfig_ArgsOverride(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-mux")
	require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755))

	t.Setenv("CLOCKWORK_MUX_COMMAND", fake)
	t.Setenv("CLOCKWORK_MUX_ARGS", "mcp --proxy --servers vanta --token override-token")

	got := resolveMuxConfig()
	want := []string{"mcp", "--proxy", "--servers", "vanta", "--token", "override-token"}
	assert.True(t, reflect.DeepEqual(got.Args, want),
		"args should reflect override; got %v want %v", got.Args, want)
}

// TestResolveMuxConfig_NoMuxReturnsZero pins that when no Mux command
// resolves, the entire muxResolution is the zero value — the bootdir
// plant then emits no `mux` MCP entry (back-compat fence).
func TestResolveMuxConfig_NoMuxReturnsZero(t *testing.T) {
	t.Setenv("CLOCKWORK_MUX_COMMAND", "")
	t.Setenv("PATH", "/this/path/does/not/exist")

	exe, exeErr := os.Executable()
	require.NoError(t, exeErr)
	if eval, err := filepath.EvalSymlinks(exe); err == nil {
		exe = eval
	}
	if isExecutableFile(filepath.Join(filepath.Dir(exe), "mux")) {
		t.Skip("sibling mux exists; skipping negative-path assertion")
	}

	got := resolveMuxConfig()
	assert.Empty(t, got.Command)
	assert.Empty(t, got.Args)
	assert.Empty(t, got.Env)
}
