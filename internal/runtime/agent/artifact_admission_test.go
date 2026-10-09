package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/harness/adapters/provider"
	"github.com/hollis-labs/substrate/harness/agentlaunch"
	"github.com/hollis-labs/substrate/harness/agentlaunch/launcher"
	"github.com/hollis-labs/substrate/llm-core/contracts/runtimes"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/launchartifacts"
	"github.com/hollis-labs/torque/internal/launchprofile"
	"github.com/stretchr/testify/require"
)

func artifactFixture(t *testing.T, providerID string, mode runtimes.Mode) (*Manager, *agentlaunch.PreparedLaunch, *launchartifacts.Custody, func()) {
	t.Helper()
	m := NewManager(nil)
	profile := config.AgentProfile{Provider: providerID}
	if providerID == "claude" {
		profile.Provider = "claude-code"
	}
	resolved := launchprofile.CompiledLaunchProfile{AgentProfile: profile, AgentProfileName: "worker"}
	injection, err := taskInjection([]agentlaunch.NativeFile{{Kind: agentlaunch.NativeFileRaw, RelPath: "tasks/fixture.md", Content: "task context"}})
	require.NoError(t, err)
	plan := launchprofile.BuildLaunchPlan(resolved, launchprofile.TaskLaunchOverlay{
		SessionID: "fixture", ProviderID: providerID, ProviderBinary: "/bin/true", RuntimeKind: mode,
		Workdir: t.TempDir(), WorkspaceDir: t.TempDir(), BuildDirRoot: t.TempDir(),
		SystemPrompt: "actual system instructions", KickoffMD: "actual kickoff", PermissionMode: resolveLaunchPermissionMode(profile), Injection: injection,
	})
	compiled, err := launcher.Compile(context.Background(), plan)
	require.NoError(t, err)
	admission, release, err := m.beginArtifactLaunch(context.Background(), "fixture", t.TempDir(), compiled)
	require.NoError(t, err)
	t.Cleanup(release)
	prepared, custody, err := launchartifacts.Prepare(context.Background(), compiled, admission)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, custody.Close()) })
	return m, prepared, custody, release
}

func TestArtifactAdmissionRefusesChangedLaunch(t *testing.T) {
	for _, change := range []string{"retired", "stopped", "plan", "root", "target", "replay"} {
		t.Run(change, func(t *testing.T) {
			m, prepared, custody, release := artifactFixture(t, "claude", runtimes.ModeStreamingStdio)
			target := prepared.PlantedBootDir
			switch change {
			case "retired":
				release()
			case "stopped":
				m.mu.Lock()
				m.stopped = true
				m.mu.Unlock()
			case "plan":
				prepared.Compiled.Plan.Provider.Binary = "/substituted"
			case "root":
				require.NoError(t, os.Rename(target, target+"-held"))
				require.NoError(t, os.Mkdir(target, 0700))
			case "target":
				target = t.TempDir()
			case "replay":
				authority, err := custody.Authorize(context.Background(), target)
				require.NoError(t, err)
				require.NoError(t, authority.Close())
			}
			_, err := custody.Authorize(context.Background(), target)
			require.Error(t, err)
		})
	}
}

func TestArtifactAdmissionRejectsConcurrentSession(t *testing.T) {
	m, p, _, release := artifactFixture(t, "claude", runtimes.ModeStreamingStdio)
	_, _, err := m.beginArtifactLaunch(context.Background(), "fixture", t.TempDir(), p.Compiled)
	require.Error(t, err)
	release()
	m.mu.Lock()
	m.bootDirs["fixture"] = p.PlantedBootDir
	m.mu.Unlock()
	_, _, err = m.beginArtifactLaunch(context.Background(), "fixture", t.TempDir(), p.Compiled)
	require.Error(t, err)
}

func TestCanonicalArtifactsPreserveProviderLaunch(t *testing.T) {
	for _, tc := range []struct {
		provider string
		mode     runtimes.Mode
	}{
		{"claude", runtimes.ModeStreamingStdio}, {"claude", runtimes.ModeSubprocessPerTurn},
		{"opencode", runtimes.ModeSubprocessPerTurn}, {"opencode", runtimes.ModeHTTPSSE},
		{"codex", runtimes.ModeJSONRPCStdio}, {"codex", runtimes.ModeSubprocessPerTurn},
		{"antigravity", runtimes.ModeSubprocessPerTurn},
	} {
		t.Run(tc.provider+"/"+string(tc.mode), func(t *testing.T) {
			_, p, custody, _ := artifactFixture(t, tc.provider, tc.mode)
			profile := config.AgentProfile{Provider: tc.provider}
			if tc.provider == "claude" {
				profile.Provider = "claude-code"
			}
			selected, err := selectRuntime(profile, "worker", ParseRuntimeKind(string(tc.mode)))
			require.NoError(t, err)
			p.PlantContext.MCPLoopbackURL = "http://127.0.0.1:1234/mcp"
			p.PlantContext.SelfMCPCommand = "/fixture/mux"
			p.PlantContext.SelfMCPArgs = []string{"mcp", "--only", "fixture"}
			bp, ok := selected.cli.(provider.BootDirProvider)
			require.True(t, ok)
			execution, err := plantCanonicalArtifacts(context.Background(), p, bp, custody.Authorize)
			require.NoError(t, err)
			require.NotNil(t, execution.Materialization)
			require.Equal(t, "/bin/true", execution.Bindings.Argv[0])
			content, err := os.ReadFile(filepath.Join(p.PlantedBootDir, "tasks/fixture.md"))
			require.NoError(t, err)
			require.Equal(t, "task context", string(content))
			kickoff, err := os.ReadFile(filepath.Join(p.PlantedBootDir, "boot.md"))
			require.NoError(t, err)
			require.Equal(t, "actual kickoff", strings.TrimSpace(string(kickoff)))
			if tc.provider == "codex" {
				_, err := os.Stat(filepath.Join(p.PlantedBootDir, "auth.json"))
				require.True(t, os.IsNotExist(err), "pure artifacts must not create credentials")
				require.Equal(t, p.PlantedBootDir, execution.Bindings.Env["CODEX_HOME"].Value)
				require.NotEmpty(t, execution.Effects, "separate credential requirement must survive")
			}
		})
	}
}
