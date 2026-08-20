# FIX-004 — Fix docstring/order mismatches (Task, Epic, Sprint, Project)

**Phase:** 1 — Locked bug fixes
**Status:** done
**Depends on:** none
**Blocks:** ENT-TASK, ENT-EPIC, ENT-SPRINT, ENT-PROJECT
**Source:** ADR-0004 §3 (Sorting); audit "Cross-Cutting Findings §2
(Sorting)", "Proposed Direction G.2"

## Summary

Five list tools' docstrings misdescribe their own default order. Four are
in ADR-0004's scope (Task, Epic, Sprint, Project); the fifth (Artifact) is
explicitly out of scope for this ADR/task set — do not touch it here.

## Scope

| Tool | Docstring claims | Actual query order | Evidence |
|---|---|---|---|
| `torque_task_list` | "ordered updated_at DESC" | `priority ASC, created_at ASC` | `task_tools.go:83` vs `tasks.go:403` |
| `torque_epic_list` | "ordered updated_at DESC" | `created_at DESC` | `epic_tools.go:51` vs `epics.go:82` |
| `torque_sprint_list` | "ordered updated_at DESC" | `created_at DESC` | `sprint_tools.go:56` vs `sprints.go:97` |
| `torque_project_list` | "ordered updated_at DESC" | `name ASC` | `project_tools.go:24` vs `projects.go:90` |

For each: either fix the docstring to describe the query's actual order, or
change the query to match the documented order — pick per-tool based on
which order is more useful/intentional (don't default to "just fix the
docs" without a moment's thought; `updated_at DESC` is probably the more
useful default for an agent scanning recent activity, so changing the query
may be the better fix in some cases). `torque_task_search`'s docstring is
correct ("priority ASC, created_at ASC") and matches — use it as the
reference for what a correct docstring looks like.

## Acceptance criteria

- [x] All four docstrings accurately describe their tool's actual query
      order (verified by reading the query, not by assumption).
- [x] If any query order was changed instead of the docstring, confirm no
      existing test/consumer asserts the old order.
- [ ] Note: this task will likely be superseded/absorbed once PRIM-002
      (sort primitive) lands and each entity gets an explicit documented
      default — if PRIM-002 lands first for a given entity, fold the
      docstring fix into that entity's Phase 4 task instead of duplicating
      work. Coordinate ordering with whoever picks up PRIM-002.

## Out of scope

- `torque_artifact_list`'s identical bug ("newest first" docstring vs
  actual oldest-first query) — Artifact is out of ADR-0004's scope
  entirely. Do not fix it as part of this task; it belongs to Artifact's
  own future pass.

## Execution notes

- `torque_task_list` — **docstring fixed**, query untouched. `ListTasks`
  (`internal/persistence/sqlstore/tasks.go:403`, `ORDER BY priority ASC,
  created_at ASC`) already matches `SearchTasks`'s order exactly (same
  file, `ORDER BY priority ASC, created_at ASC`), and `torque_task_search`'s
  docstring (already correct, cited in this task as the reference) documents
  the same order. List and search are explicitly presented as siblings in
  both tools' docstrings ("prefer torque_task_search for free-text
  queries"/"prefer torque_task_list when filtering by structured fields");
  changing only list's order to `updated_at DESC` would make the two
  diverge for no reason. Priority-first ordering is also the more
  intentional choice for a task work queue (surfaces what to work on next)
  than recency. Fixed `internal/mcpadapter/task_tools.go`'s docstring to
  read "ordered priority ASC, created_at ASC".

- `torque_epic_list` — **query changed** to `updated_at DESC`
  (`internal/persistence/sqlstore/epics.go`, `ListEpics`, was `ORDER BY
  created_at DESC`). Epics do get real status transitions
  (`UpdateEpic`/`open<->closed`) that bump `updated_at`, so ordering by
  recency surfaces epics an agent recently touched/closed, which is more
  useful for "scanning recent activity" than pure creation order — and
  matches the docstring's original (and now correct) claim. Updated the
  `ListEpics` Go doc comment to say "ordered by updated_at DESC" to match.
  No test asserted `created_at`-order across multiple epics (existing
  `TestListEpics*` tests only assert on filtered single-result sets), so
  this was safe.

- `torque_sprint_list` — **query changed** to `updated_at DESC`
  (`internal/persistence/sqlstore/sprints.go`, `ListSprints`, was `ORDER BY
  created_at DESC`). Same reasoning as epics: sprints go through real
  lifecycle transitions (`UpdateSprint`, `TransitionSprint` —
  active/inactive/completed) that bump `updated_at`, so recency ordering
  is the more useful default for an agent checking sprint state. Updated
  the `ListSprints` Go doc comment to say "ordered by updated_at DESC" to
  match. No test asserted `created_at`-order across multiple sprints
  (existing `TestListSprints*` tests only assert on filtered
  single-result sets), so this was safe.

- `torque_project_list` — **docstring fixed**, query untouched
  (`internal/persistence/sqlstore/projects.go`, `ListProjects`, `ORDER BY
  name ASC`). Unlike epics/sprints, there is no `torque_project_update`
  MCP tool at all (`internal/mcpadapter/project_tools.go` only registers
  create/list/delete), so `updated_at` on projects essentially never
  changes via the MCP surface — recency ordering would rarely differ
  meaningfully from creation order and wouldn't reflect "activity" the way
  it does for epics/sprints. Projects are also a small, long-lived,
  human-browsed list (the docstring itself frames it as "project
  discovery", with no dedicated `project_get` tool), where alphabetical
  order is the more intentional/useful default for finding a project by
  name. Fixed `internal/mcpadapter/project_tools.go`'s docstring to read
  "ordered name ASC".

Files touched: `internal/mcpadapter/task_tools.go`,
`internal/mcpadapter/project_tools.go`,
`internal/persistence/sqlstore/epics.go`,
`internal/persistence/sqlstore/sprints.go`,
`tasks/phase-1-bugfixes/FIX-004-docstring-order-mismatches.md`.

`go build ./...` and `go test ./...` both pass with no changes needed. No
issues or blockers.
