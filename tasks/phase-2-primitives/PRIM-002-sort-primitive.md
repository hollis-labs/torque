# PRIM-002 — Sort primitive (`sort_by`/`sort_dir`)

**Phase:** 2 — Shared primitives
**Status:** done
**Depends on:** none (independent of DEC-001; coordinate with PRIM-001 on
cursor tiebreak semantics if cursor pagination was chosen)
**Blocks:** ENT-TASK, ENT-COMMENT, ENT-PROJECT, ENT-EPIC, ENT-SPRINT,
ENT-ISSUE, ENT-PLAN
**Source:** ADR-0004 §3 (Sorting); audit "Cross-Cutting Findings §2
(Sorting)", "Proposed Direction B"

## Summary

Zero `OrderBy`/`SortBy` fields exist anywhere in `internal/persistence/
sqlstore` today. Build the shared `sort_by` (allow-listed per entity) /
`sort_dir` (`asc`/`desc`) mechanism once, with Task as the reference
implementation.

## Scope

- Add `OrderBy sort_by` + `sort_dir` support to the store-layer filter
  structs, starting with Task's (`TaskFilter`, `tasks.go`).
- Build a small shared validation helper: given an entity's allow-list
  (e.g. Task: `priority, status, updated_at, created_at`; Epic/Sprint/
  Project: `name, status, updated_at, created_at`), reject unknown
  `sort_by` values with a clean `arg_invalid` error rather than silently
  ignoring or erroring at the SQL layer.
- If PRIM-001 chose cursor pagination: confirm the cursor's tiebreak-on-`id`
  design still holds for every sort column, not just the default — a
  cursor keyed to `updated_at` needs `(updated_at, id)` as the real sort
  key, not just `updated_at`.
- Apply to **Task's `list`** as the reference implementation, alongside
  PRIM-001.
- Document each entity's actual default order accurately regardless of what
  else lands — this closes the same ground as FIX-004 but as a byproduct;
  if PRIM-002 for a given entity lands after FIX-004 already fixed that
  entity's docstring, just confirm consistency rather than redoing it.

## Acceptance criteria

- [x] `torque_task_list` accepts `sort_by` + `sort_dir`, validated against
      an explicit allow-list, with a clean error on an invalid value.
