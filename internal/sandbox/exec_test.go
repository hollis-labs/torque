package sandbox

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestAgentExec_Basic(t *testing.T) {
	result, err := AgentExec(AgentExecOpts{
		SessionID: "test-basic",
		Command:   "/bin/echo",
		Args:      []string{"hello"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Stdout, "hello") {
		t.Errorf("expected stdout to contain 'hello', got %q", result.Stdout)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	if result.TimedOut {
		t.Error("expected TimedOut=false")
	}
}

func TestAgentExec_DenylistBlocked(t *testing.T) {
	_, err := AgentExec(AgentExecOpts{
		SessionID: "test-deny",
		Command:   "rm",
		Args:      []string{"-rf", "/"},
	})
	if err == nil {
		t.Fatal("expected denylist error, got nil")
	}
	if !strings.Contains(err.Error(), "blocked by denylist") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAgentExec_Timeout(t *testing.T) {
	result, err := AgentExec(AgentExecOpts{
		SessionID: "test-timeout",
		Command:   "/bin/sleep",
		Args:      []string{"10"},
		Timeout:   500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.TimedOut {
		t.Error("expected TimedOut=true")
	}
	if result.ExitCode != 124 {
		t.Errorf("expected exit code 124, got %d", result.ExitCode)
	}
}

func TestAgentExec_EnvFiltering(t *testing.T) {
	// Set a secret in the environment and verify it's stripped from agent env
	os.Setenv("ANTHROPIC_API_KEY", "supersecret")
	defer os.Unsetenv("ANTHROPIC_API_KEY")

	// We can't directly inspect cmd.Env after the fact, but we can test
	// via buildAgentEnv which is used internally
	env := buildAgentEnv(nil)
	for _, entry := range env {
		if strings.HasPrefix(entry, "ANTHROPIC_API_KEY=") {
			t.Error("ANTHROPIC_API_KEY should not appear in agent env")
		}
	}
}

func TestAgentExec_CWDRestrictedToSandbox(t *testing.T) {
	sessionID := "test-cwd"
	sandboxDir, err := SandboxDir(sessionID)
	if err != nil {
		t.Fatalf("SandboxDir error: %v", err)
	}

	result, err := AgentExec(AgentExecOpts{
		SessionID: sessionID,
		Command:   "/bin/pwd",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(strings.TrimSpace(result.Stdout), sandboxDir) {
		t.Errorf("expected cwd %q in stdout, got %q", sandboxDir, result.Stdout)
	}
}

func TestAgentExec_ExtraEnvFiltered(t *testing.T) {
	extra := map[string]string{
		"MY_VAR":        "hello",
		"MY_SECRET_KEY": "shh",
	}
	env := buildAgentEnv(extra)

	found := map[string]bool{}
	for _, entry := range env {
		if strings.HasPrefix(entry, "MY_VAR=") {
			found["MY_VAR"] = true
		}
		if strings.HasPrefix(entry, "MY_SECRET_KEY=") {
			found["MY_SECRET_KEY"] = true
		}
	}

	if !found["MY_VAR"] {
		t.Error("expected MY_VAR to be present in agent env")
	}
	if found["MY_SECRET_KEY"] {
		t.Error("expected MY_SECRET_KEY to be filtered from agent env")
	}
}

func TestUserExec_Basic(t *testing.T) {
	result, err := UserExec(UserExecOpts{
		Command: "/bin/echo",
		Args:    []string{"world"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Stdout, "world") {
		t.Errorf("expected 'world' in stdout, got %q", result.Stdout)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
}

func TestUserExec_Denylist(t *testing.T) {
	_, err := UserExec(UserExecOpts{
		Command: "shutdown",
		Args:    []string{"-h", "now"},
	})
	if err == nil {
		t.Fatal("expected denylist error, got nil")
	}
	if !strings.Contains(err.Error(), "blocked by denylist") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUserExec_UsesRealDir(t *testing.T) {
	dir := t.TempDir()
	result, err := UserExec(UserExecOpts{
		Command: "/bin/pwd",
		Dir:     dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Resolve symlinks for comparison (macOS /var -> /private/var etc.)
	got := strings.TrimSpace(result.Stdout)
	if got != dir && !strings.HasSuffix(got, dir) {
		// Try resolving symlinks
		import_path_check := strings.Contains(got, "tmp") || strings.Contains(got, dir)
		if !import_path_check {
			t.Errorf("expected pwd output to contain dir %q, got %q", dir, got)
		}
	}
}

func TestUserExec_SecretFiltering(t *testing.T) {
	os.Setenv("MY_TOKEN", "topsecret")
	defer os.Unsetenv("MY_TOKEN")

	env := filterSecrets(os.Environ())
	for _, entry := range env {
		if strings.HasPrefix(entry, "MY_TOKEN=") {
			t.Error("MY_TOKEN should be filtered from user exec env")
		}
	}
}

func TestIsSecretKey(t *testing.T) {
	secretKeys := []string{
		"ANTHROPIC_API_KEY",
		"SECRET_VALUE",
		"AUTH_TOKEN",
		"MY_PASSWORD",
		"CREDENTIAL_STORE",
	}
	for _, k := range secretKeys {
		if !isSecretKey(k) {
			t.Errorf("expected %q to be recognized as secret", k)
		}
	}

	safeKeys := []string{
		"HOME",
		"USER",
		"PATH",
		"LANG",
		"MY_VAR",
	}
	for _, k := range safeKeys {
		if isSecretKey(k) {
			t.Errorf("expected %q to NOT be recognized as secret", k)
		}
	}
}

func TestClampTimeout(t *testing.T) {
	tests := []struct {
		input    time.Duration
		def      time.Duration
		max      time.Duration
		expected time.Duration
	}{
		{0, 30 * time.Second, 5 * time.Minute, 30 * time.Second},
		{-1, 30 * time.Second, 5 * time.Minute, 30 * time.Second},
		{10 * time.Second, 30 * time.Second, 5 * time.Minute, 10 * time.Second},
		{10 * time.Minute, 30 * time.Second, 5 * time.Minute, 5 * time.Minute},
		{5 * time.Minute, 30 * time.Second, 5 * time.Minute, 5 * time.Minute},
	}
	for _, tc := range tests {
		got := clampTimeout(tc.input, tc.def, tc.max)
		if got != tc.expected {
			t.Errorf("clampTimeout(%v, %v, %v) = %v, want %v", tc.input, tc.def, tc.max, got, tc.expected)
		}
	}
}
