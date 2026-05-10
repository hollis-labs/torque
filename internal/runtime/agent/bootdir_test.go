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

// TestPlantBootDir_Codex covers the codex spec (AGENTS.md + .mcp.json + boot.md).
func TestPlantBootDir_Codex(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

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

	// AGENTS.md (codex auto-load convention) + boot.md + .mcp.json.
	for _, rel := range []string{"AGENTS.md", "boot.md", ".mcp.json"} {
		_, err := os.Stat(filepath.Join(res.BootDir, rel))
		assert.NoError(t, err, "expected %s under bootDir", rel)
	}

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

// TestPlantBootDir_Claude_WithMux verifies that Mux config flows from
// plantParams through PlantContext into the rendered .mcp.json, emitting
// a second `mux` MCP server entry alongside the loopback (CW-20260510-0110).
// Spawned task agents then have access to Vanta + the portfolio-wide
// Mux-aggregated tool surface, not just the per-task loopback.
func TestPlantBootDir_Claude_WithMux(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	res, err := plantBootDir(plantParams{
		Provider:       "claude",
		Adapter:        provider.NewClaudeAdapter(),
		TaskID:         "CW-MUX-CLAUDE",
		RunID:          0,
		AgentName:      "default",
		SystemPrompt:   "you are an orchestrator",
		KickoffContent: "boot",
		ProjectDir:     "/tmp/repo",
		MCPLoopbackURL: "http://127.0.0.1:54321/mcp",
		MuxCommand:     "/usr/local/bin/mux",
		MuxArgs: []string{
			"mcp", "--proxy",
			"--servers", "vanta,clockwork,cerberus",
			"--token", "local-dev",
			"--scopes", "session.write,message.write",
		},
	})
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(res.BootDir) }()

	// .mcp.json carries BOTH loopback and mux entries.
	mcpRaw, err := os.ReadFile(filepath.Join(res.BootDir, ".mcp.json"))
	require.NoError(t, err)
	mcp := string(mcpRaw)

	assert.Contains(t, mcp, `"loopback"`, "loopback entry preserved")
	assert.Contains(t, mcp, `"http://127.0.0.1:54321/mcp"`, "loopback URL preserved")
	assert.Contains(t, mcp, `"mux"`, "mux entry added")
	assert.Contains(t, mcp, `"type": "stdio"`, "mux uses stdio transport")
	assert.Contains(t, mcp, `"command": "/usr/local/bin/mux"`, "mux command threaded through")
	assert.Contains(t, mcp, `"vanta,clockwork,cerberus"`, "mux args threaded through")
}

// TestPlantBootDir_Claude_NoMux_BackCompat pins that empty Mux fields in
// plantParams produce the same .mcp.json shape as before CW-20260510-0110.
// Existing callers that don't populate the new fields must see no behavior
// change.
func TestPlantBootDir_Claude_NoMux_BackCompat(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	res, err := plantBootDir(plantParams{
		Provider:       "claude",
		Adapter:        provider.NewClaudeAdapter(),
		TaskID:         "CW-NOMUX",
		RunID:          0,
		MCPLoopbackURL: "http://127.0.0.1:9000/mcp",
		// MuxCommand left empty.
	})
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(res.BootDir) }()

	mcpRaw, err := os.ReadFile(filepath.Join(res.BootDir, ".mcp.json"))
	require.NoError(t, err)
	mcp := string(mcpRaw)

	assert.Contains(t, mcp, `"loopback"`)
	assert.NotContains(t, mcp, `"mux"`, "no mux entry when MuxCommand empty")
}

// TestPlantBootDir_Opencode_WithMux verifies Mux threading into
// opencode.json's `mcp` block (separate from the .mcp.json sanity
// mirror). Pins the opencode-specific stdio shape: type:"local",
// command-as-array.
func TestPlantBootDir_Opencode_WithMux(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	adapter := provider.NewOpencodeAdapter()
	adapter.Agent = "executor"

	res, err := plantBootDir(plantParams{
		Provider:       "opencode",
		Adapter:        adapter,
		TaskID:         "CW-MUX-OPENCODE",
		RunID:          0,
		AgentName:      "executor",
		SystemPrompt:   "you orchestrate",
		KickoffContent: "boot",
		ProjectDir:     "/tmp/oc-repo",
		MCPLoopbackURL: "http://127.0.0.1:65500/mcp",
		MuxCommand:     "/usr/local/bin/mux",
		MuxArgs:        []string{"mcp", "--proxy"},
	})
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(res.BootDir) }()

	opencodeRaw, err := os.ReadFile(filepath.Join(res.BootDir, "opencode.json"))
	require.NoError(t, err)
	oc := string(opencodeRaw)

	assert.Contains(t, oc, `"loopback"`)
	assert.Contains(t, oc, `"type": "remote"`, "loopback uses remote transport (opencode)")
	assert.Contains(t, oc, `"mux"`)
	assert.Contains(t, oc, `"type": "local"`, "mux uses local transport (opencode's stdio keyword)")
	assert.Contains(t, oc, `"/usr/local/bin/mux"`, "mux command embedded in array")
}

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
