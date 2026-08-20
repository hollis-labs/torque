# MCP Data-Entity Ergonomics — Task Index

Source: [ADR-0004](../docs/adr/0004-mcp-data-entity-agent-ergonomics.md) ·
[MCP service-layer audit](../docs/architecture/mcp-service-layer-audit.md)

Scope: Task, Subtodo, Comment, Project, Epic, Sprint, Issue, Plan. Explicitly
**out of scope**: Run, Session, Scheduler, Settings, Model, Broker,
Collection, Artifact, Template (per ADR-0004; own future pass).

This index is the single source of truth for status. Update the **Status**
column as work lands — don't let task-file status and this table drift.

Status values: `todo` · `blocked` · `in-progress` · `done` · `skipped`

## How to use this

1. Phase 0 must resolve before any task that depends on it (see Depends-on
   column). Phases 1 and 3 have no dependencies on Phase 0 and can start
   immediately/in parallel.
2. Within a phase, tasks with no unmet `Depends on` are parallelizable.
3. Each task file is self-contained: summary, exact scope, file:line
   citations back to the ADR/audit, acceptance criteria, explicit
   out-of-scope notes. An orchestrator should not need to re-read the ADR to
   execute a task, only to resolve ambiguity.

## Phase 0 — Decisions (blocking prerequisites, no code)

| ID | Title | Status | Depends on | Blocks |
|---|---|---|---|---|
| [DEC-001](phase-0-decisions/DEC-001-pagination-strategy.md) | Pagination strategy: cursor vs offset | done (decided: cursor/keyset, all 7 list tools) | — | PRIM-001 |
| [DEC-002](phase-0-decisions/DEC-002-feature-flag-gating.md) | Feature-flag gating vs always-on for Project/Epic/Sprint tools | done (decided: keep current gating) | — | ENT-PROJECT, ENT-EPIC, ENT-SPRINT |
| [DEC-003](phase-0-decisions/DEC-003-depends-on-normalization.md) | `depends_on` normalization: join table vs documented JSON blob | done (decided: join table, split into FK-003) | — | ENT-TASK, FK-003 |

## Phase 1 — Locked bug fixes (independent, parallelizable, no Phase 0 dependency)

| ID | Title | Status | Depends on | Blocks |
|---|---|---|---|---|
| [FIX-001](phase-1-bugfixes/FIX-001-task-create-update-field-detection.md) | Task create/update field-detection + required fixes | done | — | ENT-TASK |
| [FIX-002](phase-1-bugfixes/FIX-002-subtodo-id-autogen.md) | Auto-generate Subtodo IDs | done | — | ENT-SUBTODO, ENT-TASK |
| [FIX-003](phase-1-bugfixes/FIX-003-epic-status-vocabulary-bug.md) | Fix Epic status vocabulary bug | done | — | ENT-EPIC |
| [FIX-004](phase-1-bugfixes/FIX-004-docstring-order-mismatches.md) | Fix docstring/order mismatches (Task, Epic, Sprint, Project) | done | — | ENT-TASK, ENT-EPIC, ENT-SPRINT, ENT-PROJECT |
| [FIX-005](phase-1-bugfixes/FIX-005-delete-cleanup-transaction-atomicity.md) | Wrap Sprint/Project/Epic delete-time task cleanup in a transaction | done | — | — |

## Phase 2 — Shared primitives

| ID | Title | Status | Depends on | Blocks |
|---|---|---|---|---|
| [PRIM-001](phase-2-primitives/PRIM-001-pagination-envelope.md) | Pagination + response envelope primitive | done (cursor/keyset, applied to Task) | DEC-001 | ENT-TASK, ENT-COMMENT, ENT-PROJECT, ENT-EPIC, ENT-SPRINT, ENT-ISSUE, ENT-PLAN |
| [PRIM-002](phase-2-primitives/PRIM-002-sort-primitive.md) | Sort primitive (`sort_by`/`sort_dir`) | done (applied to Task, alongside PRIM-001) | — | ENT-TASK, ENT-COMMENT, ENT-PROJECT, ENT-EPIC, ENT-SPRINT, ENT-ISSUE, ENT-PLAN |
| [PRIM-003](phase-2-primitives/PRIM-003-bulk-operation-pattern.md) | Generalized bulk-operation pattern | done (applied to Task) | — | ENT-TASK, ENT-EPIC, ENT-SPRINT, ENT-ISSUE |
| [PRIM-004](phase-2-primitives/PRIM-004-archive-primitive.md) | Archive primitive (`archived_at` + archive/unarchive) | done (Project/Epic/Sprint; Issue/Plan scope deferred) | — | ENT-PROJECT, ENT-EPIC, ENT-SPRINT |

