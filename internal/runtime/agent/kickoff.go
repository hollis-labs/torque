package agent

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
	body += "You are a `" + role + "` agent dispatched by clockwork.\n\n"
	body += "**Task ID:** `" + taskRef + "`\n"
	if planRef != "" {
		body += "**Plan ID:** `" + planRef + "`\n"
	}
	if opts.Workdir != "" {
		body += "**Project root:** `" + opts.Workdir + "`\n"
	}
	body += "\n"
	body += "Use the `clockwork_loopback` MCP server's task-scoped tools (no `task_id` parameter required) for self-task operations. Prefer them over `mcp__mux__clockwork_*` for the booted task.\n\n"
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
