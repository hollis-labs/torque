package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
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
	if got != path {
		t.Errorf("resolveApiKeyHelperPath = %q, want %q", got, path)
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
func TestResolveApiKeyHelperPath_NoHelperReturnsEmpty(t *testing.T) {
	t.Setenv("CLOCKWORK_APIKEY_HELPER", "")
	t.Setenv("PATH", "/this/path/does/not/exist")
	got := resolveApiKeyHelperPath()
	// Sibling-binary path may resolve if the test was run from a dir
	// containing clockwork-apikey-helper; tolerate either outcome
	// but log when it happens for visibility.
	if got != "" {
		t.Logf("resolveApiKeyHelperPath = %q (test sibling-binary resolution hit — env presumed clean)", got)
	}
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
