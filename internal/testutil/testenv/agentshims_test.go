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
