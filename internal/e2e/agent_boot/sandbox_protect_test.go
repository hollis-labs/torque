package agent_boot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/providertest"
	"github.com/hollis-labs/go-sandbox/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// Torque write-protects its control-plane directories from the agents it
// launches (CW-20261001-0141). These boot real launch paths with a protected
// directory and a helper standing in for the CLI that tries to write into it.

// requireSandboxTestsEnv turns a skipped sandbox test into a failure. The
// agent-os gate sets it, so the tests that enforce write-protection are known
// to have run there; a CI runner without a usable bubblewrap leaves it unset
// and they skip, with the reason.
const requireSandboxTestsEnv = "TORQUE_REQUIRE_SANDBOX_TESTS"

// requireWriteProtect skips the test when this host cannot actually
// write-protect a path. The backend's own capability report is not enough: on
// Linux it is static and always says supported, so it cannot tell a runner
// with no bubblewrap, or one that forbids unprivileged user namespaces
// (Ubuntu's AppArmor restriction), from a working host. Production stays
// fail-closed: there a launch is refused when the sandbox cannot be applied.
func requireWriteProtect(t *testing.T) {
	t.Helper()
	reason := writeProtectUnavailable()
	if reason == "" {
		return
	}
	if os.Getenv(requireSandboxTestsEnv) == "1" {
		t.Fatalf("%s=1, but this host cannot write-protect paths: %s", requireSandboxTestsEnv, reason)
	}
	t.Skipf("this host cannot write-protect paths: %s (set %s=1 to fail instead)", reason, requireSandboxTestsEnv)
}

// writeProtectUnavailable returns why a launch cannot be write-protected here,
// or "" when it can. On Linux it runs bubblewrap with the user namespace the
// protection needs, rather than trusting a capability report.
func writeProtectUnavailable() string {
	caps := sandbox.ResolveBackendCapabilities("", sandbox.BackendAuto)
	if !caps.Supported || !slices.Contains(caps.Capabilities, sandbox.CapWriteProtect) {
		return fmt.Sprintf("the %s sandbox backend reports no write-protect capability", caps.Backend)
	}
	if runtime.GOOS != "linux" {
		return ""
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return "bwrap (bubblewrap) is not on PATH"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, bwrap, "--unshare-user", "--ro-bind", "/", "/", "true").CombinedOutput(); err != nil {
		return fmt.Sprintf("bwrap cannot start a sandbox here (%v: %s)", err, strings.TrimSpace(string(out)))
	}
	return ""
}

// requireDenied asserts a write failed because the directory is
// write-protected: a read-only bind mount on Linux, a sandbox denial on
// macOS.
func requireDenied(t *testing.T, got string) {
	t.Helper()
	got = strings.ToLower(got)
	assert.True(t, strings.Contains(got, "read-only file system") || strings.Contains(got, "operation not permitted"), "the agent must not write a protected directory: %q", got)
}

// protectedDir returns a real, existing directory to protect, outside the
// agent's working directory.
func protectedDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return dir
}

type protectProbeRecord struct {
	ProtectWrite string `json:"protect_write"`
	CwdWrite     string `json:"cwd_write"`
}

// TestProtectProbeHelper stands in for claude: at start it tries to write
// into TORQUE_TEST_PROTECT_DIR and into its working directory, records both
// outcomes outside the protected dir, then reads stdin until it closes.
func TestProtectProbeHelper(t *testing.T) {
	if os.Getenv("TORQUE_TEST_PROTECT_PROBE") != "1" {
		return
	}
	attempt := func(path string) string {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			return err.Error()
		}
		return "wrote"
	}
	rec := protectProbeRecord{
		ProtectWrite: attempt(filepath.Join(os.Getenv("TORQUE_TEST_PROTECT_DIR"), "agent-wrote")),
		CwdWrite:     attempt("agent-cwd-write"),
	}
	raw, _ := json.Marshal(rec)
	if err := os.WriteFile(os.Getenv("TORQUE_TEST_PROTECT_RECORD"), raw, 0o600); err != nil {
		os.Exit(3)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

// installHelper writes a shim named name that runs this test binary's
// helper test, points envVar at it and puts its dir first on PATH.
func installHelper(t *testing.T, name, envVar, testName string) {
	t.Helper()
	dir := t.TempDir()
	executable, err := os.Executable()
	require.NoError(t, err)
	binary := filepath.Join(dir, name)
	quoted := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
	require.NoError(t, os.WriteFile(binary, []byte(fmt.Sprintf("#!/bin/sh\nexec %s -test.run=%s -- \"$@\"\n", quoted, testName)), 0o700))
	t.Setenv(envVar, binary)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// The wrapper path (claude-code, streaming-stdio): the agent's write into
// the protected directory fails, its write in its working directory lands,
// and the host can still write the protected directory itself.
func TestBoot_ProtectedPathsDenyAgentWrites_Wrapper(t *testing.T) {
	requireWriteProtect(t)
	installHelper(t, "claude", "CLAUDE_CLI_PATH", "TestProtectProbeHelper")
	protect := protectedDir(t)
	record := filepath.Join(t.TempDir(), "probe.json")

	cd := composeDeps(t, fakeRuntimeConfig{}, "claude-code")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.ProtectedPaths = []string{protect}
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "claude-code"}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: "CW-TEST-PROTECT", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeLongLived,
		Env: map[string]string{"TORQUE_TEST_PROTECT_PROBE": "1", "TORQUE_TEST_PROTECT_DIR": protect, "TORQUE_TEST_PROTECT_RECORD": record},
	})
	require.NoError(t, err, "a protected launch runs")
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })

	var rec protectProbeRecord
	require.Eventually(t, func() bool {
		raw, err := os.ReadFile(record)
		return err == nil && json.Unmarshal(raw, &rec) == nil
	}, 10*time.Second, 20*time.Millisecond, "the helper never ran")
	requireDenied(t, rec.ProtectWrite)
	assert.NoFileExists(t, filepath.Join(protect, "agent-wrote"))
	assert.Equal(t, "wrote", rec.CwdWrite, "the agent still writes its working directory")
	require.NoError(t, os.WriteFile(filepath.Join(protect, "host-wrote"), []byte("x"), 0o600), "the host still writes its own state")
}

