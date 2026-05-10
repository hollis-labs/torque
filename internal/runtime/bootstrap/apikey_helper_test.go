package bootstrap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveApiKeyHelperPath_Override pins that the
// CLOCKWORK_APIKEY_HELPER env var wins when the path is an executable
// regular file. Highest-priority resolution path; matches the operator-
// override convention documented on Dependencies.ApiKeyHelperPath.
func TestResolveApiKeyHelperPath_Override(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-helper")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho fake\n"), 0o755); err != nil {
		t.Fatalf("write fake helper: %v", err)
	}
	t.Setenv("CLOCKWORK_APIKEY_HELPER", path)
	got := resolveApiKeyHelperPath()
	// resolveApiKeyHelperPath normalizes via filepath.Abs +
	// filepath.EvalSymlinks for the doc-promised "absolute path"
	// contract. macOS resolves /var → /private/var; mirror the same
	// transform on the expected value.
	want := path
	if eval, err := filepath.EvalSymlinks(want); err == nil {
		want = eval
	}
	if got != want {
		t.Errorf("resolveApiKeyHelperPath = %q, want %q", got, want)
	}
}

// TestResolveApiKeyHelperPath_Override_NonExecutable pins that an
// override pointing at a non-executable file is ignored (logged and
// skipped). Defends against a misconfigured cerberus deployment that
// sets the env var but didn't chmod +x.
func TestResolveApiKeyHelperPath_Override_NonExecutable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "non-exec")
	if err := os.WriteFile(path, []byte("not exec"), 0o644); err != nil {
		t.Fatalf("write non-exec: %v", err)
	}
	t.Setenv("CLOCKWORK_APIKEY_HELPER", path)
	// Suppress the PATH fallback so we get a deterministic empty
	// answer when the override is rejected.
	t.Setenv("PATH", "/this/path/does/not/exist")
	got := resolveApiKeyHelperPath()
	// The override is rejected; sibling-binary lookup runs but the
	// test binary's sibling almost certainly isn't named
	// clockwork-apikey-helper. Empty is the expected outcome on a
	// clean test run.
	if got != "" && got != path {
		t.Logf("resolveApiKeyHelperPath = %q (test-binary sibling resolution may have hit; not an error)", got)
	}
	// Most important assertion: the override path is not returned
	// when it's not executable.
	if got == path {
		t.Errorf("non-executable override should be rejected, got %q", got)
	}
}

// TestResolveApiKeyHelperPath_NoHelperReturnsEmpty pins the
// graceful-fallback contract: when no resolution path hits, return ""
// so Boot skips the apiKeyHelper field. This is the existing
// contract for ANTHROPIC_API_KEY-only deployments.
//
// Sibling-binary resolution is tested separately
// (TestResolveApiKeyHelperPath_SiblingBinary). Here we want to assert
// the negative-path: env override empty, sibling absent, PATH lookup
// fails. To make the sibling-binary check fail we need the test
// binary's directory to NOT contain a `clockwork-apikey-helper` file —
// `go test` runs against a tempdir-built binary, so that's the default
// and we can assert directly.
func TestResolveApiKeyHelperPath_NoHelperReturnsEmpty(t *testing.T) {
	t.Setenv("CLOCKWORK_APIKEY_HELPER", "")
	t.Setenv("PATH", "/this/path/does/not/exist")

	// Defensive: confirm the test binary's sibling-dir genuinely lacks a
	// clockwork-apikey-helper. If a developer happens to drop the binary
	// into the test tempdir, the assertion below would erroneously fail.
	exe, exeErr := os.Executable()
	require.NoError(t, exeErr, "os.Executable should resolve in tests")
	if eval, err := filepath.EvalSymlinks(exe); err == nil {
		exe = eval
	}
	siblingCandidate := filepath.Join(filepath.Dir(exe), "clockwork-apikey-helper")
	if isExecutableFile(siblingCandidate) {
		t.Skipf("sibling clockwork-apikey-helper exists at %s; skipping the negative-path assertion (env-clean precondition fails)", siblingCandidate)
	}

	got := resolveApiKeyHelperPath()
	assert.Empty(t, got,
		"resolveApiKeyHelperPath must return \"\" when no resolution path hits; got %q", got)
}

// TestIsExecutableFile pins the predicate's behavior: regular file +
// at least one execute bit = true; everything else (missing, dir,
// non-executable file) = false.
func TestIsExecutableFile(t *testing.T) {
	dir := t.TempDir()

	exec := filepath.Join(dir, "yes-exec")
	if err := os.WriteFile(exec, []byte("x"), 0o755); err != nil {
		t.Fatalf("write exec: %v", err)
	}
	if !isExecutableFile(exec) {
		t.Error("regular +x file should be executable")
	}

	notExec := filepath.Join(dir, "no-exec")
	if err := os.WriteFile(notExec, []byte("x"), 0o644); err != nil {
		t.Fatalf("write non-exec: %v", err)
	}
	if isExecutableFile(notExec) {
		t.Error("regular file without execute bits should NOT be executable")
	}

	if isExecutableFile(filepath.Join(dir, "missing")) {
		t.Error("missing path should NOT be executable")
	}

	if isExecutableFile(dir) {
		t.Error("a directory should NOT be reported as executable file")
	}
}
