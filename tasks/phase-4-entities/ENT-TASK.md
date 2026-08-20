# ENT-TASK — Task: filters, bulk ops, create-field expansion, transition comment

**Phase:** 4 — Per-entity rollout
**Status:** done
**Depends on:** FIX-001, FIX-002, FIX-004, PRIM-001, PRIM-002, PRIM-003,
DEC-003
**Blocks:** SWEEP-001
**Source:** ADR-0004 §3, §4, §5 (Task)

## Summary

Task is "closest to done already" per the ADR and already served as the
reference implementation for PRIM-001/PRIM-002/PRIM-003. This task rolls
the remaining Task-specific gaps on top of that foundation: filter closure,
bulk_tag, create-field expansion, and the transition-comment convenience.

## Scope

**Filters** (audit "Filter/facet coverage"):
- Add `Statuses` (multi-status OR filter) to `torque_task_list` — already
  exists at the store layer (`TaskFilter.Statuses`) and on HTTP
  (`httpserver/tasks.go:420`), just needs MCP wiring. This is the one
  filter gap called out as highest-traffic-entity priority.
- Add `created_at`/`updated_at` range filters — currently no filter support
  at any layer for these.
- Add budget/duration filters: `cost_budget`, `token_budget`,
  `max_duration_ms`, `max_retries` — also currently no filter support at
  any layer. These enable "show me over-budget tasks" style queries.
- Add `agent_profile`, `launch_profile` filters (currently no filter
  support at any layer either).

**Bulk** (depends on PRIM-003):
- `bulk_update`, `bulk_delete`, `bulk_tag` using the PRIM-003 pattern.
  (`bulk_transition` already exists via `TaskService.BulkTransition` —
  don't rebuild it, just confirm it still matches the PRIM-003 response
  shape for consistency.)

**Create-field expansion** — `TaskCreateInput` already accepts these at the
service layer but `torque_task_create`'s MCP schema doesn't expose them:
`tools`, `on_review`, `files`, `cost_budget`, `max_retries`, `permissions`,
`environment`, `max_duration_ms`, `token_budget`, `escalation_chain`,
`quality_gates`, `deliverables`, `blocked_reason`, and an optional initial
`subtodos[]` seed list (create the task and its subtodos in one call).

**Transition comment**: `transition` optionally takes a `comment` string,
atomically posting a comment with the status change (single call instead of
transition + separate comment add).

**`depends_on`**: DEC-003 decided to normalize `depends_on` into a real
join table, split out into
[FK-003](../phase-3-fk-migration/FK-003-depends-on-join-table.md).
This task does **not** touch `depends_on` — no field-exposure work needed
here since it's already a full field on `torque_task_create`/`update`
today (just JSON-blob-backed); FK-003 changes the storage underneath
without changing this tool's request/response contract. If FK-003 hasn't
landed yet when this task is picked up, no interim docstring caveat is
needed either — FK-003 is scheduled, not deferred indefinitely.

## Acceptance criteria

- [x] `torque_task_list` accepts `statuses[]`, `created_at`/`updated_at`
      range params, and the budget/duration/profile filters listed above.
- [x] `bulk_update`/`bulk_delete`/`bulk_tag` exist, matching PRIM-003's
      response shape.
- [x] `torque_task_create` accepts every field listed in the create-field
      expansion section; a create call with `subtodos[]` produces a task
      with those subtodos already attached.
- [x] `torque_task_transition` accepts an optional `comment` and posts it
      atomically with the status change (same transaction, not two calls).
- [x] `depends_on` handling matches DEC-003's decision.

## Out of scope

