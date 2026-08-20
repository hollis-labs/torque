# FIX-003 — Fix Epic status vocabulary bug

**Phase:** 1 — Locked bug fixes
**Status:** done
**Depends on:** none
**Blocks:** ENT-EPIC
**Source:** ADR-0004 §5 (Epic); audit "Per-Entity Findings → Epic [high]",
"Proposed Direction G.1"

## Summary

Confirmed correctness bug, independent of the broader ergonomics work:
`torque_epic_update`'s own docstring and example
(`{"id":"EP-4","status":"closed"}`, "open<->closed transitions",
`epic_tools.go:31-38`) will **always fail validation** —
`EpicService.Update` only accepts `validEpicStatuses = {active, inactive}`
(`service/epic.go:33-36`). The DB schema's own default is
`status TEXT NOT NULL DEFAULT 'open'`
(`migrations/001_initial.sql:127`), but `CreateEpic` overrides that to
`"active"` in Go (`epics.go:41-43`) — so `"open"` is dead too.

## Scope

Reconcile the enum one way or the other:
- **Option A**: change `validEpicStatuses` to `{open, closed}` (or whatever
  set matches the tool's documented example), update `CreateEpic`'s default
  accordingly, and confirm the DB default stays consistent.
- **Option B**: keep `active`/`inactive` as the real enum and fix the tool
  docstring/example on `torque_epic_update` to stop advertising
  `open`/`closed`.

Pick whichever is closer to how Epic status is actually used/displayed
elsewhere (GUI, HTTP) — check for existing UI copy or HTTP contract that
already commits to one vocabulary before choosing, to avoid introducing a
second mismatch.

## Acceptance criteria

- [ ] `torque_epic_update`'s docstring example is runnable as documented
      (whichever vocabulary is chosen, the example uses real, accepted
      values).
- [ ] `EpicService.Update`'s valid-status set, `CreateEpic`'s default, and
      the DB column default (`001_initial.sql`) all agree.
- [ ] Existing epics with the old vocabulary value in the DB still load and
      display correctly (no migration needed if only the accepted-input set
      changes, but double check nothing reads the raw DB value expecting
      the old vocabulary elsewhere).

## Out of scope

- Epic `priority` settability (ENT-EPIC, not this bug fix).
- Epic archive/unarchive (PRIM-004 / ENT-EPIC).

## Execution notes

**Chosen: Option B** — kept `active`/`inactive` as the real enum, fixed the
`torque_epic_update`/`torque_epic_list`/`torque_epic_delete` docstrings and
examples to stop advertising `open`/`closed`.

**Why:** Before touching anything, checked every layer that already commits
to a vocabulary:
- GUI: `apps/gui/src/lib/types.ts` `ContainerStatus = 'active' | 'inactive' |
  'completed'`, shared with `Project`. `EpicEditPage.tsx`'s status `<select>`
  only offers `active`/`inactive`; `EpicsPage.tsx` filters/counts by
  `status === 'active'`; `scope-manager-dialog.tsx`'s
  `normalizeScopeRecordStatus` collapses everything to `active`/`inactive`.
- Service/store: `EpicService.Update`'s `validEpicStatuses` and
  `Store.CreateEpic`'s in-Go default (`internal/persistence/sqlstore/epics.go:41-43`)
  already both say `active`.
- Tests: `internal/persistence/sqlstore/epics_test.go` and
  `internal/service/epic_test.go` already exercise `"active"`/`"inactive"`
  exclusively — the test suite had already locked in the real vocabulary.
- `projects.status` (the sibling entity `EpicRecord`'s `ContainerStatus`
  type is shared with) had its own `open`→`active`-style default fixed back
  in `004_enrich_entities.sql` (`ALTER TABLE projects ADD COLUMN status TEXT
  NOT NULL DEFAULT 'active'`), i.e. there's precedent for `active`/`inactive`
  being Torque's actual container-status vocabulary.
- HTTP layer (`internal/httpserver/epics.go`) is a pure pass-through — no
  vocabulary commitment either way.

`open`/`closed` had zero real callers; only the `torque_epic_update`
docstring/example and the `torque_epic_list` docstring/example advertised
it (plus the dead `001_initial.sql` column default). Option A would have
meant unwinding the GUI, service validation, and existing tests — clearly
the wrong direction.

**Files touched:**
- `internal/mcpadapter/epic_tools.go` — `torque_epic_update` docstring
  ("open<->closed transitions" → "active<->inactive transitions", "close via
  status=closed" → "status=inactive", example `status:"closed"` →
  `status:"inactive"`, status field doc `open|closed|inactive` →
  `active|inactive`); `torque_epic_delete` docstring ("status=closed" →
  "status=inactive"); `torque_epic_list` docstring/example (`open|closed|inactive`
  → `active|inactive`, example `{"status":"open"}` → `{"status":"active"}`).
- `internal/service/epic.go` — unchanged; `validEpicStatuses` already
  `{active, inactive}`.
- `internal/persistence/sqlstore/epics.go` — unchanged; `CreateEpic`'s
  Go-level default already `"active"`.
- `internal/persistence/sqlstore/migrations/027_epic_status_default.sql`
  (new) — full-table-rebuild migration flipping `epics.status DEFAULT`
  from `'open'` (set in `001_initial.sql`, never corrected) to `'active'`,
  following the same rebuild pattern already used for the same class of fix
  on `tasks.executor` (migrations 016/021/024). No data backfill: no code
  path has ever inserted an epic with `status='open'` (`CreateEpic` always
  overrides an empty status to `"active"` before the INSERT), so there was
  nothing to migrate — only the schema-level default for future raw inserts
  was wrong.
- `internal/persistence/sqlstore/migrations/migrate_test.go` — added a
  check that a freshly-migrated DB defaults a status-less epic insert to
  `"active"`, and that the rebuilt `epics` table still has all its columns
  (`priority`, `project_id` from `004_enrich_entities.sql`).

**Verification against acceptance criteria:**
- `torque_epic_update`'s example (`{"id":"EP-4","status":"inactive"}`) now
  uses a value `validEpicStatuses` actually accepts.
- Docstring vocabulary, `EpicService.Update`'s valid-status set,
  `CreateEpic`'s default, and the DB column default now all agree on
  `active`/`inactive`.
- Existing epics with a legacy/unexpected status value (including a
  hypothetical `'open'`) still load fine: `GetEpic`/`ListEpics` `SELECT`
  the raw `status` string with no `CHECK` constraint or Go-side enum
  decoding, and the GUI renders `epic.status` as a plain badge string /
  filters everything non-`'inactive'` into the "active" bucket
  (`scope-manager-dialog.tsx`'s `normalizeScopeRecordStatus`) — nothing
  reads the DB value expecting `open`/`closed` specifically.

**Tests:** `go build ./...` and `go test ./...` both pass (full suite, all
packages green, including the new migration assertion).

**Issues/blockers:** none.