## Phase 3 — Real FK migration (independent track, parallel to Phase 2)

| ID | Title | Status | Depends on | Blocks |
|---|---|---|---|---|
| [FK-001](phase-3-fk-migration/FK-001-orphan-backfill-cleanup.md) | Backfill/cleanup orphaned sprint_id/project_id/epic_id refs | done | — | FK-002 |
| [FK-002](phase-3-fk-migration/FK-002-promote-real-foreign-keys.md) | Promote sprint_id/project_id/epic_id to real FKs | done (migration 028) | FK-001 | — |
| [FK-003](phase-3-fk-migration/FK-003-depends-on-join-table.md) | Normalize `depends_on` into a real join table (fixes scheduler deadlock bug) | done (migration 029, must run after FK-002's 028) | DEC-003 | — |

## Phase 4 — Per-entity rollout

| ID | Title | Status | Depends on | Blocks |
|---|---|---|---|---|
| [ENT-TASK](phase-4-entities/ENT-TASK.md) | Task: filters, bulk ops, create-field expansion, transition comment | done | FIX-001, FIX-002, FIX-004, PRIM-001, PRIM-002, PRIM-003, DEC-003 | SWEEP-001 |
| [ENT-SUBTODO](phase-4-entities/ENT-SUBTODO.md) | Subtodo: bulk_add | done | FIX-002 | SWEEP-001 |
| [ENT-COMMENT](phase-4-entities/ENT-COMMENT.md) | Comment: update/delete, pagination/sort/filter, bulk_add | done | PRIM-001, PRIM-002 | SWEEP-001 |
| [ENT-PROJECT](phase-4-entities/ENT-PROJECT.md) | Project: get/update (new), create fields, list wiring, archive | done | FIX-004, PRIM-001, PRIM-002, PRIM-004, DEC-002 | SWEEP-001 |
| [ENT-EPIC](phase-4-entities/ENT-EPIC.md) | Epic: merge list+search, settable priority, archive, bulk_update | done | FIX-003, FIX-004, PRIM-001, PRIM-002, PRIM-003, PRIM-004, DEC-002 | SWEEP-001 |
| [ENT-SPRINT](phase-4-entities/ENT-SPRINT.md) | Sprint: list sort/cursor, archive, bulk_update, budget filter | done | FIX-004, PRIM-001, PRIM-002, PRIM-003, PRIM-004, DEC-002 | SWEEP-001 |
| [ENT-ISSUE](phase-4-entities/ENT-ISSUE.md) | Issue: merge list+search, DB-level limit, status filter, bulk ops | done | PRIM-001, PRIM-002, PRIM-003 | SWEEP-001 |
| [ENT-PLAN](phase-4-entities/ENT-PLAN.md) | Plan: dedicated list tool, update/delete, list_children default limit | done | PRIM-001, PRIM-002 | SWEEP-001 |

## Phase 5 — Consistency sweep

| ID | Title | Status | Depends on | Blocks |
|---|---|---|---|---|
| [SWEEP-001](phase-5-consistency/SWEEP-001-envelope-and-error-taxonomy-audit.md) | Envelope + error-taxonomy consistency pass across all 8 entities | done (found + fixed presence-detection regressions in Sprint/Epic, not_found mapping bug in Subtodo; spun out FIX-006) | all Phase 4 tasks | — |
| [FIX-006](phase-5-consistency/FIX-006-task-list-search-merge.md) | Merge `torque_task_search` into `torque_task_list` (never actually done despite ADR-0004 scope) | done | — | — |
| [FIX-007](phase-5-consistency/FIX-007-task-write-transaction-atomicity.md) | Wrap TaskService.Create/Update's multi-write paths in a transaction | done | — | — |

## Explicitly not scheduled here

Per ADR-0004 Consequences, these are follow-ups the ADR names but
deliberately does not scope:

- **Opt-in lifecycle mechanism** (which execution/lifecycle fields become
  toggleable) — no follow-up ADR written yet.
- **MCP discoverability/schema reference layer** — deferred until the
  surface above is locked, so it doesn't need revision mid-implementation.
- **Run/Session/Scheduler/Settings/Model/Broker/Collection/Artifact/Template**
  — out of ADR-0004's scope entirely; own future audit-derived pass.

Do not create tasks against these until a follow-up ADR exists.
