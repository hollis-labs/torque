# FIX-006 — Merge torque_task_search into torque_task_list (finish the ADR-0004 "one way to find things" merge for Task)

**Phase:** 5 — Consistency sweep (spun out of SWEEP-001)
**Status:** todo
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

- [ ] `torque_task_search` tool registration and handler removed;
      `torque_task_list`'s `search` param is the only way to free-text
      search tasks (matching Issue's already-merged shape).
- [ ] A registration-check test (mirroring ENT-ISSUE's) confirms
      `torque_task_search` is gone from the tool list.
- [ ] Every existing behavior `torque_task_search`'s test suite verified
      that's still meaningful (byte-cap behavior, default-exclude of
      `kind=internal`, project/sprint/epic_id + tags + manual filter
      combination with free-text search) has an equivalent assertion against
      `torque_task_list` — no coverage silently dropped, but redundant
      tests (duplicating what `torque_task_list`'s own test suite already
      asserts) may be retired rather than ported 1:1.
- [ ] `docs/task-tagging-conventions.md` no longer references
      `torque_task_search`.
- [ ] `go build ./...` and `go test ./...` clean.

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
