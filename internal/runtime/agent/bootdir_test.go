package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPlantBootDir_Claude verifies that the lib's BootDirSpec for claude
// produces the expected file shape inside the per-task tempdir, and that
// clockwork-side wiring (PlantContext, MCP loopback URL) lands in the
// rendered files.
func TestPlantBootDir_Claude(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	res, err := plantBootDir(plantParams{
		Provider:       "claude",
		Adapter:        provider.NewClaudeAdapter(),
		TaskID:         "CW-20260508-0001",
		RunID:          7,
		AgentName:      "default",
		SystemPrompt:   "You are an orchestrator.",
		KickoffContent: "# Boot\n\nDo the work.",
		ProjectDir:     "/tmp/repo",
		MCPLoopbackURL: "http://127.0.0.1:54321/mcp",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	defer func() { _ = os.RemoveAll(res.BootDir) }()

	// Naming convention.
	assert.Contains(t, res.BootDir, "clockwork-boot-claude-CW-20260508-0001-r7-")

	// Lib's BootDirSpec planted: CLAUDE.md + boot.md + .claude/settings.json + .mcp.json.
	for _, rel := range []string{"CLAUDE.md", "boot.md", ".claude/settings.json", ".mcp.json"} {
		_, err := os.Stat(filepath.Join(res.BootDir, rel))
		assert.NoError(t, err, "expected %s under bootDir", rel)
	}

	// PlantContext threaded through: SystemPrompt + KickoffContent + URL.
	claudeMD, _ := os.ReadFile(filepath.Join(res.BootDir, "CLAUDE.md"))
	assert.Contains(t, string(claudeMD), "You are an orchestrator.")
	assert.Contains(t, string(claudeMD), "http://127.0.0.1:54321/mcp")

	bootMD, _ := os.ReadFile(filepath.Join(res.BootDir, "boot.md"))
	assert.Equal(t, "# Boot\n\nDo the work.", string(bootMD))

	mcpJSON, _ := os.ReadFile(filepath.Join(res.BootDir, ".mcp.json"))
	assert.Contains(t, string(mcpJSON), "127.0.0.1:54321")

	// Spawn cwd = bootDir for claude (CwdBootDir).
	assert.Equal(t, res.BootDir, res.SpawnCwd)

	// ProjectDirArg pre-tokenized: --add-dir <projectDir>.
	assert.Equal(t, []string{"--add-dir", "/tmp/repo"}, res.ProjectDirArg)
}

// TestPlantBootDir_Codex covers the codex spec (AGENTS.md + boot.md +
// config.toml + auth.json + .mcp.json sidecar) and verifies the
// CODEX_HOME env amendment + 0o600 perms on auth.json/.mcp.json.
func TestPlantBootDir_Codex(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	// Isolate codex auth source so the test doesn't depend on the dev's
	// ~/.codex/auth.json. CODEX_HOME points at an empty dir → the lib's
	// readCodexAuthSource returns "" silently (the "user not logged in"
	// branch), which is what we want to assert against.
	t.Setenv("CODEX_HOME", t.TempDir())

	res, err := plantBootDir(plantParams{
		Provider:       "codex",
		Adapter:        provider.NewCodexAdapter(),
		TaskID:         "CW-CODEX-1",
		RunID:          0,
		AgentName:      "codex-agent",
		SystemPrompt:   "Codex persona.",
		KickoffContent: "kickoff",
		ProjectDir:     "/tmp/codex-repo",
		MCPLoopbackURL: "http://127.0.0.1:1/mcp",
	})
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(res.BootDir) }()

	// AGENTS.md (codex auto-load) + boot.md + config.toml (load-bearing
	// MCP config) + auth.json (placeholder when not logged in) + .mcp.json
	// (legacy claude-shape sidecar, not read by codex).
	for _, rel := range []string{"AGENTS.md", "boot.md", "config.toml", "auth.json", ".mcp.json"} {
		_, err := os.Stat(filepath.Join(res.BootDir, rel))
		assert.NoError(t, err, "expected %s under bootDir", rel)
	}

	// config.toml carries the load-bearing [mcp_servers.loopback] block.
	configTOML, err := os.ReadFile(filepath.Join(res.BootDir, "config.toml"))
	require.NoError(t, err)
	assert.Contains(t, string(configTOML), "[mcp_servers.loopback]")
	assert.Contains(t, string(configTOML), "127.0.0.1:1")

	// Sensitive files chmod'd to 0o600.
	for _, rel := range []string{"auth.json", ".mcp.json"} {
		st, err := os.Stat(filepath.Join(res.BootDir, rel))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), st.Mode().Perm(), "%s should be 0o600", rel)
	}

	// CODEX_HOME env amendment substituted to the real boot dir.
	require.NotEmpty(t, res.EnvAmendments)
	assert.Equal(t, "CODEX_HOME="+res.BootDir, res.EnvAmendments[0])

	// Codex's project-dir flag is --cd per the lib spec.
	assert.Equal(t, []string{"--cd", "/tmp/codex-repo"}, res.ProjectDirArg)
}

