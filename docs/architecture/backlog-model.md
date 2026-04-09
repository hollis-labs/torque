---
intent: system_doc
audience: humans
---

# Backlog Model — v0.4

## Purpose

The backlog is a **low-friction capture layer** between a raw idea and an active task.
Ideas should be cheap to record and never lost. Commitment to execution (promotion)
is a deliberate, separate step.

---

## Hierarchy

```
Idea / observation
  → Backlog item  (.agentrc/backlog/BACKLOG-YYYYMMDD-NNN.md)
      → Active task  (.agentrc/tasks/TASK-YYYYMMDD-NNN.md)
          → Sprint / iteration execution
```

A **backlog item** is informal and exploratory.
An **active task** is committed and tracked in the iteration loop.

---

## Backlog Item Format

```yaml
---
id: BACKLOG-YYYYMMDD-NNN
title: "Short description of the idea"
status: captured | promoting | promoted | dropped
priority: A | B | C
project: volon
tags: []
context: dev
created_at: YYYY-MM-DD
updated_at: YYYY-MM-DD
promoted_to: null          # set to TASK-ID when promoted
---

## Description

One paragraph: what is this and why does it matter?

## Notes

Details, constraints, open questions — to be refined before promoting.
```

---

## Status Lifecycle

| Transition | Trigger | Who |
|---|---|---|
| → `captured` | `/backlog-task "Title"` | Agent or human |
| `captured` → `promoting` | Manual edit or `/backlog-task promote` | Human |
| `promoting` → `promoted` | `/backlog-task promote` succeeds | Agent |
| `captured/promoting` → `dropped` | Manual edit | Human |

**Rule: promotion is always explicit.** ~~No auto-promotion from backlog → task.~~

**Priority-Based Auto-Promotion (v0.5+):**
- Items with **A priority (urgent)** get `auto_promote_opt_in=true` and are eligible for automatic promotion to sprints
- Items with **B/C priority or unspecified priority** stay in backlog until manually promoted
- Priority mapping: P0 → A (urgent), P1 → B, P2 → C
- This enables the scheduler to automatically pick up urgent backlog items for execution

---

## Priority Behavior (v0.5+)

Default priority behavior follows the rule: **unspecified = backlog, urgent = sprint**

| Priority | Default Destination | Auto-Promote | Description |
|----------|-------------------|--------------|-------------|
| A (urgent/P0) | Sprint | `auto_promote_opt_in=true` | High priority items eligible for automatic execution |
| B (default/P1) | Backlog | `auto_promote_opt_in=false` | Standard priority items require manual promotion |
| C (low/P2) | Backlog | `auto_promote_opt_in=false` | Low priority items require manual promotion |
| Unspecified | Backlog (defaults to B) | `auto_promote_opt_in=false` | Items default to B priority |

This behavior is configured in `agentrc.yaml`:

```yaml
backlog:
  priority_behavior:
    default_priority: "B"           # Items without priority specification
    urgent_auto_promote: true       # A priority items get auto_promote_opt_in=true
```

## Promotion Rules

Before promoting, verify:
1. Title is clear and actionable
2. Description explains the "why" (not just "what")
3. Priority is set appropriately
4. Tags and context are correct

On promotion, `/backlog-task promote <id>` will:
1. Read the backlog item
2. Create an active task via `/task-create` with matching fields
3. Update the backlog item: `status: promoted`, `promoted_to: <TASK-ID>`

---

## Sprint & Iteration Identifiers (v0.4)

Active tasks support two optional scheduling fields:

```yaml
sprint_id: "sprint-2026-01"     # optional — sprint this task belongs to
iteration_id: 24                 # optional — volon iteration that created this task
```

**Sprint rules (from execution brief):**
- A sprint *can* invoke an iteration loop
- An iteration loop *cannot* invoke a sprint
- Sprint scope is larger than an iteration

These fields are set at task creation time and are informational — they do not
affect task selection or loop behavior in v0.4.

---

## Using the Backlog Skill

```bash
# Capture
/backlog-task "Idea title"
/backlog-task "Investigate caching options" priority=B tags=perf,research

# Review
/backlog-task list
/backlog-task list status=captured

# Promote
/backlog-task promote BACKLOG-20260221-001
```

See `plugins/backlog/skills/backlog-task/SKILL.md` for full protocol.

---

## Volon CLI Backlog Commands

For deterministic automation (and to keep history outside the chat window), the Volon CLI now exposes the same operations:

```
volon backlog list [--status ...] [--priority ...] [--tag ...] [--limit N]
volon backlog show <BACKLOG-ID>
volon backlog promote <BACKLOG-ID> [--title ...] [--priority ...] [--tags ...] [--type ...] [--sprint <slug>]
```

- `list` reads `.agentrc/backlog/BACKLOG-*.md`, filters by status/priority/tag substring, and prints a tabular queue sorted by ID.
- `show` prints the exact markdown file so you can review/edit outside of a skill invocation.
- `promote` creates a new `TASK-YYYYMMDD-###.md` (setting `promoted_from` + optional `sprint_id` via `--sprint sprint-YYYY-MM`), updates the backlog file to `status: promoted` / `promoted_to: TASK-…`, and refuses to run if the entry is already promoted or dropped.

Use either the slash command (`/backlog-task promote ...`) or the CLI — both mutate the same markdown files, so pick whichever fits the workflow you’re running.

---

## Evidence

- Created: 2026-02-21 (EPIC-004, TASK-084, iteration 24)
- Source: `volon_v0.4_execution_brief.md` — EPIC-004 scope
- Reviewed against: `docs/04_task-model.md`, `.agentrc/pcc/02_conventions.md`
