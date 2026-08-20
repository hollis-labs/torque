# FIX-006 — Merge torque_task_search into torque_task_list (finish the ADR-0004 "one way to find things" merge for Task)

**Phase:** 5 — Consistency sweep (spun out of SWEEP-001)
**Status:** done
**Depends on:** none (ENT-TASK, ENT-ISSUE already landed; Issue's merge is the
reference pattern to follow)
**Blocks:** none
**Source:** ADR-0004 §2 item 1 ("one way to find things"), §3 ("Search folds
into list where it's the same query"), Consequences section; discovered by
`tasks/phase-5-consistency/SWEEP-001-envelope-and-error-taxonomy-audit.md`
(scope item 5, "one way to find things")

## Summary

ADR-0004 explicitly calls for merging every entity's `_list`/`_search` tool
pair into one tool, and names `torque_task_search` directly in its own
Consequences section as a tool that removal was expected to affect:
"merging `_list`/`_search` pairs removes tools (`torque_task_search`,
`torque_issue_search`)". Issue's merge landed in Phase 4 (ENT-ISSUE,
`torque_issue_search` is gone, folded into `torque_issue_list`'s optional
`query` param). Task's never did — `torque_task_search` is still a fully
separate, registered MCP tool
(`internal/mcpadapter/task_tools.go:256`,`handleTaskSearch`).

