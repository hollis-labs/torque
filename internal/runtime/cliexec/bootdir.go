package cliexec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// bootDirPrefix is the os.MkdirTemp pattern for per-task agent boot dirs.
// The pattern includes the task ID and run ID so manual inspection of
// $TMPDIR can identify which task a leaked dir came from.
const bootDirPrefix = "clockwork-cli-boot-"

// setupBootDir creates a per-task tempdir, plants the boot CLAUDE.md and
// .mcp.json (loopback URL), and returns the boot dir path. The boot dir is
// the spawned agent's cwd; the user's project dir is exposed via --add-dir
// so the agent reads/edits there without our scaffolding polluting it.
//
// Mirrors the mux/externshell.bootClaude and nanite/adapter-claude.PopulateSandbox
// patterns. Phase B's cliexec.Run regressed this convention by setting
// Workdir = projectDir directly; CW-20260427-0059 restores it.
//
// Caller is responsible for os.RemoveAll on the returned path.
func setupBootDir(taskID string, runID int64, systemPrompt, projectDir string, loopbackPort int) (string, error) {
	pattern := fmt.Sprintf("%s%s-r%d-*", bootDirPrefix, sanitizeTaskID(taskID), runID)
	bootDir, err := os.MkdirTemp("", pattern)
	if err != nil {
		return "", fmt.Errorf("create boot dir: %w", err)
	}

	claudeMD := composeBootClaudeMD(systemPrompt, projectDir, bootDir)
	if err := os.WriteFile(filepath.Join(bootDir, "CLAUDE.md"), []byte(claudeMD), 0o644); err != nil {
		_ = os.RemoveAll(bootDir)
		return "", fmt.Errorf("write boot CLAUDE.md: %w", err)
	}

	mcpJSON, err := buildMCPConfig(loopbackPort)
	if err != nil {
		_ = os.RemoveAll(bootDir)
		return "", fmt.Errorf("build .mcp.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(bootDir, ".mcp.json"), mcpJSON, 0o600); err != nil {
		_ = os.RemoveAll(bootDir)
		return "", fmt.Errorf("write .mcp.json: %w", err)
	}

	return bootDir, nil
}

// composeBootClaudeMD synthesizes the boot CLAUDE.md content. The agent's
// cwd is bootDir, so Claude auto-loads this file; project context is reached
// via the --add-dir flag passed at spawn time.
//
// Layout: caller-supplied systemPrompt first (agent persona / task framing),
// followed by project-pointer + loopback-tools-pointer + reload-after-compaction
// instructions. Mirrors the mux/externshell.bootClaude shape.
func composeBootClaudeMD(systemPrompt, projectDir, bootDir string) string {
	var b strings.Builder
	if trimmed := strings.TrimSpace(systemPrompt); trimmed != "" {
		b.WriteString(trimmed)
		b.WriteString("\n\n---\n\n")
	}
	fmt.Fprintf(&b, "**Project root:** %s\n", projectDir)
	fmt.Fprintf(&b, "To load project context: Read %s/CLAUDE.md\n\n", projectDir)
	b.WriteString("**Per-task MCP loopback:** the `clockwork_loopback` server is wired for this task.\n")
	b.WriteString("Use `clockwork_task_summary`, `clockwork_task_blocked`, `clockwork_task_review`,\n")
	b.WriteString("`clockwork_artifact_create`, `clockwork_comment_add`, `clockwork_task_subtodo_add`,\n")
	b.WriteString("`clockwork_task_subtodo_done` from this server. They target the current task implicitly\n")
	b.WriteString("— no `task_id` parameter is needed or accepted.\n\n")
	b.WriteString("Prefer the loopback tools over `mcp__mux__clockwork_*` for self-task operations:\n")
	b.WriteString("the loopback path is closure-bound to this task and avoids context bloat.\n\n")
	fmt.Fprintf(&b, "After context compaction, re-read this file: %s/CLAUDE.md\n", bootDir)
	return b.String()
}

// buildMCPConfig returns the bytes of a per-task .mcp.json declaring the
// loopback HTTP server at 127.0.0.1:<port>/mcp. Discovered by Claude as
// project-scope (cwd-based) at spawn time alongside the user's user-scope
// servers — no flag injection needed.
func buildMCPConfig(loopbackPort int) ([]byte, error) {
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"clockwork_loopback": map[string]any{
				"type": "http",
				"url":  fmt.Sprintf("http://127.0.0.1:%d/mcp", loopbackPort),
			},
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// sanitizeTaskID strips characters that break os.MkdirTemp pattern matching
// or look ugly in $TMPDIR listings. Task IDs are CW-YYYYMMDD-NNNN today,
// which is already safe; this is defensive against future ID shape changes.
func sanitizeTaskID(taskID string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, taskID)
}
