package executorcli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveWorkingDir is table-driven per the CW-20260418-0014 spec. Each
// row captures the policy decision the resolver enforces — expansion rules
// AND the explicit rejects (relative paths, ~user/, undefined env vars).
func TestResolveWorkingDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("working_dir resolver is macOS/Linux-first; Windows has separate path semantics")
	}

	// Use fixed HOME so expectations are stable across developer laptops / CI.
	fakeHome := "/tmp/clockwork-test-home"
	t.Setenv("HOME", fakeHome)
	// A concrete non-HOME var we can reference from test rows.
	t.Setenv("CLOCKWORK_TEST_ROOT", "/srv/projects")

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string // substring match; empty = expect success
	}{
		{
			name:  "empty preserved",
			input: "",
			want:  "",
		},
		{
			name:  "tilde slash path",
			input: "~/foo",
			want:  filepath.Join(fakeHome, "foo"),
		},
		{
			name:  "bare tilde slash",
			input: "~/",
			want:  fakeHome,
		},
		{
			name:  "bare tilde",
			input: "~",
			want:  fakeHome,
		},
		{
			name:    "tilde user form rejected",
			input:   "~bob/foo",
			wantErr: "~user/ form is not supported",
		},
		{
			name:  "bare dollar HOME",
			input: "$HOME/foo",
			want:  filepath.Join(fakeHome, "foo"),
		},
		{
			name:  "braced HOME",
			input: "${HOME}/foo",
			want:  filepath.Join(fakeHome, "foo"),
		},
		{
			name:  "bare custom env var",
			input: "$CLOCKWORK_TEST_ROOT/nanite",
			want:  "/srv/projects/nanite",
		},
		{
			name:  "braced custom env var with suffix",
			input: "${CLOCKWORK_TEST_ROOT}abc",
			want:  "/srv/projectsabc",
		},
		{
			name:  "absolute path passthrough",
			input: "/abs/path",
			want:  "/abs/path",
		},
		{
			name:  "absolute path cleaned trailing slash",
			input: "/abs/path/",
			want:  "/abs/path",
		},
		{
			name:  "absolute path cleaned redundant dot",
			input: "/abs/./path",
			want:  "/abs/path",
		},
		{
			name:    "relative path rejected",
			input:   "rel/path",
			wantErr: "is relative",
		},
		{
			name:    "single dot rejected",
			input:   ".",
			wantErr: "is relative",
		},
		{
			name:    "undefined env var rejected",
			input:   "$UNDEFINED_VAR_XYZ/x",
			wantErr: "is not set",
		},
		{
			name:    "undefined braced env var rejected",
			input:   "${UNDEFINED_VAR_XYZ}/x",
			wantErr: "is not set",
		},
		{
			name:    "unterminated braced env rejected",
			input:   "${HOME/foo",
			wantErr: "unterminated ${",
		},
		{
			name:    "empty braced env rejected",
			input:   "${}/foo",
			wantErr: "empty ${}",
		},
		{
			name:  "dollar with no valid name is literal",
			input: "/tmp/$",
			want:  "/tmp/$",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveWorkingDir(tt.input)
			if tt.wantErr != "" {
				require.Error(t, err, "input=%q", tt.input)
				assert.Contains(t, err.Error(), tt.wantErr, "error should mention %q", tt.wantErr)
				return
			}
			require.NoError(t, err, "input=%q", tt.input)
			assert.Equal(t, tt.want, got, "input=%q", tt.input)
		})
	}
}

// TestResolveWorkingDir_DoesNotFollowSymlinks is a stronger assertion than the
// table test: given a symlink whose target exists, resolution MUST return the
// symlink path verbatim (after Clean+Abs), NOT the target. Symlink resolution
// would change the observable cwd of the spawned process and surprise users
// whose project layout relies on symlinks (e.g. clockwork-manifold, which is
// itself a symlink on the maintainer's machine).
func TestResolveWorkingDir_DoesNotFollowSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test requires unix semantics")
	}
	tmp := t.TempDir()

	targetDir := filepath.Join(tmp, "real")
	require.NoError(t, os.MkdirAll(targetDir, 0o755))

	linkDir := filepath.Join(tmp, "link")
	require.NoError(t, os.Symlink(targetDir, linkDir))

	got, err := resolveWorkingDir(linkDir)
	require.NoError(t, err)
	assert.Equal(t, linkDir, got, "resolver must not collapse symlinks via EvalSymlinks")
	// Sanity: the symlink target differs, confirming we actually had a symlink.
	assert.NotEqual(t, targetDir, got)
}

// TestResolveWorkingDir_TildeProducesAbsolute covers the case where `~/foo`
// expands against the executor's real HOME. We don't set HOME here — the
// test confirms the returned path is absolute and ends with the expected
// subcomponent, so we don't need a pinned home value.
func TestResolveWorkingDir_TildeProducesAbsolute(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("HOME semantics differ on Windows")
	}
	got, err := resolveWorkingDir("~/clockwork-sentinel")
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(got, "/clockwork-sentinel"), "path should end with the sub-component: got %s", got)
	assert.True(t, filepath.IsAbs(got), "resolved tilde path must be absolute")
}
