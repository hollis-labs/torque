---
intent: system_doc
audience: humans
---

# Loop Runner (tasks-driven) — v0.1 (Orchestrator Mode)

## Default execution role
By default, the active session should behave as an **Orchestrator** (see `docs/08_orchestrator.md`):
- canonical state coordinator
- delegates read-only sub-agents optionally
- drives tasks/workflows
- finalizes with app-owned bootstrap/cache refresh

## Pause/Resume
- Use `/pause-task restart "<note>"` to externalize state and restart cleanly.
- Start a new session and run `/resume-task "<optional note>"`.

See: `docs/10_pause_resume.md`

## Manual run (fresh session)
1. Read `.agentrc/bootstrap.md`
2. Run tasks-driven loop (execute up to K tasks)
3. Write run log
4. Finalize iteration (refresh app-owned bootstrap/cache artifacts)

## Copy/paste loop prompt (includes bootstrap finalize)
```markdown
You are operating in a Volon-managed repository in **Orchestrator Mode**.

Do not rely on prior chat context.

Rules:
- You are the canonical state coordinator. Sub-agents (if enabled) are bounded helpers and should avoid direct cache edits.
- Ground truth: volon.yaml, bootstrap, PCC, tasks, logs.

0) If the user requests a pause: run /pause-task (mode as requested) and stop.

1) Read `.agentrc/bootstrap.md` (if present).
2) Execute a tasks-driven loop from `.agentrc/tasks/`:
   - select next todo by priority (A>B>C), oldest first
   - set doing → execute → verify → done/blocked/paused
   - append Updates in task file
   - write run log under `.agentrc/logs/`
   - stop after <K> tasks or no todos

3) Finalize iteration:
   - run /commit-task (if git.auto_commit: true and commit_mode: iteration)
   - refresh app-owned bootstrap/cache artifacts
   - ensure `.agentrc/bootstrap.md` and history copy exist if cache is enabled

End with:
DONE
```
