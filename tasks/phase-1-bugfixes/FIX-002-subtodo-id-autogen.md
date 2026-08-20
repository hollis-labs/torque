# FIX-002 — Auto-generate Subtodo IDs

**Phase:** 1 — Locked bug fixes
**Status:** done
**Depends on:** none
**Blocks:** ENT-SUBTODO, ENT-TASK
**Source:** ADR-0004 §1 item 3 (locked)

## Summary

`torque_task_subtodo_add`'s `id` is currently required and caller-invented,
enforced in `TaskService.AddSubtodo` (`internal/service/task.go:637-639`) —
the only entity in the system without a server-generated ID.

## Scope

- Make `id` optional on `torque_task_subtodo_add`.
- When omitted, the server generates a unique id. Scheme is not decided by
  the ADR ("short random slug or sequential, doesn't need deciding here") —
  pick one consistent with how other entity IDs are generated elsewhere in
  the codebase (check existing ID-generation helpers before inventing a new
  one).
- When the caller *does* supply an id, existing uniqueness validation still
  applies — meaningful slugs (e.g. `"check-auth-flow"`) stay possible.

## Acceptance criteria

- [x] `torque_task_subtodo_add` with `id` omitted succeeds and returns a
      generated id.
- [x] `torque_task_subtodo_add` with an explicit `id` still works and still
      rejects duplicates within the same task.
- [x] `TaskService.AddSubtodo` signature/behavior updated accordingly;
      existing callers passing an explicit id are unaffected.
- [x] Generated IDs don't collide with caller-supplied slug-style ids in
      practice (namespace or format them distinctly if that's the simplest
      way to guarantee this, e.g. a generated-id prefix).

## Out of scope

- Cross-task subtodo query surface (ADR explicitly keeps Subtodo
  task-scoped only, §1 item 4 — not revisited).
- `bulk_add` (that's ENT-SUBTODO, which depends on this task).

## Execution notes

**ID scheme chosen:** `sub_` prefix + 8 random bytes (crypto/rand) hex-encoded,
e.g. `sub_a1b2c3d4e5f60718`. Rationale:

- Every other entity's `Next*ID` generator (`NextTaskID`, `NextSprintID`,
  `NextEpicID`, `NextProjectID`, `NextCollectionID` in
  `internal/persistence/sqlstore/*.go`) uses a `PREFIX-YYYYMMDD-NNNN`
  date-bucketed sequential scheme backed by a `SELECT MAX(...)` query against
  a dedicated SQL table/column. That pattern exists to solve two problems
  Subtodo doesn't have: global uniqueness across a queryable table, and
  human-friendly chronological sort order for a cross-entity list surface.
  Subtodos are stored as a JSON blob on the parent task row (see
  `internal/persistence/sqlstore/subtodos.go`), are explicitly task-scoped
  only (ADR-0004 §1 item 4, no cross-task query surface — see Out of scope
  above), and `AddSubtodo` already has the full existing-items slice in hand,
  so no DB round-trip is needed to generate or de-dupe an id.
- Instead, the new `newSubtodoID()` helper in `internal/service/task.go`
  mirrors `internal/runtime/scheduler.newEventID()` — the codebase's existing
  precedent for a self-contained, non-DB-backed id: `crypto/rand` bytes,
  hex-encoded, under a fixed prefix, with a `time.Now().UnixNano()` fallback
  in the astronomically unlikely case `rand.Read` errors.
- The `sub_` prefix is a deliberate, distinct namespace so a generated id can
  never collide with a caller-supplied slug-style id (e.g.
  `"check-auth-flow"`), per the acceptance criteria's suggested approach.
  `AddSubtodo` also defensively regenerates on the (vanishingly unlikely)
  event of a collision against the existing checklist rather than erroring.

**What changed:**

- `internal/service/task.go`: `TaskService.AddSubtodo` no longer requires
  `item.ID`; when empty it generates one via the new `newSubtodoID()` helper
  (collision-checked against the existing checklist via new
  `subtodoIDTaken()` helper). Explicit-id callers are unaffected — duplicate
  checking still applies. Added `crypto/rand`, `encoding/hex`, `fmt`, `time`
  imports for the generator.
- `internal/mcpadapter/subtodo_tools.go`: `torque_task_subtodo_add`'s `id`
  param dropped `mcp.Required()`; description updated with an omitted-id
  example and now documents auto-generation.
- `internal/mcpadapter/loopback.go`: the loopback-context sibling tool
  (`handleLoopbackSubtodoAdd`) had its own independent `id == ""` required
  check and `mcp.Required()` marker — both removed to match, since this
  handler doesn't go through the cross-task tool's validation path.
  Description updated the same way.
- `internal/httpserver/subtodos.go`: no change needed — `addSubtodo` already
  passed `body.ID` straight through to `TaskService.AddSubtodo` with no
  independent required check, so it inherits the new optional-id behavior
  for free.
- Tests updated/added:
  - `internal/service/subtodo_service_test.go`: replaced
    `TestTaskService_AddSubtodoRejectsMissingFields` (which asserted a
    missing-id error that's no longer true) with
    `TestTaskService_AddSubtodoRejectsMissingText` (missing-text still
    errors), plus new `TestTaskService_AddSubtodoOmittedIDAutoGenerates` and
    `TestTaskService_AddSubtodoExplicitIDStillWorks`.
  - `internal/mcpadapter/subtodo_tools_test.go`: added
    `TestSubtodoAdd_OmittedIDAutoGenerates` (add with no `id`, assert
    `sub_`-prefixed id returned, then use it in a follow-up `..._done` call).
  - `internal/mcpadapter/loopback_test.go`: replaced
    `TestLoopback_SubtodoAddRequiresIDAndText` (asserted a missing-id error
    on the loopback tool) with `TestLoopback_SubtodoAddRequiresText` (text
    is now the only required field) and new
    `TestLoopback_SubtodoAddOmittedIDAutoGenerates`.
  - `internal/mcpadapter/descriptions_test.go` incidentally exercised: the
    Phase C description-inventory test requires every tool description to
    contain a literal `Example:` fragment; the first description edit used
    `Example (explicit id):` / `Example (auto id):` labels which broke that
    assertion — fixed by keeping a bare `Example:` line and adding a second
    `Example, id omitted (server generates one):` line.

**Test results:** `go build ./...`, `go vet ./...`, and `go test -count=1
./...` (every package in the module, not just `internal/service` and
`internal/mcpadapter`) all pass with no failures.

**Issues/blockers:** None. One incidental finding during setup: this worktree
was branched before `main`'s `docs(mcp-ergonomics)` commit (c58dd2a) that
added the `tasks/` directory, so `tasks/phase-1-bugfixes/FIX-002-*.md` did
not exist here initially — it was pulled in read-only via `git show
c58dd2a:tasks/phase-1-bugfixes/FIX-002-subtodo-id-autogen.md` (the commit is
present in the shared object store across worktrees) rather than merging all
of `main`'s new `tasks/` content, per instructions to touch only this one
task file.