- Duplicate/clone tool for Task (audit notes this as low-priority, not in
  ADR-0004's target inventory).
- Archive/soft-delete parity for Task (explicitly optional per ADR — Task
  keeps hard-delete + `abandoned` status as its close path).

## Execution notes

**Worktree was stale — fast-forwarded onto `main` first.** This worktree
branched from `main` before PRIM-001/002/003, FIX-001/002/004, FK-002/003,
and DEC-003 actually landed (33 commits ahead on `main`, none present on the
branch point). None of the "already done" bulk/pagination/sort
infrastructure existed here at session start. Since the branch had zero
unique commits of its own, `git merge main --ff-only` was a clean
fast-forward with no conflicts — after that, PRIM-003's bulk pattern
(`internal/service/bulk.go`, `internal/service/task_bulk.go`,
`internal/mcpadapter/task_bulk_tools.go`) and PRIM-001/002's pagination/sort
(`internal/service/pagination/`) were present and matched the task's
description of them.

**Bulk ops verification found a real gap, now fixed.** `BulkUpdate`/
`BulkDelete`/`BulkTag` (PRIM-003) already matched the canonical
`{succeeded, failed}` envelope. `bulk_transition` did **not** — it still
returned its pre-PRIM-003 `{success: int, failed: int, errors?: string}`
shape, and its `[]error` return from `TaskService.BulkTransition` had no id
attached per failure (a caller couldn't tell which task a given error
belonged to in a partial-success batch). Fixed by routing
`BulkTransition` through the shared `RunBulk` helper (now returns
`[]service.BulkItemError`) and switching `handleTaskBulkTransition` to the
shared `bulkResult` envelope — no other call site depended on the old
shape (`internal/httpserver/tasks.go`'s HTTP bulk-transition endpoint
already discarded the error slice).

**Filters implemented at every layer** (`internal/persistence/sqlstore/tasks.go`
`TaskFilter`/`ListTasks`, `internal/mcpadapter/task_tools.go`
`handleTaskList`): `statuses[]` (store-layer `Statuses` OR-filter already
existed, only needed MCP wiring), `created_after`/`created_before`/
`updated_after`/`updated_before` (RFC3339 in, reformatted to
`SQLiteDatetimeLayout` to match the stored TEXT shape — same convention
`taskSortValue`/`taskCursorArg` already use), `cost_budget_gte/lte`,
`token_budget_gte/lte`, `max_duration_ms_gte/lte`, `max_retries_gte/lte`
(a NULL budget column never matches either bound — no separate "has budget"
filter needed), and `agent_profile`/`launch_profile` exact-match.

**Create-field expansion**: all fields listed in scope
(`tools`, `on_review`, `files`, `cost_budget`, `max_retries`,
`permissions`, `environment`, `max_duration_ms`, `token_budget`,
`escalation_chain`, `quality_gates`, `deliverables`, `blocked_reason`)
wired onto `torque_task_create`'s MCP schema — the service layer already
accepted them all on `TaskCreateInput`, confirming the task file's
description.

**`subtodos[]` seed list — found and fixed a latent ID-collision gap.**
`TaskCreateInput.Subtodos` already let `Create` skip
`ExtractSubtodosFromDescription`, but nothing generated ids for
caller-supplied items missing one, or rejected duplicates — unlike
`AddSubtodo`'s single-append path (FIX-002), which does both. Added the
same normalization to `TaskService.Create` (id auto-gen + duplicate
rejection over the whole seed batch, validated before `NextTaskID`/
`CreateTask` run so a bad seed list never leaves a half-created task row).

**Transition comment**: added `TaskService.TransitionWithComment` and
`WriteTx.AddComment` (new) so the status UPDATE and the comment INSERT
commit in one SQL transaction (`sqlstore.Store.BeginWriteTx` +
`WriteTx.TransitionTask` + `WriteTx.AddComment` + `Commit`). Only used when
the MCP caller actually passes a non-empty `comment` — the far more common
no-comment path still calls `Transition`/`ForceTransition` completely
unchanged, so no risk to the existing well-exercised path. Verified an
FSM-invalid transition leaves no orphan comment behind (FSM check runs
before the transaction opens).

**`depends_on`**: untouched, as scoped — FK-003 (join-table normalization)
had already landed on `main` and came in with the fast-forward merge.

**Tests added**: `internal/persistence/sqlstore/tasks_test.go` (new filter
coverage: statuses OR, agent/launch profile, created/updated range,
budget/duration range incl. NULL-exclusion), `internal/service/task_test.go`
(`TransitionWithComment` incl. invalid-transition/force cases, subtodos
seed id-gen/dup-rejection/empty-disables-auto-extract), and
`internal/mcpadapter/task_tools_test.go` /
`internal/mcpadapter/task_bulk_tools_test.go` (full-stack MCP coverage for
all of the above, plus the `bulk_transition` response-shape fix).

**Verification**: `go build ./...` and `go test ./...` both clean across
the full repo (all packages, no failures).
