# FIX-004 — Fix docstring/order mismatches (Task, Epic, Sprint, Project)

**Phase:** 1 — Locked bug fixes
**Status:** todo
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

- [ ] All four docstrings accurately describe their tool's actual query
      order (verified by reading the query, not by assumption).
- [ ] If any query order was changed instead of the docstring, confirm no
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