// TestPlantBootDir_Opencode covers the opencode spec — the only spec where
// CwdPreference=CwdProjectDir (boot dir is the *config* dir, cwd is
// project dir) AND EnvAmendments carries OPENCODE_CONFIG_DIR.
func TestPlantBootDir_Opencode(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	adapter := provider.NewOpencodeAdapter()
	adapter.Agent = "my-agent"

	res, err := plantBootDir(plantParams{
		Provider:       "opencode",
		Adapter:        adapter,
		TaskID:         "CW-OPENCODE-1",
		RunID:          1,
		AgentName:      "my-agent",
		SystemPrompt:   "Opencode persona.",
		KickoffContent: "kickoff",
		ProjectDir:     "/tmp/oc-repo",
		MCPLoopbackURL: "http://127.0.0.1:2/mcp",
	})
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(res.BootDir) }()

	// Opencode plants agents/<name>.md + agents.json + opencode.json + boot.md + .mcp.json.
	for _, rel := range []string{
		"agents/my-agent.md", "agents.json", "opencode.json", "boot.md", ".mcp.json",
	} {
		_, err := os.Stat(filepath.Join(res.BootDir, rel))
		assert.NoError(t, err, "expected %s under bootDir", rel)
	}

	// Spawn cwd = projectDir (CwdProjectDir).
	assert.Equal(t, "/tmp/oc-repo", res.SpawnCwd)

	// EnvAmendments expanded with the booted dir.
	require.NotEmpty(t, res.EnvAmendments)
	assert.Equal(t, "OPENCODE_CONFIG_DIR="+res.BootDir, res.EnvAmendments[0])

	// Project-dir flag is --dir per the lib spec.
	assert.Equal(t, []string{"--dir", "/tmp/oc-repo"}, res.ProjectDirArg)
}

// gemini/copilot bespoke-stub tests removed alongside the underlying
// provider.NewGeminiAdapter / provider.NewCopilotAdapter constructors,
// which were dropped in go-providers v0.12.0 (unused PTY adapter
// cleanup). factory.go now returns a permanent error for those
// providers; if either adapter is restored upstream, the corresponding
// unit tests should be re-added.

// TestPlantBootDir_TwoDirSeparation verifies that the boot dir lives under
// $TMPDIR (ephemeral) and is distinct from the workspace dir convention.
// The cross-app design's two-dir model is the architectural invariant
// being locked here.
func TestPlantBootDir_TwoDirSeparation(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	res, err := plantBootDir(plantParams{
		Provider: "claude",
		Adapter:  provider.NewClaudeAdapter(),
		TaskID:   "CW-TWO-DIR-1",
		RunID:    0,
	})
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(res.BootDir) }()

	// Boot dir lives under $TMPDIR (ephemeral on the test rig).
	assert.True(t, len(res.BootDir) > len(tmp), "bootDir under $TMPDIR")

	// Workspace dir is a separate concern — workspaceCreate puts it under
	// $HOME/.clockwork/workspaces/... The two-dir contract is enforced at
	// the Boot() seam, not here. This test verifies the bootDir-only side.
}
