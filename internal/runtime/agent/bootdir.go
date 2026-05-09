package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/go-providers/provider"
)

// bootDirPrefix is the os.MkdirTemp pattern for per-task agent boot dirs.
// Per cross-app design §5: cross-app forensic discoverability — running
// `find /var/folders -name "clockwork-boot-claude-*"` surfaces all clockwork
// boot dirs from all running daemons.
const bootDirPrefix = "clockwork-boot-"

// bootDirResult captures the per-task boot dir layout side-effects: the
// dir path itself, the env amendments to apply (after template substitution),
// the spawn cwd preference (project dir for opencode, boot dir for claude/
// codex), and the spawn-args fragment for granting project access (e.g.
// "--add-dir <projectDir>").
type bootDirResult struct {
	BootDir       string
	EnvAmendments []string
	SpawnCwd      string
	ProjectDirArg []string // pre-tokenized; appended to BuildArgs at spawn time
	KickoffFile   string   // relative path under BootDir; "boot.md" for the lib's convention
}

// plantParams aggregates the inputs to plantBootDir. Caller (Boot) builds
// this from Options + the resolved profile + the loopback URL.
type plantParams struct {
	Provider       string
	Adapter        provider.CLIAdapter
	TaskID         string
	RunID          int64
	AgentName      string // opencode --agent value; profile name today
	SystemPrompt   string
	KickoffContent string
	ProjectDir     string
	MCPLoopbackURL string
}

// plantBootDir materializes the per-task boot dir for the spawn. Returns
// the layout side-effects on success; caller plumbs them into the
// agentsessions.StartRequest. On error, any partially-created bootDir is
// cleaned via os.RemoveAll before the function returns.
//
// Strategy (per lib-tier status §"BootDirSpec coverage"):
//
//   - Adapters with a concrete `BootDirSpec().PlantedFiles` (claude, codex,
//     opencode) — iterate the lib's spec; let it own the convention.
//   - Adapters with a stub spec (gemini, copilot, aider, junie, kiro, qwen)
//     — fail with ErrBootDirNotImplemented; per-provider bespoke planting
//     lives in bootdir_<provider>.go and is dispatched by this function
//     when the file is filled in. Today gemini/copilot are stubs that fail;
//     the others aren't reachable from clockwork (factory rejects them).
func plantBootDir(p plantParams) (*bootDirResult, error) {
	switch p.Provider {
	case "gemini":
		return plantGeminiBootDir(p)
	case "copilot":
		return plantCopilotBootDir(p)
	}

	// Adapters with a concrete BootDirSpec: claude, codex, opencode.
	bp, ok := p.Adapter.(provider.BootDirProvider)
	if !ok {
		return nil, fmt.Errorf("agent: adapter %q does not implement BootDirProvider", p.Adapter.Name())
	}
	spec := bp.BootDirSpec()
	if len(spec.PlantedFiles) == 0 {
		// Stub spec leaked through (gemini/copilot caught above). The lib's
		// Notes documents what's missing; surface it for forensic value.
		return nil, fmt.Errorf("%w: provider=%s notes=%s", ErrBootDirNotImplemented, p.Provider, spec.Notes)
	}

	bootDir, err := makeBootDir(p.Provider, p.TaskID, p.RunID)
	if err != nil {
		return nil, err
	}

	plantCtx := provider.PlantContext{
		SystemPrompt:   p.SystemPrompt,
		BootContent:    p.KickoffContent,
		AgentName:      p.AgentName,
		MCPLoopbackURL: p.MCPLoopbackURL,
		ProjectDir:     p.ProjectDir,
		// BootDir is the gate for go-providers v0.8.2's per-bootdir trust
		// seeding (CW-20260508-0007): the claude .claude/settings.json
		// Render closure side-effects on ~/.claude.json's projects map
		// only when BootDir != "". Without this, PTY-mode claude stalls
		// on the first-run workspace trust dialog.
		BootDir: bootDir,
	}

	for _, pf := range spec.PlantedFiles {
		path := filepath.Join(bootDir, pf.RelPath)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			_ = os.RemoveAll(bootDir)
			return nil, fmt.Errorf("plant %s: mkdir: %w", pf.RelPath, err)
		}
		if pf.Render == nil {
			// Lib explicitly permits a nil Render; treat as "skip without
			// failing." Forensic-friendly to leave a placeholder so the
			// post-mortem reader knows the file was expected.
			if err := os.WriteFile(path, nil, 0o644); err != nil {
				_ = os.RemoveAll(bootDir)
				return nil, fmt.Errorf("plant %s: write empty: %w", pf.RelPath, err)
			}
			continue
		}
		content, err := pf.Render(plantCtx)
		if err != nil {
			_ = os.RemoveAll(bootDir)
			return nil, fmt.Errorf("plant %s: render: %w", pf.RelPath, err)
		}
		mode := os.FileMode(0o644)
		if pf.RelPath == ".mcp.json" {
			// Loopback URL is per-task secret-ish (any process that can
			// read it could impersonate the agent against the loopback).
			// Match cliexec's convention.
			mode = 0o600
		}
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			_ = os.RemoveAll(bootDir)
			return nil, fmt.Errorf("plant %s: write: %w", pf.RelPath, err)
		}
	}

	return &bootDirResult{
		BootDir:       bootDir,
		EnvAmendments: substituteTemplates(spec.EnvAmendments, bootDir, p.ProjectDir),
		SpawnCwd:      spec.SpawnWorkdir(bootDir, p.ProjectDir),
		ProjectDirArg: tokenizeArg(spec.ProjectDirArg, bootDir, p.ProjectDir),
		KickoffFile:   "boot.md",
	}, nil
}

// makeBootDir creates the per-task tempdir with the cross-app naming
// convention. Caller responsible for os.RemoveAll on the returned path.
func makeBootDir(providerName, taskID string, runID int64) (string, error) {
	pattern := fmt.Sprintf("%s%s-%s-r%d-*", bootDirPrefix, providerName, sanitizeTaskID(taskID), runID)
	bootDir, err := os.MkdirTemp("", pattern)
	if err != nil {
		return "", fmt.Errorf("create boot dir: %w", err)
	}
	return bootDir, nil
}

// substituteTemplates expands {{.BootDir}} / {{.ProjectDir}} in each input
// string. The lib's spec uses these placeholders so apps own the runtime
// path values without round-tripping through text/template.
func substituteTemplates(in []string, bootDir, projectDir string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.ReplaceAll(s, "{{.BootDir}}", bootDir)
		s = strings.ReplaceAll(s, "{{.ProjectDir}}", projectDir)
		out = append(out, s)
	}
	return out
}

// tokenizeArg expands the lib's ProjectDirArg template (e.g.
// "--add-dir {{.ProjectDir}}") into a pre-split arg slice ready for
// BuildArgs.append. Empty input → nil; whitespace-only → nil.
func tokenizeArg(template, bootDir, projectDir string) []string {
	if template == "" || projectDir == "" {
		return nil
	}
	expanded := strings.ReplaceAll(template, "{{.BootDir}}", bootDir)
	expanded = strings.ReplaceAll(expanded, "{{.ProjectDir}}", projectDir)
	return strings.Fields(expanded)
}

// sanitizeTaskID strips characters that break os.MkdirTemp pattern matching
// or look ugly in $TMPDIR listings. Forked from cliexec/bootdir.go.
func sanitizeTaskID(taskID string) string {
	if taskID == "" {
		return "no-task"
	}
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
