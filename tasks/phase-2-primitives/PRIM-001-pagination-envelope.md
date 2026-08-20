# PRIM-001 — Pagination + response envelope primitive

**Phase:** 2 — Shared primitives
**Status:** done
**Depends on:** DEC-001 (pagination strategy must be decided first)
**Blocks:** ENT-TASK, ENT-COMMENT, ENT-PROJECT, ENT-EPIC, ENT-SPRINT,
ENT-ISSUE, ENT-PLAN
**Source:** ADR-0004 §3 (Pagination, Response envelope); audit "Cross-Cutting
Findings §1 (Pagination)", "Proposed Direction A"

## Summary

Build the shared pagination + envelope shape once, with Task as the
reference implementation, so every Phase 4 entity task applies the same
pattern instead of re-deriving it. This is the highest-leverage primitive
in the ADR — nearly every per-entity task depends on it.

## Scope

Implement whatever DEC-001 decided:

- **If cursor**: an opaque `cursor` param every list tool accepts (returned
  from the previous call), internally breaking sort ties on `id`. Build the
  cursor encode/decode helper once (likely in `internal/service` or a
  shared package), not per-entity.
- **If offset**: wire the `Offset` field that already exists at the SQL
  layer (`tasks.go:108`, `collections.go:30-34`, `epics.go:23-28`,
  `sprints.go:26-30`, `projects.go:29-33`) through the service and MCP
  layers — this is "the single cheapest, lowest-risk fix" per the audit,
  pure plumbing for those five.

Either way, land the **response envelope** change:
- `data` becomes `{items, meta: {returned, limit, total_count|has_more,
  next_cursor, hint?}}` — `next_cursor` is `null` when exhausted (cursor
  mode) or `has_more`/`total_count` reflects the true remaining count
  (offset mode).
- `{ok, data}` outer envelope stays unchanged.

Also fix independently-noted issues in the same code path while you're
there (small, same-file, same-concern):
- `internal/mcpadapter/response.go`'s `cappedJSONResult` applies a *second*,
  silent truncation by response byte size (100KB cap) on top of `limit` —
  keep this, but make sure the `meta` returned still accurately reflects
  what actually made it into the response after byte-capping, not what was
  requested.
- `torque_session_list`'s `meta.limit` is hardcoded to
  `defaultGenericListLimit` regardless of the caller-supplied limit
  actually honored (`session_tools.go:193`) — **note**: Session is out of
  ADR-0004's scope, so do not fix this here; flagged only so the pattern
  isn't accidentally copied. Use `model_tools.go:67,81`'s clamp-once/reuse
  pattern as the correct reference instead.

Apply the primitive to **Task's `list`/`get`** as the reference
implementation (Task is "closest to done already" per ADR §5) — this proves
the pattern works end-to-end before the other 6 entities adopt it in
Phase 4.

## Acceptance criteria

- [x] Shared pagination helper exists and is entity-agnostic (not
      hand-copied per file).
- [x] `torque_task_list` returns the new envelope shape, verified against a
      dataset large enough to need a second page.
