package agent_boot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

func requireWriteProtect(t *testing.T) {
	t.Helper()
	caps := sandbox.ResolveBackendCapabilities("", sandbox.BackendAuto)
	if !caps.Supported || !slices.Contains(caps.Capabilities, sandbox.CapWriteProtect) {
		t.Skipf("the %s sandbox backend cannot write-protect paths here", caps.Backend)
	}
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
	assert.Contains(t, strings.ToLower(rec.ProtectWrite), "read-only file system", "the agent must not write a protected directory")
	assert.NoFileExists(t, filepath.Join(protect, "agent-wrote"))
	assert.Equal(t, "wrote", rec.CwdWrite, "the agent still writes its working directory")
	require.NoError(t, os.WriteFile(filepath.Join(protect, "host-wrote"), []byte("x"), 0o600), "the host still writes its own state")
}

// bootLegacy (codex app-server, agentkit StartOptions.ProtectedPaths): the
// agent's write into the protected directory fails.
func TestBoot_ProtectedPathsDenyAgentWrites_Legacy(t *testing.T) {
	requireWriteProtect(t)
	installHelper(t, "codex", "CODEX_CLI_PATH", "TestCodexRPCProcessHelper")
	protect := protectedDir(t)
	dir := t.TempDir()
	recordPath := filepath.Join(dir, "process.json")

	cd := composeDeps(t, fakeRuntimeConfig{}, "codex")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.ProtectedPaths = []string{protect}
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "codex"}}
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
	assert.Contains(t, strings.ToLower(rec.ProtectWrite), "read-only file system")
	assert.NoFileExists(t, filepath.Join(protect, "agent-wrote"))
	require.NoError(t, os.WriteFile(filepath.Join(protect, "host-wrote"), []byte("x"), 0o600))
}

// An ACP launch is refused while protection is on: go-agent-wrapper cannot
// write-protect it yet (CW-20261001-0162). Nothing is launched.
func TestBoot_ProtectedPathsRefuseACP(t *testing.T) {
	fake := providertest.New(t, runtimes.Copilot, providertest.Replay("copilot/acp_turn"))
	fake.ExpectErrors()
	fake.Install()
	cd := composeACPDeps(t, "copilot")
	cd.Deps.ProtectedPaths = []string{protectedDir(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: "CW-ACP-PROTECT", AgentProfile: "worker", Workdir: t.TempDir(),
		Mode: agent.ModeOneShot, Description: "say hello",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ACP sandbox protect not yet supported (CW-20261001-0162); set TORQUE_SANDBOX_PROTECT=0 to launch ACP unprotected")
	assert.Empty(t, fake.Calls(), "nothing is launched")
}