// bootLegacy (codex app-server, agentkit StartOptions.ProtectedPaths): the
// agent's write into the protected directory fails. The profile bypasses
// codex's own sandbox (danger-full-access), so Torque's is the only one.
func TestBoot_ProtectedPathsDenyAgentWrites_Legacy(t *testing.T) {
	requireWriteProtect(t)
	installHelper(t, "codex", "CODEX_CLI_PATH", "TestCodexRPCProcessHelper")
	protect := protectedDir(t)
	dir := t.TempDir()
	recordPath := filepath.Join(dir, "process.json")

	cd := composeDeps(t, fakeRuntimeConfig{}, "codex")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.ProtectedPaths = []string{protect}
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "codex", PermissionMode: "bypassPermissions"}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: "CW-TEST-PROTECT-LEGACY", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeLongLived,
		Env: map[string]string{"TORQUE_TEST_CODEX_HELPER": "1", "TORQUE_TEST_CODEX_RECORD": recordPath, "TORQUE_TEST_PROTECT_DIR": protect},
	})
	require.NoError(t, err, "a protected codex app-server launch runs")
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })

	raw, err := os.ReadFile(recordPath)
	require.NoError(t, err)
	var rec codexProcessRecord
	require.NoError(t, json.Unmarshal(raw, &rec))
	requireDenied(t, rec.ProtectWrite)
	assert.NoFileExists(t, filepath.Join(protect, "agent-wrote"))
	require.NoError(t, os.WriteFile(filepath.Join(protect, "host-wrote"), []byte("x"), 0o600))
}

// A codex launch that runs its commands in codex's own sandbox (here
// app-server under the default posture: workspace-write) is not wrapped in
// Torque's, where codex's sandbox could not start (a nested user namespace
// is denied). Codex's sandbox confines writes to the working directory,
// temp and its writable_roots, and the config Torque plants names no
// protected directory among them. The helper standing in for codex is not
// sandboxed by anything, so its write into the protected directory lands:
// that is the proof Torque left this launch alone.
func TestBoot_CodexOwnSandboxIsNotWrapped(t *testing.T) {
	installHelper(t, "codex", "CODEX_CLI_PATH", "TestCodexRPCProcessHelper")
	protect := protectedDir(t)
	recordPath := filepath.Join(t.TempDir(), "process.json")

	cd := composeDeps(t, fakeRuntimeConfig{}, "codex")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.ProtectedPaths = []string{protect}
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "codex"}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: "CW-TEST-PROTECT-CODEX-SANDBOX", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeLongLived,
		Env: map[string]string{"TORQUE_TEST_CODEX_HELPER": "1", "TORQUE_TEST_CODEX_RECORD": recordPath, "TORQUE_TEST_PROTECT_DIR": protect},
	})
	require.NoError(t, err, "codex in its own sandbox launches with protection on")
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })

	raw, err := os.ReadFile(recordPath)
	require.NoError(t, err)
	var rec codexProcessRecord
	require.NoError(t, json.Unmarshal(raw, &rec))
	assert.Equal(t, "wrote", rec.ProtectWrite, "Torque does not wrap a launch codex sandboxes itself")
	settings, err := os.ReadFile(filepath.Join(rec.Home, "config.toml"))
	require.NoError(t, err)
	assert.Contains(t, string(settings), `sandbox_mode = "workspace-write"`)
	for _, line := range strings.Split(string(settings), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "writable_roots") {
			assert.NotContains(t, line, protect, "codex's writable_roots must not include a protected directory")
		}
	}
}

