package agent

import (
	"log"
	"path/filepath"

	"github.com/hollis-labs/agentkit/agentruntime/turn"
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

// firstTurnKickoff is the wrapper path's kickoff turn for a runtime that runs
// in workdir. Where that is the planted boot dir, `Boot @./boot.md` points at
// the planted file. OpenCode runs in the project dir, with the boot dir as
// its OPENCODE_CONFIG_DIR, so the pointer resolves against the wrong
// directory and the agent never sees its briefing (CW-20261001-0104); such a
// runtime gets boot.md's content as the turn instead. That is what opencode
// run's first turn carried before agentkit v0.13.0, when every turn ran the
// prepared argv and its prompt was the planted boot content.
func firstTurnKickoff(bootDir, workdir, kickoffMD string) string {
	if bootDir == "" || kickoffMD == "" || filepath.Clean(bootDir) == filepath.Clean(workdir) {
		return kickoffPayload("")
	}
	return kickoffMD
}

// maxArgvKickoff bounds the boot.md content a turn carries as one
// command-line argument. Linux refuses an execve argument longer than
// MAX_ARG_STRLEN (128 KiB) with E2BIG, so the turn would never launch; the
// bound leaves room below that.
const maxArgvKickoff = 100 << 10

// argvSafeTurn returns turn, unless it is boot.md's content (kickoffMD) for
// a runtime that passes each turn's prompt as an argument (subprocess-per-
// turn: opencode run) and is longer than maxArgvKickoff. Such a turn points
// at the planted boot.md instead, by absolute path because the runtime runs
// in the project dir, where `@./boot.md` names nothing (CW-20261001-0121).
// A runtime that takes the turn over stdin or HTTP keeps the content.
func argvSafeTurn(sessID, turn, bootDir, kickoffMD string, kind RuntimeKind) string {
	if kind != RuntimeKindSubprocess || bootDir == "" || kickoffMD == "" || turn != kickoffMD || len(turn) <= maxArgvKickoff {
		return turn
	}
	pointer := kickoffPayloadForBootDir(bootDir)
	log.Printf("agent.Boot: session=%s boot.md is %d bytes, over the %d-byte bound for a command-line argument; the first turn is %q instead of its content", sessID, len(turn), maxArgvKickoff, pointer)
	return pointer
}

// oneShotTurn is the wrapper path's one-shot turn: the caller's prompt (the
// one-shot prompt or the task description), or the kickoff when there is
// none. A runtime that does not run in its boot dir gets boot.md's content
// even with a prompt: the content already carries the prompt under "First
// turn", plus the briefing the agent cannot reach through the pointer. That
// is what opencode run's one-shot argv carried before agentkit v0.13.0.
// Claude one-shot stays the prompt alone, as before.
func oneShotTurn(prompt, bootDir, workdir, kickoffMD string) string {
	kickoff := firstTurnKickoff(bootDir, workdir, kickoffMD)
	if prompt == "" || (kickoffMD != "" && kickoff == kickoffMD) {
		return kickoff
	}
	return prompt
}

// kickoffMarkdown returns the content planted into <bootDir>/boot.md. Read by
// the agent on its first turn (via the @./boot.md reference) and again post-
// compaction (since the file lives on disk). Keep concise: the systemPrompt
// already loaded via CLAUDE.md / AGENTS.md / agents/<name>.md carries the
// persona; this file is task-specific. muxOmitsTorque says the session's
// mux server carries no torque tools (Dependencies.MuxOmitsTorque).
func kickoffMarkdown(opts Options, role string, muxOmitsTorque bool) string {
	body := kickoffHeader(opts, role)
	body += kickoffLoopbackLine(role, muxOmitsTorque)
	if opts.TaskID != "" {
		body += "Your assigned task bundle is already planted under the boot dir's `tasks/` directory. Start with `tasks/README.md` and the task's `task.md`, `task.json`, and `process.md` files instead of calling MCP just to look up task, run, project, or session IDs. For opencode, resolve this under `$OPENCODE_CONFIG_DIR/tasks/` because the process cwd is the project dir.\n\n"
	}
	return body + kickoffFirstTurn(opts)
}

// kickoffLoopbackTools points the agent at the per-task loopback's tools. The
// mux preference is conditional: a Claude worker's session has no mux server
// unless its profile names mux_servers (CW-20261001-0226).
const kickoffLoopbackTools = "Use the `loopback` MCP server's task-scoped tools (no `task_id` parameter required) for self-task operations. If your session also has a `mux` server, prefer them over its `mcp__mux__torque_*` tools for the booted task.\n\n"

// kickoffLoopbackFullSurface is kickoffLoopbackTools for an orchestrator-class
// role, whose loopback carries the full Torque surface: unlike a worker's, it
// is not bound to one task, so every call names the task it acts on.
const kickoffLoopbackFullSurface = "Use the `loopback` MCP server's Torque tools (the full surface: pass the `task_id` of the task each call acts on). If your session also has a `mux` server, prefer them over its `mcp__mux__torque_*` tools.\n\n"

// kickoffLoopbackFullSurfaceOnly is kickoffLoopbackFullSurface while Torque
// write-protects its state: mux serves no `torque` tools, so the loopback
// carries the same `torque_*` tools mux did.
const kickoffLoopbackFullSurfaceOnly = "Use the `loopback` MCP server's Torque tools (the full surface: pass the `task_id` of the task each call acts on). They are this session's only Torque tools: while Torque write-protects its state, the `mux` server carries no `torque` tools, and the `torque_*` tools it carried are on the loopback.\n\n"

// kickoffLoopbackLine picks the kickoff's loopback paragraph: the full
// surface for an orchestrator-class role (orchestrator, planner,
// reviewer-end-agent), the task-scoped subset for everything else, and, while
// mux serves no torque tools, the variant that says the loopback is the only
// Torque surface.
func kickoffLoopbackLine(role string, muxOmitsTorque bool) string {
	switch {
	case isOrchestratorClassRoleForPrompt(role) && muxOmitsTorque:
		return kickoffLoopbackFullSurfaceOnly
	case isOrchestratorClassRoleForPrompt(role):
		return kickoffLoopbackFullSurface
	case muxOmitsTorque:
		return kickoffLoopbackOnly
	}
	return kickoffLoopbackTools
}

// kickoffLoopbackOnly replaces kickoffLoopbackTools while Torque
// write-protects its state: mux then serves no torque tools, so the loopback
// is the session's only way to reach Torque.
const kickoffLoopbackOnly = "Use the `loopback` MCP server's task-scoped tools (no `task_id` parameter required) for self-task operations. They are this session's only Torque tools: while Torque write-protects its state, the `mux` server carries no `torque` tools.\n\n"

// kickoffHeader is the kickoff's opening: who the agent is, the task and
// plan it serves, and the workspace it writes in.
func kickoffHeader(opts Options, role string) string {
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
	return body
}

// kickoffFirstTurn is the kickoff's close: the first turn's instruction and
// any task framing the caller added.
func kickoffFirstTurn(opts Options) string {
	body := ""
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
