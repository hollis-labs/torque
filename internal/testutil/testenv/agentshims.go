package testenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// AgentCLIs are the agent CLI binaries Torque or its launch libraries can
// exec. Keep it a superset: a shim for a CLI nobody launches costs nothing.
var AgentCLIs = []string{"agy", "antigravity", "claude", "codex", "copilot", "cursor-agent", "gemini", "opencode", "pi"}

// agentCLIPathEnv maps a CLI to the override go-providers' Detect reads
// before PATH.
var agentCLIPathEnv = map[string]string{
	"agy":      "AGY_CLI_PATH",
	"claude":   "CLAUDE_CLI_PATH",
	"codex":    "CODEX_CLI_PATH",
	"opencode": "OPENCODE_CLI_PATH",
}

// AgentShimExitCode is the status every shim exits with.
const AgentShimExitCode = 97

// RunWithAgentShims runs the package's tests in a temp root of their own,
// with the agent CLI shims installed (InstallAgentShims), and removes both.
// Call it from TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(testenv.RunWithAgentShims(m)) }
//
// The temp root (IsolateTempDir) keeps the boot dirs, run sidecars and
// t.TempDir()s a test binary writes off the shared $TMPDIR, and a test that
// leaves a boot dir behind fails the package (CW-20261001-0144).
func RunWithAgentShims(m *testing.M) int {
	root, err := IsolateTempDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "testenv: isolate the temp dir: %v\n", err)
		return 1
	}
	defer os.RemoveAll(root)
	if _, err := InstallAgentShims(); err != nil {
		fmt.Fprintf(os.Stderr, "testenv: install agent CLI shims: %v\n", err)
		return 1
	}
	code := m.Run()
	if leaked := LeakedBootDirs(root, 2*time.Second); len(leaked) > 0 {
		fmt.Fprintf(os.Stderr, "testenv: tests left %d boot dir(s) under %s; every booted session must be stopped or its dir removed (CW-20261001-0144):\n  %s\n",
			len(leaked), filepath.Join(root, BootDirRootName), strings.Join(leaked, "\n  "))
		if code == 0 {
			code = 1
		}
	}
	return code
}

// BootDirRootName is the directory under $TMPDIR that Torque plants boot
// dirs in: agent.BuildDirRootName, spelled out here because the agent
// package's own tests import testenv.
const BootDirRootName = "torque-boot"

// IsolateTempDir points $TMPDIR at a fresh directory under the current one
// and returns it; the caller removes it. Everything that resolves its
// location through os.TempDir() then lands there: Torque's boot dirs
// ($TMPDIR/torque-boot), the run stderr sidecars ($TMPDIR/torque/runs when
// TORQUE_DATA_DIR is unset), t.TempDir() and the agent CLI shims. A helper
// process a test re-execs inherits the variable, so what it leaves when it
// exits through os.Exit stays inside the root too.
//
// Before, every package's test binary wrote to the shared $TMPDIR, a 16G
// tmpfs on the overnight host: boot dirs from tests that did not clean up
// (1.6G at one point) and a torque-agent-shims-* dir per re-exec'd helper
// (456 of them).
func IsolateTempDir() (string, error) {
	root, err := os.MkdirTemp("", "torque-test-")
	if err != nil {
		return "", err
	}
	if err := os.Setenv("TMPDIR", root); err != nil {
		_ = os.RemoveAll(root)
		return "", err
	}
	return root, nil
}

// LeakedBootDirs lists the boot dirs left under root's BootDirRootName,
// waiting up to grace for session teardowns still finishing to remove
// theirs. Each entry names the dir and the files planted in it, which say
// which task booted it.
func LeakedBootDirs(root string, grace time.Duration) []string {
	dir := filepath.Join(root, BootDirRootName)
	deadline := time.Now().Add(grace)
	for {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			out := make([]string, 0, len(entries))
			for _, e := range entries {
				planted, _ := filepath.Glob(filepath.Join(dir, e.Name(), "*"))
				for i, p := range planted {
					planted[i] = filepath.Base(p)
				}
				out = append(out, fmt.Sprintf("%s [%s]", e.Name(), strings.Join(planted, " ")))
			}
			return out
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// InstallAgentShims keeps a package's tests from running a real agent CLI
// (CW-20261001-0041). Torque's launch path resolves an agent binary by bare
// name through PATH (the wrapper path's argv[0] is the provider
// descriptor's binary name) or through a *_CLI_PATH override, and
// go-providers' Detect falls back to install dirs such as ~/.local/bin. A
// test that boots a session without its own fixture therefore ran the
// developer's real `claude` or `opencode`: a paid model call, the user's
// own hooks, and seconds of latency.
//
// It writes a refusing shim for every name in AgentCLIs into a fresh temp
// dir, puts the dir first on PATH and points the *_CLI_PATH overrides at
// the shims. A shim prints why on stderr and exits AgentShimExitCode, so a
// launch that reaches one fails fast. A test that needs a working fake
// installs its own with t.Setenv (PATH and *_CLI_PATH), which wins. It
// returns the dir; the caller removes it.
func InstallAgentShims() (string, error) {
	dir, err := os.MkdirTemp("", "torque-agent-shims-")
	if err != nil {
		return "", err
	}
	for _, name := range AgentCLIs {
		// Single-quoted and free of backticks: the message is text, never
		// a command substitution that would re-run the shim.
		script := fmt.Sprintf("#!/bin/sh\necho 'testenv: refused to run a real %s from a Torque test; install a fixture on PATH (internal/testutil/testenv)' >&2\nexit %d\n",
			name, AgentShimExitCode)
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
		if env, ok := agentCLIPathEnv[name]; ok {
			if err := os.Setenv(env, path); err != nil {
				_ = os.RemoveAll(dir)
				return "", err
			}
		}
	}
	path := strings.Join([]string{dir, os.Getenv("PATH")}, string(os.PathListSeparator))
	if err := os.Setenv("PATH", path); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}