- [x] Paging through Task results with the new mechanism doesn't
      duplicate/skip rows even when a row is inserted between calls (if
      cursor was chosen — this is the property offset can't guarantee).
- [x] `meta.total_count` (or `has_more`) is accurate, not a copy of `limit`.
- [x] The helper is documented well enough (short doc comment, not a design
      doc) that Phase 4 entity tasks can adopt it by following the Task
      example.

## Out of scope

- Rolling the primitive out to the other 6 entities — that's each entity's
  own Phase 4 task.
- Sorting (PRIM-002, separate primitive, though cursor mode's tiebreak
  design should anticipate sort_by being added).

## Execution notes

Implemented together with PRIM-002 in one pass, per DEC-001's binding spec
(cursor mode, chosen by the project owner over the subagent's original
hybrid proposal). See PRIM-002's execution notes for the sort_by allow-list
specifics; this section covers the cursor/envelope half.

**Shared cursor helper — `internal/service/pagination` (new package):**
- `cursor.go`: `Cursor{V, SortBy, SortDir, SortValue, ID}` +
  `Encode(sortBy, sortDir, sortValue, id) string` (base64url(JSON)) +
  `Decode(token string) (Cursor, error)` + `(Cursor) Validate(sortBy, sortDir
  string) error`. Implements DEC-001's binding spec literally.
- `sort.go`: `ValidateSortBy(value string, allowed ...string) (string,
  error)` and `ValidateSortDir(value string) (string, error)` — the shared
  allow-list validator PRIM-002 also uses.
- Placed in `internal/service` (not `internal/mcpadapter` or
  `internal/persistence/sqlstore`) because it depends on neither: cursor
  encode/decode is pure string/JSON, with no knowledge of MCP request shapes
  or SQL column types. Both mcpadapter and sqlstore can (and do) reach it
  without an import cycle. Unit-tested standalone in `cursor_test.go`.

**`torque_task_list` (`internal/mcpadapter/task_tools.go`):**
- Added `sort_by`/`sort_dir`/`cursor` params. Validates via
  `pagination.ValidateSortBy`/`ValidateSortDir`/`Cursor.Validate`, each
  failure returning a clean `arg_invalid` (field-scoped to `sort_by`,
  `sort_dir`, or `cursor`).
- Fetches `limit+1` rows (`TaskFilter.Limit = limit + 1`) to compute
  `has_more` without a `COUNT(*)` query — DEC-001's recommended cheaper
  default, no concrete need for exact `total_count` surfaced for Task.
- New `taskListCursorEnvelope` builds the `{items, meta}` response via a new
  `cappedCursorJSONResult` helper (`internal/mcpadapter/response.go`),
  parallel to (not a replacement for) the existing `cappedJSONResult` — see
  below for why.
- `torque_task_search` and every other entity's list/search tool are
  **unchanged**, still on `cappedJSONResult`/the old `{truncated, returned,
  limit, hint?}` meta shape — out of scope here (Task `list` only, per this
  task's scope).

**Response envelope (`internal/mcpadapter/response.go`):**
- New `listMetaCursor{Truncated, Returned, Limit, HasMore, NextCursor,
  Hint}` and `listEnvelopeCursor{Items, Meta}` types, and a new
  `cappedCursorJSONResult(items, limit, sortBy, sortDir, hasMoreFromQuery,
  cursorAt)` function — deliberately a *sibling* to `cappedJSONResult`, not
  a signature change to it. `cappedJSONResult` has 19 existing call sites
  across every other entity's list/search tool; none have adopted cursor
  pagination yet (that's each entity's own Phase 4 task), so changing its
  signature would force an unwanted migration on all of them today. Phase 4
  entities adopt `cappedCursorJSONResult` directly when they add cursor
  support, following Task's example — that's the "helper documented well
  enough to adopt by following the Task example" bar from the acceptance
  criteria.
- `cappedCursorJSONResult`'s `cursorAt` callback lets the caller (which
  still holds the typed `[]sqlstore.TaskRecord`, not just `[]any`) compute
  `next_cursor` from the **last row actually included after the byte-size
  trim**, not before — the trim loop calls `cursorAt` at whatever candidate
  boundary it's testing, so a byte-cap-triggered truncation and a
  query-level `has_more` both correctly converge on the same "what actually
  shipped" answer.
- Kept `Truncated` (byte-cap flag) *distinct* from `HasMore`/`NextCursor`
  (query-exhaustion flag) — they're orthogonal concerns; a page can be
  byte-truncated with no more query rows, or have more query rows while
  fully fitting under the byte cap.
- `has_more` chosen over `total_count` per DEC-001's recommended default (no
  concrete need for exact counts surfaced for Task).

**`TaskFilter.Offset` (`internal/persistence/sqlstore/tasks.go`):**
- **Left in place, not removed.** It is dead from `torque_task_list`'s
  perspective (superseded by cursor pagination) but is NOT dead code
  overall: `internal/httpserver/tasks.go`'s `/api/v1/tasks` HTTP handler
  still sets `filter.Offset` directly from its own `?offset=` query param,
  entirely independent of ADR-0004/MCP scope. Removing the field would have
  broken that unrelated HTTP endpoint. Documented in `TaskFilter`'s doc
  comment.

**Judgment call / discovered bug (see PRIM-002's execution notes for the
full story — it blocked `sort_by=updated_at`/`created_at`, which is a
PRIM-002 concern, but the fix landed in the same files this task touches):**
fixed a pre-existing `tasks.updated_at` write-format inconsistency
(`internal/persistence/sqlstore/tasks.go`'s new `updatedAtNow()` helper,
also applied in `write_tx.go` and `subtodos.go`) that silently broke plain
TEXT comparison — necessary for `has_more`/cursor correctness on that sort
column, not scope creep on pagination itself.

Files touched (beyond the two task files): `internal/service/pagination/`
(new: `cursor.go`, `sort.go`, `cursor_test.go`),
`internal/mcpadapter/response.go`, `internal/mcpadapter/task_tools.go`,
`internal/mcpadapter/task_tools_test.go`, `internal/mcpadapter/errors.go`,
`internal/persistence/sqlstore/tasks.go`,
`internal/persistence/sqlstore/tasks_test.go`,
`internal/persistence/sqlstore/write_tx.go`,
`internal/persistence/sqlstore/subtodos.go`.

`go build ./...` and `go test ./...` pass with no other changes needed.