- [x] Default order (no `sort_by` given) matches what the docstring says
      (coordinate with FIX-004 so these don't contradict).
- [x] The allow-list + validation helper is reusable, not copy-pasted
      per-entity in Phase 4.
- [x] If cursor pagination is in play, paging through a `sort_by=priority`
      list with duplicate priority values doesn't produce duplicate/missing
      rows across pages.

## Out of scope

- Rolling out to the other 6 entities — Phase 4.
- Filter/facet closure beyond sorting (separate ADR concern, folded into
  each entity's Phase 4 task where relevant).

## Execution notes

Implemented together with PRIM-001 in one pass — see PRIM-001's execution
notes for the cursor helper / response envelope implementation; this section
covers the `sort_by`/`sort_dir` allow-list specifics and the discovered
correctness bug on the `updated_at`/`created_at` sort columns.

**Allow-list + validation helper:** `internal/service/pagination.
ValidateSortBy(value string, allowed ...string) (string, error)` and
`ValidateSortDir(value string) (string, error)` (`sort.go`, same new package
as PRIM-001's cursor helper — see PRIM-001's notes for why it lives in
`internal/service`). Both are case-insensitive, return the canonicalized
lower-case value, and are generic — a Phase 4 entity calls
`pagination.ValidateSortBy(raw, "name", "status", "updated_at",
"created_at")` with its own allow-list, no per-entity copy of the validation
logic itself.

**`TaskFilter` (`internal/persistence/sqlstore/tasks.go`):** added
`SortBy`, `SortDir`, `AfterSortValue`, `AfterID` string fields. `SortBy ==
""` (unset) preserves the exact original `ORDER BY priority ASC, created_at
ASC` — every existing internal caller (picker, scheduler, sprint service,
parent-rollup, HTTP `/api/v1/tasks`) that doesn't set `SortBy` is
unaffected. `ListTasks` builds `ORDER BY <col> <dir>, id ASC` plus the
DEC-001 tuple-comparison WHERE predicate only when `SortBy` is set.

**Task's allow-list** (`taskSortAllowList` in `task_tools.go`): `priority,
status, updated_at, created_at`, exactly as scoped. Default
(`taskSortDefaultBy`/`taskSortDefaultDir`) is `priority`/`asc`.

**Default order vs FIX-004:** FIX-004 fixed `torque_task_list`'s docstring
to read "ordered priority ASC, created_at ASC" without touching the query.
DEC-001's cursor design requires a *single* sort column tiebroken on `id`
(not FIX-004's two-column priority+created_at order), so the tiebreak column
changed from `created_at` to `id`. This is a judgment call, not a
regression: CW task IDs are date-prefixed and assigned monotonically per day
(`NextTaskID`), so `id ASC` and `created_at ASC` produce the same practical
ordering for same-priority ties — the docstring now reads "ordered priority
ASC (tiebreak id ASC) by default" to describe the new (equivalent-in-
practice) reality accurately, per FIX-004's own instruction to "confirm
consistency rather than redoing it" when the two land in the same pass.

**Cursor tiebreak holds for every sort column, not just the default**
(explicit PRIM-002 scope item): `ListTasks`'s `ORDER BY <sortCol> <dir>, id
ASC` and its WHERE predicate `(sortCol <op> ? OR (sortCol = ? AND id > ?))`
are generic over `sortCol` — verified for `priority` (duplicate-value
tiebreak, `TestListTasks_SortByPriority_CursorTiebreakOnID`) and for
`updated_at` (`TestListTasks_SortByUpdatedAtDesc_CursorRoundTrips`,
`TestFullStack_TaskList_SortByUpdatedAtDesc`).

**Discovered bug, fixed in scope — `tasks.updated_at` write-format
inconsistency:** while wiring up `sort_by=updated_at`, cursor pagination on
that column produced wrong results (a page would re-return its own last
row instead of advancing). Root cause, confirmed empirically against this
project's actual modernc.org/sqlite v1.48.1 + `go-sqlite/sqlitekit` DSN
setup (no `_time_format` override):
- A freshly created task's `updated_at`/`created_at` come from the column's
  `DEFAULT CURRENT_TIMESTAMP` (never in `CreateTask`'s explicit INSERT
  column list) — stored as literal text `"YYYY-MM-DD HH:MM:SS"` (confirmed
  via `quote(updated_at)`; whole-second, no zone suffix).
- Every UPDATE site that touched `updated_at` bound a raw `time.Now().UTC()`
  `time.Time` value. The driver's write path formats an unconverted
  `time.Time` via Go's `time.Time.String()`, producing `"YYYY-MM-DD
  HH:MM:SS.ffffff +0000 UTC"` — a different text *shape*, not just different
  precision (space-separated timestamp AND a trailing zone name).
- Two incompatible formats coexisting in one column silently breaks plain
  TEXT comparison: SQLite's own `julianday()`/`datetime()` can't even parse
  the `time.Time.String()` shape, and raw byte comparison between the two
  shapes has no relationship to actual chronological order (confirmed: a
  row could lexically compare as "less than itself" against its own
  round-tripped cursor value).
- Reading was never affected — modernc.org/sqlite auto-parses any
  DATE/DATETIME/TIMESTAMP-declared column on read regardless of which shape
  was written, which is exactly why this was invisible until cursor
  pagination started comparing raw column text directly instead of going
  through the driver's read-side parser.
- **Fix:** `internal/persistence/sqlstore/tasks.go` adds
  `SQLiteDatetimeLayout = "2006-01-02 15:04:05"` (matching
  `CURRENT_TIMESTAMP`'s own output byte-for-byte) and `updatedAtNow() string`
  (whole-second `time.Now().UTC()`, pre-formatted with that layout, bound as
  a plain `string` so the driver's `time.Time`-specific reformatting is
  never invoked). Every `tasks.updated_at` write site now uses
  `updatedAtNow()`: `UpdateTask`, `transitionTaskTx` (both branches),
  `ParkTaskOnCheckpoint` in `tasks.go`; the `WriteTx` equivalents
  (`transitionTask`, `IncrementTaskRetryCount`, `SetTaskEscalationStep`,
  `SetTaskAgentProfile`, `SetTaskMaxRetries`) in `write_tx.go`; and
  `SetSubtodos` in `subtodos.go`. `taskCursorArg`'s decode side and
  `mcpadapter.taskSortValue`'s encode side both use
  `sqlstore.SQLiteDatetimeLayout` (not `time.RFC3339Nano`, which was the
  original — incorrect — implementation) so cursor values match the actual
  stored text exactly.
- **Not touched:** `CreateTask`'s `now := time.Now().UTC()` (the in-memory
  `TaskRecord.CreatedAt`/`UpdatedAt` returned to the caller on create) still
  holds full Go precision even though the DB row itself only has
  whole-second precision from `CURRENT_TIMESTAMP` — a pre-existing,
  narrow, cosmetic discrepancy (the create-response value vs. a subsequent
  `GetTask`) that doesn't affect sort/cursor correctness (nothing paginates
  off the create response) and was left alone to keep this fix minimal and
  scoped to what the sort primitive actually needed.
- This same fix is committed as part of PRIM-001's file set (same files);
  documented here since it was PRIM-002's `sort_by=updated_at` work that
  surfaced it.

Files touched: see PRIM-001's execution notes (same set — PRIM-001 and
PRIM-002 landed as one coherent change per the coordination note in both
tasks' headers).

`go build ./...` and `go test ./...` pass. Re-ran the timing-sensitive
sort/cursor tests (`TestListTasks_SortByUpdatedAtDesc_CursorRoundTrips`,
`TestFullStack_TaskList_SortByUpdatedAtDesc`) multiple times with `-count=1`
to confirm they aren't flaky — both sleep past a full second before
asserting order, since `updated_at` is whole-second precision by design
(see above).
