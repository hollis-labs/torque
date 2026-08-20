# FIX-001 — Task create/update field-detection + required fixes

**Phase:** 1 — Locked bug fixes
**Status:** done
**Depends on:** none
**Blocks:** ENT-TASK
**Source:** ADR-0004 §1 items 1–2 (locked)

## Summary

Two concrete, audited deltas on `torque_task_create`/`torque_task_update`,
locked in ADR-0004 §1 as small and independent of the larger surface work.

## Scope

1. **Drop `mcp.Required()` from `description` on `torque_task_create`.**
   Neither the DB (`description TEXT NOT NULL DEFAULT ''`) nor
   `TaskService.Create` (only checks `Title`) requires it — the MCP schema
   is the only layer over-requiring. File:
   `internal/mcpadapter/task_tools.go`.
2. **Fix `handleTaskUpdate`'s field-detection bug for `title`/`description`/
   `priority`.** These three currently use value-based detection
   (`if v := reqStr(req, "title"); v != ""`), unlike every other field on
   the same tool, which correctly uses presence-based detection
   (`if _, ok := args["executor"]; ok`). Effect: `description` (and
   `title`) can never be cleared via update — `"description": ""` silently
   no-ops, contradicting the tool's own docstring ("empty string clears
   most nullable scalars"). Fix: switch all three to presence-based
   detection, matching the rest of the tool.
3. **Implementation note for the presence-based `title` fix**: once `title`
   is presence-based, an explicit `"title": ""` will reach
   `TaskService.Update`. Add an explicit non-empty check there (a clean
   `ValidationError`, not a raw DB `NOT NULL` failure) — title is the one
   truly-required field and must not be clearable to empty.

## Acceptance criteria

- [ ] `torque_task_create` accepts a create call with `description` omitted
      entirely (not `""`) without a required-field error.
- [ ] `torque_task_update` with `"description": ""` actually clears the
      description (verified against the DB row, not just a 200 response).
- [ ] `torque_task_update` with `"title": ""` returns a clean
      `arg_invalid`/validation error, not a raw SQLite `NOT NULL` failure.
- [ ] `torque_task_update` with `"priority": 0` (or whatever zero-value
      makes sense) is correctly detected as "field present" and applied,
      not silently ignored because the old code treated the zero value as
      "not set."
- [ ] Existing task update tests still pass; add coverage for the three
      presence-based fields if not already covered.

## Out of scope

- The unenforced `priority` range (1–5, documented but not validated
  anywhere) — ADR-0004 explicitly flags this as "not actioned this round,"
  pick up only if it blocks something concrete.
- Any of the create-field expansion in ENT-TASK (this task is the two
  narrow §1 bug fixes only).

## Execution notes

All three scope items implemented as specified; no deviations.

1. **`torque_task_create` description no longer required** —
   `internal/mcpadapter/task_tools.go`: dropped `mcp.Required()` from the
   `description` param declaration (was the only over-requiring layer; DB
   column is `NOT NULL DEFAULT ''` and `TaskService.Create` only checks
   `Title`).
2. **`handleTaskUpdate` field-detection fix** — same file, `handleTaskUpdate`:
   switched `title`, `description`, and `priority` from value-based checks
   (`if v := reqStr(...); v != ""` / `v != 0`) to presence-based checks
   (`if _, ok := args["..."]; ok`), matching every other field on the tool.
   Confirmed the store layer (`sqlstore.UpdateTask`) already builds its SQL
   set-clauses off pointer-nil checks, so no downstream change was needed
   there — the bug was isolated to the MCP handler.
3. **`TaskService.Update` title-non-empty guard** —
   `internal/service/task.go`: added a check at the top of `Update` that
   rejects `input.Title != nil && *input.Title == ""` with a
   `*ValidationError{Field: "title", ...}`, mirroring `Create`'s existing
   check. This maps to `arg_invalid` via the adapter's existing
   `mapServiceError`, so no adapter-side error-mapping change was required.

**Test coverage added** (acceptance criteria: add coverage for the three
presence-based fields if not already covered — it wasn't):
- `internal/mcpadapter/task_tools_test.go`:
  `TestFullStack_TaskCreate_DescriptionOptional` (create with description
  omitted entirely succeeds), `TestFullStack_TaskUpdate_PresenceBasedFields`
  (`description:""` clears the column, verified via a follow-up
  `torque_task_get` read of the persisted row rather than trusting the
  update call's 200; `priority:0` is applied and persists), and
  `TestFullStack_TaskUpdate_TitleCannotBeCleared` (`title:""` returns
  `arg_invalid`/field=`title`, message does not leak a raw `NOT NULL`
  string, and the title is left unchanged).
- `internal/service/task_test.go`: `TestTaskUpdateTitleCannotBeCleared`
  exercises the new `TaskService.Update` guard directly (bypassing the MCP
  layer) to pin the service-level contract independent of the adapter.

**Verification:** `go build ./...`, `go vet ./...`, and `go test ./...`
(full suite, `-count=1`) all pass — no existing test relied on the old
value-based/required-description behavior, so nothing else needed updating.

**Files touched:** `internal/mcpadapter/task_tools.go`,
`internal/mcpadapter/task_tools_test.go`, `internal/service/task.go`,
`internal/service/task_test.go`.

**Judgment calls / notes:**
- Kept the `ValidationError` message generic ("title cannot be cleared to
  empty") rather than reusing Create's "title is required" wording, since
  the update-path failure mode is conceptually "clearing," not "missing on
  create" — purely cosmetic, easy to change if a reviewer prefers parity.
- Did not touch the HTTP-side `createTask` handler in
  `internal/httpserver/tasks.go` (mentioned only as a "parallel override" in
  a comment) — out of scope per the task's explicit MCP-adapter-only file
  citation.
