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
// path: TORQUE_MUX_COMMAND env var overrides every other resolver.
// Mirrors the operator-override convention from
// resolveApiKeyHelperPath. The path is normalized to absolute +
// symlink-resolved.
func TestResolveMuxCommand_Override(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-mux")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho fake\n"), 0o755))

	t.Setenv("TORQUE_MUX_COMMAND", path)

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

	t.Setenv("TORQUE_MUX_COMMAND", path)
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
	t.Setenv("TORQUE_MUX_COMMAND", "")
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
// TORQUE_MUX_ARGS override is set. The shape mirrors the user's
// ~/.claude.json `mcpServers.mux` entry verbatim — agents that work
// interactively see the same aggregator surface when dispatched as a
// torque task.
func TestResolveMuxConfig_DefaultArgs(t *testing.T) {
	// Need a resolvable Mux for the args branch to fire; simulate via
	// an override pointing at a fake executable.
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-mux")
	require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755))

	t.Setenv("TORQUE_MUX_COMMAND", fake)
	t.Setenv("TORQUE_MUX_ARGS", "")

	got := resolveMuxConfig()
	assert.NotEmpty(t, got.Command, "Mux command should resolve via override")
	assert.True(t, reflect.DeepEqual(got.Args, defaultMuxArgs),
		"args should fall back to defaultMuxArgs; got %v", got.Args)
}

// TestResolveMuxConfig_ArgsOverride pins the TORQUE_MUX_ARGS env
// override path. Whitespace-split with no quoting; operators with
// values containing spaces should use the future
// TORQUE_MUX_ARGS_JSON path (filed as a follow-up).
func TestResolveMuxConfig_ArgsOverride(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-mux")
	require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755))

	t.Setenv("TORQUE_MUX_COMMAND", fake)
	t.Setenv("TORQUE_MUX_ARGS", "mcp --proxy --servers vanta --token override-token")

	got := resolveMuxConfig()
	want := []string{"mcp", "--proxy", "--servers", "vanta", "--token", "override-token"}
	assert.True(t, reflect.DeepEqual(got.Args, want),
		"args should reflect override; got %v want %v", got.Args, want)
}

// TestResolveMuxConfig_NoMuxReturnsZero pins that when no Mux command
// resolves, the entire muxResolution is the zero value — the bootdir
// plant then emits no `mux` MCP entry (back-compat fence).
func TestResolveMuxConfig_NoMuxReturnsZero(t *testing.T) {
	t.Setenv("TORQUE_MUX_COMMAND", "")
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

// TestResolveMuxConfig_DefaultArgsAreDefensiveCopy pins that the args
// returned in the default branch do NOT alias defaultMuxArgs's backing
// array. Without the defensive copy a downstream consumer could
// `append(args, ...)` and silently mutate the package-level default for
// every subsequent task.
func TestResolveMuxConfig_DefaultArgsAreDefensiveCopy(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-mux")
	require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755))

	t.Setenv("TORQUE_MUX_COMMAND", fake)
	t.Setenv("TORQUE_MUX_ARGS", "")

	got := resolveMuxConfig()
	require.NotEmpty(t, got.Args)

	// Snapshot the default args so we can assert they're untouched after
	// a downstream-style mutation on the returned slice.
	want := append([]string(nil), defaultMuxArgs...)

	// Mutate the returned slice in place — this would mutate
	// defaultMuxArgs's backing array if the slice were a direct reference.
	got.Args[0] = "MUTATED"

	assert.Equal(t, want, defaultMuxArgs,
		"defaultMuxArgs must not be mutated by changes to resolveMuxConfig's returned slice (defensive copy is load-bearing)")
}

// TestResolveMuxConfig_WhitespaceOnlyOverrideFallsBackToDefaults pins
// the whitespace-only override behavior: if TORQUE_MUX_ARGS parses
// to zero tokens, fall back to defaultMuxArgs rather than emit a bare
// `mux` invocation. Empty-after-Fields is far more likely a
// misconfiguration than a deliberate "run mux bare" request.
func TestResolveMuxConfig_WhitespaceOnlyOverrideFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-mux")
	require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755))

	t.Setenv("TORQUE_MUX_COMMAND", fake)
	t.Setenv("TORQUE_MUX_ARGS", "   \t  \n  ")

	got := resolveMuxConfig()
	assert.True(t, reflect.DeepEqual(got.Args, defaultMuxArgs),
		"whitespace-only TORQUE_MUX_ARGS should fall back to defaultMuxArgs, not emit zero args; got %v", got.Args)
}
