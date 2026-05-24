package agent

import (
	"path/filepath"

	"github.com/hollis-labs/go-agent-runtime/turn"
)

// kickoffPayload returns the user-message body Boot fires (or planted as
// FirstTurnPayload) for the session's first turn.
//
// The convention is `Boot @./<file>` — claude resolves `@` references inline
// when the file is in cwd. For providers without `@` resolution, the bootdir
// layout plants the raw content into a different shape (see §"Per-provider
// boot dir layouts" in the design doc). The caller's Layout decides whether
// the payload here is the literal pointer or the raw content fallback.
//
// kickoffFileName is the file the kickoff message points at (typically
// "boot.md"). The `@./` prefix is provider-agnostic when the cwd is bootDir;
// opencode's cwd is the project dir but its config dir is bootDir, so the
// Layout's PlantContext handles that case differently.
func kickoffPayload(kickoffFileName string) string {
	if kickoffFileName == "" {
		return "Boot @./boot.md"
	}
	return "Boot @./" + kickoffFileName
}

// kickoffPayloadForBootDir returns a boot-file pointer that survives runtimes
// whose thread cwd is intentionally not the planted boot dir. JSON-RPC Codex
// binds cwd to the work root so tools operate in the right tree; the first
// turn therefore needs an absolute boot.md path.
func kickoffPayloadForBootDir(bootDir string) string {
	if bootDir == "" {
		return kickoffPayload("")
	}
	return "Boot @" + filepath.Join(bootDir, "boot.md")
}

// kickoffMarkdown returns the content planted into <bootDir>/boot.md. Read by
// the agent on its first turn (via the @./boot.md reference) and again post-
// compaction (since the file lives on disk). Keep concise: the systemPrompt
// already loaded via CLAUDE.md / AGENTS.md / agents/<name>.md carries the
// persona; this file is task-specific.
func kickoffMarkdown(opts Options, role string) string {
	if role == "" {
		role = opts.AgentProfile
	}
	taskRef := opts.TaskID
	if taskRef == "" {
		taskRef = "(no task_id)"
	}
	planRef := ""
	if opts.SessionMeta != nil {
		if v, ok := opts.SessionMeta["plan_id"]; ok && v != "" {
			planRef = v
		}
	}

	body := "# Boot\n\n"
	body += "You are a `" + role + "` agent dispatched by torque.\n\n"
	body += "**Task ID:** `" + taskRef + "`\n"
	if planRef != "" {
		body += "**Plan ID:** `" + planRef + "`\n"
	}
	if opts.Workdir != "" {
		body += "**Work root:** `" + opts.Workdir + "`\n"
	}
	if repoRoot := resolveRepoRoot(opts); repoRoot != "" {
		body += "**Repo root:** `" + repoRoot + "`\n"
	}
	body += "\n"
	if opts.Workdir != "" {
		body += "Workspace rule: run all shell commands and file edits from the work root above, or use `$TORQUE_WORK_ROOT`. If task metadata or MCP reports a different `working_dir`/`repo_root`, treat that as the source checkout pointer, not the writable workspace. Do not edit the source checkout when it differs from the work root.\n\n"
	}
	body += "Use the `torque_loopback` MCP server's task-scoped tools (no `task_id` parameter required) for self-task operations. Prefer them over `mcp__mux__torque_*` for the booted task.\n\n"
	if opts.TaskID != "" {
		body += "Your assigned task bundle is already planted under the boot dir's `tasks/` directory. Start with `tasks/README.md` and the task's `task.md`, `task.json`, and `process.md` files instead of calling MCP just to look up task, run, project, or session IDs. For opencode, resolve this under `$OPENCODE_CONFIG_DIR/tasks/` because the process cwd is the project dir.\n\n"
	}
	if opts.OneShotPrompt != "" {
		body += "## First turn\n\n"
		body += opts.OneShotPrompt + "\n"
	} else if opts.Description != "" {
		body += "## First turn\n\n"
		body += opts.Description + "\n"
	}
	if extra := opts.SystemPrompt; extra != "" {
		body += "\n## Task framing\n\n"
		body += extra + "\n"
	}
	return body
}

// encodeStreamJSONUserMessage wraps a plaintext user turn as the stream-json
// frame the claude-code (streaming-stdio) runtime requires.
//
// claude-code runs `claude -p --input-format stream-json`: every line on
// stdin must be one JSON object, NOT raw text. A bare line like
// `Boot @./boot.md` makes claude's parser reject the input
// ("JSON Parse error: Unexpected identifier"). The subprocess and pty
// runtimes take the plaintext verbatim and must NOT use this — only the
// streaming-stdio runtime needs the JSON envelope.
//
// The runtime appends the trailing newline (streaming-stdio SendInput frames
// one JSON object per call), so this returns the bare JSON object.
func encodeStreamJSONUserMessage(text string) ([]byte, error) {
	return turn.ClaudeStreamingUserFrame(text)
}