This was caught during SWEEP-001 (Phase 5's cross-entity consistency audit),
which assumed — per its own scope text ("Task and Issue were explicitly
merged in Phase 4") — that this had already happened for both. It hadn't.
No Phase 4 task file (`ENT-TASK.md`) ever had this in scope; ENT-TASK's own
scope section only covers filters/bulk/create-fields/transition-comment, not
the list/search merge.

**Why this is real drift, not just a missing nice-to-have:** `handleTaskList`
already has a `search` param (`internal/mcpadapter/task_tools.go:164`,
"Substring match on title + description") that runs through the exact same
`a.svc.Task.List(filter)` call `handleTaskSearch` uses
(`internal/mcpadapter/task_tools.go:1132`) — `torque_task_search` is a
strict subset of `torque_task_list`'s capability today (no cursor
pagination, no sort_by, none of ENT-TASK's new filters — project_id/
sprint_id/epic_id/tags/manual only), just with a different default limit
(25/max 100 vs list's 50/max 200) and a required `query` param instead of an
optional `search` param.

## Scope

- Remove the `torque_task_search` tool registration and its handler
  (`registerTaskTools`'s block at `task_tools.go:256-271`,
  `handleTaskSearch` at `task_tools.go:1107-1137`) — `torque_task_list`'s
  existing `search` param already covers the same query.
- Update `torque_task_list`'s docstring: drop "prefer torque_task_search for
  free-text queries" (`task_tools.go:142`) — replace with guidance to pass
  `search` directly on `torque_task_list`.
- Remove now-dead `defaultTaskSearchLimit`/`maxTaskSearchLimit` constants
  (`response.go:27-28`) if nothing else references them after the handler is
  gone (confirm via grep — Plan's `handlePlanListChildren`/`tasksToEnvelope`
  reuses `tasksToEnvelope` but not these two constants, so they should be
  safe to remove, but verify before deleting).
- Follow Issue's merge pattern (`git show 304663a`, ENT-ISSUE's execution
  notes) as the template: that merge's "old dual-tool redundancy removed"
  acceptance criterion and its `TestFullStack_...` registration-check test
  (asserting `torque_issue_search` is gone while the surviving tool exists)
  are the right shape to replicate for Task.
- **Test migration** (the actual size of this ticket): `internal/mcpadapter/
  response_test.go` and `internal/mcpadapter/task_tools_test.go` currently
  call `torque_task_search` directly in ~11 places, several asserting
  `torque_task_search`-specific numeric defaults that differ from
  `torque_task_list`'s (25/max 100 vs 50/max 200):
  - `TestTaskSearch_Default25Limit_FitsUnderCap`,
    `TestTaskSearch_MaxLimitCapped` (response_test.go) assert the 25/100
    search-specific limits — decide whether these become
    `torque_task_list`-flavored equivalents asserting *its* 50/200 defaults,
    or are retired as redundant with `TestTaskList_500Tasks_FitsUnderCap`
    (which already covers list's cap behavior at limit=200).
  - `TestBeforeAfter_ByteCounts` (response_test.go) uses `torque_task_search`
    purely as a byte-count vehicle (brief vs verbose) — trivially portable
    to `torque_task_list` with a `search` param.
  - `TestFullStack_SearchTasks`, `TestFullStack_TaskList_CombinedSearchAndProjectID`
    (already list-based — no change needed),
    `TestFullStack_TaskSearch_WithSprintID`,
    `TestFullStack_TaskSearch_EmptyQueryReturnsError` (task_tools_test.go) —
    the last one tests a behavior `torque_task_list` doesn't have and isn't
    expected to (`search=""` on `torque_task_list` just means "no substring
    filter," which is correct list semantics, not an error) — decide whether
    to drop that assertion entirely or repurpose it to confirm the *new*
    (correct) no-op-filter behavior instead of an error.
  - The `include_internal` default-exclude pair at task_tools_test.go:304-311
    (comment: "torque_task_search mirrors the same default-exclude") needs
    its `torque_task_search` call ported to `torque_task_list`.
- Update `docs/task-tagging-conventions.md:5`'s reference to
  `` torque_task_list`/`torque_task_search`'s `tags` filter `` (drop the
  second tool name).
- Do **not** edit historical/already-`done` task files that merely mention
  `torque_task_search` as a past-tense fact (`FIX-004`, `PRIM-001`,
  `ENT-SPRINT`, the audit doc, ADR-0004 itself, `docs/superpowers/**` design
  docs) — those are historical record, not live specs, and predate this
  removal.

## Acceptance criteria

- [x] `torque_task_search` tool registration and handler removed;
      `torque_task_list`'s `search` param is the only way to free-text
      search tasks (matching Issue's already-merged shape).
- [x] A registration-check test (mirroring ENT-ISSUE's) confirms
      `torque_task_search` is gone from the tool list.
- [x] Every existing behavior `torque_task_search`'s test suite verified
      that's still meaningful (byte-cap behavior, default-exclude of
      `kind=internal`, project/sprint/epic_id + tags + manual filter
      combination with free-text search) has an equivalent assertion against
      `torque_task_list` — no coverage silently dropped, but redundant
      tests (duplicating what `torque_task_list`'s own test suite already
      asserts) may be retired rather than ported 1:1.
- [x] `docs/task-tagging-conventions.md` no longer references
      `torque_task_search`.
- [x] `go build ./...` and `go test ./...` clean.

## Out of scope

- Any change to `torque_task_list`'s existing filter/sort/pagination surface
  beyond what's needed to absorb search's former callers (it already has
  everything search offered and more).
- Editing historical Phase 0-4 task files that reference
  `torque_task_search` in past tense — leave those as the historical record
  they are.
- Any other entity's list/search shape — Comment's `list`/`search` split is
  ADR-0004's one deliberate, permanent exception (different query shapes);
  every other entity was already confirmed merged during SWEEP-001.

## Execution notes

**Worktree was stale at start.** This worktree branched from `main` before
SWEEP-001/ENT-COMMENT/ENT-EPIC/ENT-ISSUE (and `tasks/`, ADR-0004 itself) had
landed — `git log` topped out at "Docs sync" with none of that history.
Fast-forward merged local `main` (`d96d265`) into the branch first
(`git merge --ff-only main`); no conflicts, no worktree-local commits existed
yet.

**Removal.** Deleted the `torque_task_search` tool registration block and
`handleTaskSearch` (`internal/mcpadapter/task_tools.go`), plus the now-dead
`defaultTaskSearchLimit`/`maxTaskSearchLimit` constants
(`internal/mcpadapter/response.go`) — grep confirmed no other caller
referenced them (`tasksToEnvelope`, reused by `handlePlanListChildren`, uses
its own `defaultGenericListLimit`/`maxTaskListLimit`, not the search-specific
pair). `torque_task_list`'s `search` param already covered every filter
`torque_task_search` offered (project_id/sprint_id/epic_id/tags/manual/
include_internal), so no new filter plumbing was needed on the list side —
this was a pure subtraction, unlike ENT-ISSUE's merge which also had to
consolidate two divergent service-layer methods. Left
`internal/service/task.go`'s `Search`/`sqlstore.Store.SearchTasks` and
`internal/httpserver/tasks.go`'s HTTP `/search` endpoint untouched — the
task's Scope section only names the MCP tool layer, and unlike Issue's merge
commit (304663a), FIX-006 never asked for an HTTP/service-layer
consolidation.

**Docstrings.** `torque_task_get` and `torque_task_list` no longer say
"prefer torque_task_search" / "prefer torque_task_list/search" — replaced
with guidance to pass `search` directly on `torque_task_list`. Also updated
a stale comment in `internal/persistence/sqlstore/tasks.go` (TaskFilter's
`ExcludeInternal` doc) that named `torque_task_search` as one of the
default-exclude boundaries; not explicitly in scope but a one-line
correction of a comment that would otherwise reference a now-nonexistent
tool. Updated `docs/task-tagging-conventions.md:5` to drop the
`torque_task_search` half of the `tags`-filter reference.

**Registration-check test.** Added
`TestFullStack_TaskSearchToolRemoved` (`internal/mcpadapter/task_tools_test.go`),
mirroring ENT-ISSUE's `TestFullStack_IssueSearchToolRemoved` exactly: asserts
`torque_task_search` is not registered and `torque_task_list` is, via the
shared `toolIsRegistered` helper (`opt_in_tools_test.go`).

**Per-test decisions** (the ~11 `torque_task_search` call sites named in
Scope):

- `TestTaskSearch_Default25Limit_FitsUnderCap`,
  `TestTaskSearch_MaxLimitCapped` (`response_test.go`) — **ported**, not
  retired. Renamed to `TestTaskList_Default50Limit_FitsUnderCap` /
  `TestTaskList_MaxLimitCapped`, switched to `torque_task_list` +
  `search` param, and re-asserted against list's own 50/200 defaults
  (not search's old 25/100). Kept rather than treated as redundant with
  `TestTaskList_500Tasks_FitsUnderCap` because that test only exercises an
  *explicit* `limit=200`; nothing else in the suite exercised
  `torque_task_list`'s implicit default-limit (omitted `limit` → 50) or its
  clamp-down behavior on an over-max request — genuine, previously-search-only
  coverage that would have silently disappeared.
- `TestBeforeAfter_ByteCounts` (`response_test.go`) — **ported** verbatim as
  described in Scope: both calls switched from `torque_task_search`
  `query` to `torque_task_list` `search`; brief-vs-verbose byte-count
  assertion (`briefBytes*5 < verboseBytes`) unchanged and still passes
  (18033 vs 101863 bytes on the 80-task/6KB-description fixture).
- `TestFullStack_SearchTasks`, `TestFullStack_TaskList_CombinedSearchAndProjectID`
  (`task_tools_test.go`) — the former **ported** (renamed
  `TestFullStack_TaskList_Search`, `query`→`search` on `torque_task_list`);
  the latter needed **no change** (already `torque_task_list`-based, per
  Scope's own note).
- `TestFullStack_TaskSearch_WithSprintID` — **ported**, renamed
  `TestFullStack_TaskList_SearchWithSprintID`, `torque_task_search`
  `{query, sprint_id}` → `torque_task_list` `{search, sprint_id}`.
- `TestFullStack_TaskSearch_EmptyQueryReturnsError` — **repurposed**, not
  dropped. `torque_task_search` required a non-empty `query` and errored
  otherwise; `torque_task_list`'s `search` has no such requirement (empty
  search = no substring filter = correct list semantics, not an error).
  Renamed `TestFullStack_TaskList_EmptySearchIsNoOpFilter`: seeds one task,
  calls `torque_task_list` with `search=""`, asserts no error and the task
  is still returned — confirms the new correct behavior instead of porting
  an assertion that would now be wrong.
- The `include_internal` default-exclude pair at (former)
  `task_tools_test.go:304-311` inside
  `TestFullStack_TaskList_DefaultExcludesInternal` — **ported** in place:
  both `torque_task_search` calls (bare and with `include_internal=1`)
  switched to `torque_task_list` with `search` in the same test function
  (no rename needed; the test was already named for `torque_task_list`).

No coverage gap: every filter combination `torque_task_search`'s test suite
exercised (byte-cap at default/max limits, brief-vs-verbose byte delta,
default-exclude of `kind=internal` with and without opt-in, `project_id`/
`sprint_id` combined with free-text search) has an equivalent assertion
against `torque_task_list` post-merge. (`epic_id`+search and `tags`/`manual`
combined-with-search were never covered by `torque_task_search`'s own test
suite either — confirmed via `git show 304663a^:.../task_tools_test.go`, the
last commit before Issue's merge touched the same file — so there was no
existing coverage to preserve there.)

**Verification.** `go build ./...` and `go test ./...` both clean (full
suite, all packages, no `-short`/`-run` filtering). `go vet` clean on the
touched packages.

**Files touched:** `internal/mcpadapter/task_tools.go`,
`internal/mcpadapter/response.go`, `internal/mcpadapter/task_tools_test.go`,
`internal/mcpadapter/response_test.go`, `internal/persistence/sqlstore/tasks.go`,
`docs/task-tagging-conventions.md`.
