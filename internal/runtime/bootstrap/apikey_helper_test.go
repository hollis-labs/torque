package bootstrap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestResolveApiKeyHelperPath_Override pins that the
// TORQUE_APIKEY_HELPER env var wins when the path is an executable
// regular file. Highest-priority resolution path; matches the operator-
// override convention documented on Dependencies.ApiKeyHelperPath.
func TestResolveApiKeyHelperPath_Override(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-helper")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho fake\n"), 0o755); err != nil {
		t.Fatalf("write fake helper: %v", err)
	}
	t.Setenv("TORQUE_APIKEY_HELPER", path)
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
// override pointing at a non-executable file is rejected (logged and
// dropped). Defends against a misconfigured deployment that sets the
// env var but didn't chmod +x. Post 2026-05-26 the function has no
// fallback path, so the rejection produces an empty string.
func TestResolveApiKeyHelperPath_Override_NonExecutable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "non-exec")
	if err := os.WriteFile(path, []byte("not exec"), 0o644); err != nil {
		t.Fatalf("write non-exec: %v", err)
	}
	t.Setenv("TORQUE_APIKEY_HELPER", path)
	got := resolveApiKeyHelperPath()
	assert.Empty(t, got, "non-executable override must be rejected; got %q", got)
}

// TestResolveApiKeyHelperPath_NoOverrideReturnsEmpty pins the opt-in
// contract: with TORQUE_APIKEY_HELPER unset, the function returns ""
// so Boot omits the apiKeyHelper field from .claude/settings.json and
// claude uses its default keychain/env discovery. This is the
// subscription-OAuth-friendly default introduced 2026-05-26.
func TestResolveApiKeyHelperPath_NoOverrideReturnsEmpty(t *testing.T) {
	t.Setenv("TORQUE_APIKEY_HELPER", "")
	got := resolveApiKeyHelperPath()
	assert.Empty(t, got,
		"resolveApiKeyHelperPath must return \"\" when TORQUE_APIKEY_HELPER is unset; got %q", got)
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