// Protection that is on but could not be set up refuses every launch, and
// says how to turn it off: Torque fails closed.
func TestBoot_ProtectRefusalRefusesLaunch(t *testing.T) {
	fake := providertest.New(t, runtimes.Claude, providertest.Script(providertest.AwaitEOF()))
	fake.ExpectErrors()
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, "claude-code")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.ProtectRefusal = "no Torque control-plane directory could be write-protected, so agent launches are refused; set TORQUE_SANDBOX_PROTECT=0 to launch agents without protection"
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "claude-code"}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := cd.Manager.Boot(ctx, agent.Options{TaskID: "CW-TEST-PROTECT-REFUSED", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeLongLived})
	require.Error(t, err)
	assert.ErrorIs(t, err, agent.ErrBootFailed)
	assert.Contains(t, err.Error(), "TORQUE_SANDBOX_PROTECT=0")
	assert.Empty(t, fake.Calls(), "nothing is launched")
}

// An ACP launch is write-protected like any other (CW-20261001-0162,
// go-agent-wrapper v0.25.0+): with no sandbox policy resolved, the agent runs
// under the wrapper's protect-only profile, so its write into the protected
// directory fails, its write in its working directory lands, the session comes
// up, and the host can still write the directory itself. Tested against an ACP
// agent helper (go-providers' copilot fixtures cannot try a write); not
// live-tested against a real Copilot or pi binary, which this host lacks.
func TestBoot_ProtectedPathsDenyAgentWrites_ACP(t *testing.T) {
	requireWriteProtect(t)
	installHelper(t, "copilot", "COPILOT_CLI_PATH", "TestACPAgentHelper")
	protect := protectedDir(t)
	dir := t.TempDir()
	record := filepath.Join(dir, "probe.json")

	cd := composeACPDeps(t, "copilot")
	cd.Deps.ProtectedPaths = []string{protect}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: "CW-ACP-PROTECT", AgentProfile: "worker", Workdir: t.TempDir(),
		Mode: agent.ModeOneShot, Description: "say hello",
		Env: map[string]string{
			"TORQUE_TEST_ACP_AGENT": "1", "TORQUE_TEST_ACP_RECORD": filepath.Join(dir, "mcp-servers.json"),
			"TORQUE_TEST_PROTECT_PROBE": "1", "TORQUE_TEST_PROTECT_DIR": protect, "TORQUE_TEST_PROTECT_RECORD": record,
		},
	})
	require.NoError(t, err, "a protected ACP launch runs")
	assert.Equal(t, agent.StatusDone, sess.Status)

	var rec protectProbeRecord
	require.Eventually(t, func() bool {
		raw, err := os.ReadFile(record)
		return err == nil && json.Unmarshal(raw, &rec) == nil
	}, 10*time.Second, 20*time.Millisecond, "the helper never ran")
	requireDenied(t, rec.ProtectWrite)
	assert.NoFileExists(t, filepath.Join(protect, "agent-wrote"))
	assert.Equal(t, "wrote", rec.CwdWrite, "the agent still writes its working directory")
	require.NoError(t, os.WriteFile(filepath.Join(protect, "host-wrote"), []byte("x"), 0o600), "the host still writes its own state")
}

// With protection off (TORQUE_SANDBOX_PROTECT=0 leaves no protected paths) an
// ACP launch is not wrapped, and its write into a directory lands: the kill
// switch still turns the new protection off.
func TestBoot_ACPUnprotectedWithoutProtectedPaths(t *testing.T) {
	installHelper(t, "copilot", "COPILOT_CLI_PATH", "TestACPAgentHelper")
	target := protectedDir(t)
	dir := t.TempDir()
	record := filepath.Join(dir, "probe.json")

	cd := composeACPDeps(t, "copilot")
	cd.Deps.ProtectedPaths = nil
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: "CW-ACP-NOPROTECT", AgentProfile: "worker", Workdir: t.TempDir(),
		Mode: agent.ModeOneShot, Description: "say hello",
		Env: map[string]string{
			"TORQUE_TEST_ACP_AGENT": "1", "TORQUE_TEST_ACP_RECORD": filepath.Join(dir, "mcp-servers.json"),
			"TORQUE_TEST_PROTECT_PROBE": "1", "TORQUE_TEST_PROTECT_DIR": target, "TORQUE_TEST_PROTECT_RECORD": record,
		},
	})
	require.NoError(t, err)
	var rec protectProbeRecord
	require.Eventually(t, func() bool {
		raw, err := os.ReadFile(record)
		return err == nil && json.Unmarshal(raw, &rec) == nil
	}, 10*time.Second, 20*time.Millisecond)
	assert.Equal(t, "wrote", rec.ProtectWrite, "no protected paths: nothing is write-protected")
}
