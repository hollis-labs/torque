package testenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// RunWithAgentShims installs the agent CLI shims (InstallAgentShims), runs
// the package's tests and removes the shims. Call it from TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(testenv.RunWithAgentShims(m)) }
func RunWithAgentShims(m *testing.M) int {
	dir, err := InstallAgentShims()
	if err != nil {
		fmt.Fprintf(os.Stderr, "testenv: install agent CLI shims: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)
	return m.Run()
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
