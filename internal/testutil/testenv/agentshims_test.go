package testenv

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestInstallAgentShims_ShadowRealCLIs(t *testing.T) {
	t.Setenv("PATH", os.Getenv("PATH"))
	t.Setenv("CLAUDE_CLI_PATH", "/somewhere/real/claude")
	dir, err := InstallAgentShims()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	for _, name := range AgentCLIs {
		got, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("LookPath(%s): %v", name, err)
		}
		if filepath.Dir(got) != dir {
			t.Fatalf("LookPath(%s) = %s, want the shim in %s", name, got, dir)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = exec.CommandContext(ctx, name, "-p", "hello").Run()
		cancel()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != AgentShimExitCode {
			t.Fatalf("%s: want exit %d from the shim, got %v", name, AgentShimExitCode, err)
		}
	}
	if got := os.Getenv("CLAUDE_CLI_PATH"); got != filepath.Join(dir, "claude") {
		t.Fatalf("CLAUDE_CLI_PATH = %q, want the shim", got)
	}
}

func TestIsolateTempDir(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("TMPDIR", parent)
	root, err := IsolateTempDir()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	if filepath.Dir(root) != parent {
		t.Fatalf("root %s is not under the previous TMPDIR %s", root, parent)
	}
	if os.TempDir() != root {
		t.Fatalf("os.TempDir() = %s, want the isolated root %s", os.TempDir(), root)
	}
}

func TestLeakedBootDirs(t *testing.T) {
	root := t.TempDir()
	if got := LeakedBootDirs(root, 0); got != nil {
		t.Fatalf("no torque-boot dir: want nil, got %v", got)
	}
	leaf := filepath.Join(root, BootDirRootName, "agentlaunch-bootdir-abc-1")
	if err := os.MkdirAll(leaf, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "boot.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := LeakedBootDirs(root, 0)
	if len(got) != 1 || got[0] != "agentlaunch-bootdir-abc-1 [boot.md]" {
		t.Fatalf("want the leaked dir and its planted file, got %v", got)
	}

	// A teardown still finishing within the grace period is not a leak.
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = os.RemoveAll(leaf)
	}()
	if got := LeakedBootDirs(root, 2*time.Second); got != nil {
		t.Fatalf("removed within the grace period: want nil, got %v", got)
	}
}
