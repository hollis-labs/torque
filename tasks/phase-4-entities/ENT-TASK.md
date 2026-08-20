# ENT-TASK — Task: filters, bulk ops, create-field expansion, transition comment

**Phase:** 4 — Per-entity rollout
**Status:** todo
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

- [ ] `torque_task_list` accepts `statuses[]`, `created_at`/`updated_at`
      range params, and the budget/duration/profile filters listed above.
- [ ] `bulk_update`/`bulk_delete`/`bulk_tag` exist, matching PRIM-003's
      response shape.
- [ ] `torque_task_create` accepts every field listed in the create-field
      expansion section; a create call with `subtodos[]` produces a task
      with those subtodos already attached.
- [ ] `torque_task_transition` accepts an optional `comment` and posts it
      atomically with the status change (same transaction, not two calls).
- [ ] `depends_on` handling matches DEC-003's decision.

## Out of scope

- Duplicate/clone tool for Task (audit notes this as low-priority, not in
  ADR-0004's target inventory).
- Archive/soft-delete parity for Task (explicitly optional per ADR — Task
  keeps hard-delete + `abandoned` status as its close path).
