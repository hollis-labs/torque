# SWEEP-001 — Envelope + error-taxonomy consistency pass across all 8 entities

**Phase:** 5 — Consistency sweep
**Status:** done
**Depends on:** ENT-TASK, ENT-SUBTODO, ENT-COMMENT, ENT-PROJECT, ENT-EPIC,
ENT-SPRINT, ENT-ISSUE, ENT-PLAN (all Phase 4 tasks)
**Blocks:** none
**Source:** ADR-0004 §3 (Response envelope), §5 (per-entity design
principles: "one way to find things," "update is always a true partial
patch")

## Summary

Eight entities implementing the same primitives independently (across
different Phase 4 tasks, likely different sessions/agents) creates real
risk of subtle drift — one entity's `bulk_update` returning a slightly
different shape, one list tool's `meta` missing a field another has. This
task is the final pass confirming the whole surface actually landed
consistent, not just "each entity individually satisfies its own task
file."

## Scope

For each of Task, Subtodo, Comment, Project, Epic, Sprint, Issue, Plan,
verify:

- **List envelope**: every list-shaped tool's `data` is exactly
  `{items, meta: {returned, limit, total_count|has_more, next_cursor,
  hint?}}` — no entity-specific variation in field names or shape.
- **Bulk response shape**: every bulk tool (`bulk_update`, `bulk_delete`,
  `bulk_tag`, `bulk_transition` where applicable) returns exactly
  `{succeeded: [...], failed: [{id, error}]}`.
- **Error taxonomy**: every write path maps failures into
  `arg_invalid`/`not_found`/`conflict`/`domain`/`permission`/`internal`
  correctly — spot-check at least one FK-miss-style failure per entity to
  confirm it maps to `not_found`, not falling through to `internal` (the
  audit found this exact bug already present for Artifact; confirm none of
  the 8 in-scope entities have the same failure mode after Phase 4 landed).
- **Update semantics**: every `update`/`bulk_update` across all 8 entities
  uses presence-in-payload detection consistently — spot check for any
  entity that regressed to value-based detection (the bug FIX-001 fixed for
  Task) when it implemented its own update path.
- **"One way to find things"**: confirm no entity ended up with a stray
  parallel `_list`/`_search` pair that should have been merged (Task and
  Issue were explicitly merged in Phase 4; Comment's `list`/`search` split
  is the one deliberate exception — confirm nothing else drifted into a
  similar accidental split).
- **Docstrings**: spot-check that every list tool's docstring still
  accurately describes its actual default order after all the Phase 4
  changes (this is the same class of bug FIX-004 fixed — confirm it wasn't
  reintroduced).

## Acceptance criteria

- [x] A single table (in this file's Outcome section, or a short follow-up
      doc) listing all 8 entities' list/bulk tool shapes side by side,
      confirming consistency or flagging drift found.
- [x] Any drift found is either fixed as part of this task (if small) or
      spun back out into a new task file appended to this index (if it
      needs its own scoped work) — don't silently note drift without
      acting on it.
- [x] Error-taxonomy spot-checks documented per entity (at least one
      `not_found`-shaped failure verified per entity).

## Out of scope

- New capability — this is a verification/consistency pass, not a place to
  add features that weren't already in scope for a Phase 4 task.

## Outcome

**Worktree was stale at start (same failure mode ENT-EPIC/ENT-COMMENT hit).**
This worktree's branch tip was `c57775d` ("Docs sync") — the pre-Phase-0
state, predating PRIM-001 through PRIM-004, FK-002/003, and all 8 ENT-*
rollouts. The local `main` worktree (a separate checkout at
`/Users/chrispian/dev/hollis-labs/apps/torque`) was at `ddd63ba`
("ENT-COMMENT done — Phase 4 complete"), 45 commits ahead with zero
divergent commits from this branch's base. Confirmed via `git log --all`
and `git merge-base --is-ancestor`, then brought this worktree's branch up
to `ddd63ba` (`git fetch . main:refs/heads/_main_snapshot && git reset
--hard _main_snapshot`, since this branch had no unique commits of its own
to preserve) before starting the audit. `tasks/`, `docs/adr/0004-*.md`, and
`docs/architecture/mcp-service-layer-audit.md` all came in through that
sync — none existed in the worktree's original state despite the task
brief describing them as already present.

### Consistency table (all 8 entities)

| Entity | List tool | List envelope (`items`/`meta` w/ `has_more`+`next_cursor` always present) | Bulk tools | Bulk shape | `_list`/`_search` split | Docstring order accurate |
|---|---|---|---|---|---|---|
| Task | `torque_task_list` | ✅ cursor (PRIM-001 reference impl) | `bulk_update`, `bulk_delete`, `bulk_tag`, `bulk_transition` | ✅ `{succeeded,failed}` via shared `bulkResult` | ❌ **drift found** — `torque_task_search` still exists as a separate tool, a strict subset of `list`'s `search` param, calling the identical `svc.Task.List(filter)`. ADR-0004's own Consequences section names `torque_task_search` as a tool the merge was expected to remove; Issue's equivalent was merged in Phase 4, Task's never was (not in ENT-TASK's scope). **Spun out to FIX-006** (test-migration surface too large for a same-task fix — 11+ call sites across 2 test files with differing numeric defaults). | ✅ "priority ASC, created_at ASC" (FIX-004) |
| Subtodo | `torque_task_subtodo_list` | N/A by design — bounded per-task checklist, not a top-level paginated collection (ADR-0004 §3 names only "the other 6" entities for cursor rollout, excluding Subtodo) | `bulk_add` | Deviates from canonical shape (expected/documented) — see bulk_add row below | N/A (no search concept) | N/A (no sort_by) |
| Comment | `torque_comment_list` + `torque_comment_search` | ✅ cursor, both tools | `bulk_add` | Deviates from canonical shape (expected/documented) — see bulk_add row below | ✅ deliberate exception per ADR-0004 §3 (different query shapes: per-entity chronological thread vs cross-entity recency search) | ✅ list "created_at ASC", search "created_at DESC" — both match code |
| Project | `torque_project_list` | ✅ cursor | none (not in ADR-0004 §5 target inventory for Project) | N/A | ✅ merged (no `_search` ever existed) | ✅ "name ASC" (FIX-004) |
| Epic | `torque_epic_list` | ✅ cursor | `bulk_update` | ✅ `{succeeded,failed}` via shared `bulkResult` | ✅ merged (no `_search` tool) | ✅ "updated_at DESC" (FIX-004, query changed to match) |
| Sprint | `torque_sprint_list` | ✅ cursor | `bulk_update` | ✅ `{succeeded,failed}` via shared `bulkResult` | ✅ no search added, by design (documented low-cardinality rationale in ENT-SPRINT) | ✅ "updated_at DESC" (FIX-004) |
| Issue | `torque_issue_list` | ✅ cursor | `bulk_update`, `bulk_transition` | ✅ `{succeeded,failed}` via shared `bulkResult` | ✅ merged in Phase 4 (ENT-ISSUE) — `torque_issue_search` confirmed gone | ✅ "priority ASC" (reuses Task's default) |
| Plan | `torque_plan_list` | ✅ cursor (reuses `taskListCursorEnvelope`) | none (not in ADR-0004 §5 target inventory for Plan) | N/A | ✅ dedicated `list` tool exists (was previously only reachable via `torque_task_list kind=plan`) | ✅ "priority ASC" (reuses Task's default) |

### Bulk `bulk_add` deviation: Subtodo vs Comment (scope item 2)

Both are legitimately documented adaptations of PRIM-003's canonical
`{succeeded: [...], failed: [{id, error}]}` shape (each creates new rows
rather than operating on pre-existing ids), but they are **not literally
consistent with each other**:

- **Subtodo** `bulk_add`: `succeeded` is `[id...]` (the resolved/generated
  subtodo id strings); `failed` is `[{id, error}]` where `id` is the
  caller-supplied id when given, else a positional placeholder (`item[N]`).
  Natural identity per item is a single scalar id (a subtodo within one
  task).
- **Comment** `bulk_add`: `succeeded` is `[<CommentRecord>...]` (full
  created records, not just ids); `failed` is `[{target: {entity_type,
  entity_id}, error}]`, keyed by the compound target rather than a scalar
  id. Natural identity per item is a *compound* key (`{entity_type,
  entity_id}`), since one call fans the same comment out across multiple
  different entities — there's no single scalar id to key on even
  internally (the adapter keys its own lookup map by
  `"entity_type:entity_id"`).

**Verdict: reviewed, not unified.** The two shapes diverge in a way that's
each independently justified by real structural differences (scalar vs
compound identity), not by carelessness — forcing them to match would mean
either stripping useful `CommentRecord` content down to bare ids (removing
information a broadcast-comment caller plausibly wants back inline) or
inventing an artificial delimited-string id for Comment that ADR-0004
doesn't call for. Not treated as a bug to fix; documented here as the
scope-2 verification this task asked for.

### Update semantics (presence-in-payload) — regressions found and fixed

Two entities had regressed to **value-based** field detection (`if v !=
""` / `if v > 0`) instead of presence-based (`reqHasArg`) on their
`update`/`bulk_update` paths — the exact bug class FIX-001 fixed for Task's
`title`/`description`/`priority`. Both were explicitly, deliberately
scoped out of their own Phase 4 tasks (ENT-SPRINT and ENT-EPIC's execution
notes both say so directly, anticipating this sweep would be the place to
fix them) — fixed here:

- **Sprint** (`buildSprintUpdate`, `internal/mcpadapter/sprint_tools.go`):
  `name`/`goal`/`approval_mode`/`cost_budget` were value-based (an explicit
  empty string/zero was silently dropped as "not provided";
  `cost_budget` could never be cleared/set to 0). Converted all four to
  `reqHasArg`. Added a `name`-cannot-be-cleared-to-empty guard to
  `SprintService.Update` (mirroring `TaskService.Update`'s title guard),
  since `name` becoming presence-based means an explicit `"name": ""` now
  reaches the service layer.
- **Epic** (`buildEpicUpdateInput`, `internal/mcpadapter/epic_tools.go`):
  `name`/`description`/`status` were value-based. Converted to
  `reqHasArg`. Added the same `name`-cannot-be-cleared-to-empty guard to
  `EpicService.Update`; `status=""` is already rejected by the existing
  `validEpicStatuses` check, no separate guard needed.

New regression tests: `internal/service/sprint_test.go`
(`TestSprintUpdateNameCannotBeCleared`,
`TestSprintUpdateCostBudgetExplicitZero`), `internal/mcpadapter/
opt_in_tools_test.go` (`TestSprintUpdateGoalClearViaMCP`,
`TestSprintUpdateCostBudgetZeroViaMCP`,
`TestSprintUpdateNameCannotBeClearedViaMCP`), `internal/service/
epic_test.go` (`TestEpicUpdateNameCannotBeCleared`,
`TestEpicUpdateDescriptionClear`), `internal/mcpadapter/epic_tools_test.go`
(`TestFullStack_EpicUpdate_DescriptionClear`,
`TestFullStack_EpicUpdate_NameCannotBeCleared`).

All other entities (Task, Comment, Project, Issue, Plan) confirmed already
presence-based throughout. Comment's `update` intentionally requires
`content` (no partial-patch ambiguity — there's exactly one mutable field,
so "provide it" is the only sensible contract; not a regression).

### Error taxonomy — `not_found` spot-check per entity

One `not_found`-shaped failure verified per entity, confirming correct
mapping (not falling through to `internal`):

| Entity | Path verified | Mechanism | Result |
|---|---|---|---|
| Task | unknown task id (`errors_integration_test.go`) | typed `sqlstore.ErrTaskNotFound` sentinel | ✅ `not_found` |
| Subtodo | unknown subtodo id on `update`/`delete` | **was broken** — see below | ✅ `not_found` (fixed) |
| Comment | unknown comment id (`comment_tools_test.go`) | typed `sqlstore.ErrCommentNotFound`, caught by string-match tier (not in the typed-sentinel list, but message contains `" not found"`) | ✅ `not_found` |
| Project | unknown project id (`project_tools_test.go`) | untyped `fmt.Errorf("project %s not found", id)`, string-match tier | ✅ `not_found` |
| Epic | unknown epic id (`epic_tools_test.go`) | untyped `fmt.Errorf("epic %s not found", id)`, string-match tier | ✅ `not_found` |
| Sprint | unknown sprint id — **no prior coverage, added** (`TestSprintGet_UnknownIDMapsToNotFound`) | untyped `fmt.Errorf("sprint %s not found", id)`, string-match tier | ✅ `not_found` |
| Issue | unknown task id in bulk_update (`issue_tools_test.go`) | typed `sqlstore.ErrTaskNotFound` (issues are Task rows) | ✅ `not_found` |
| Plan | unknown plan id (`plan_tools_test.go`) | typed `sqlstore.ErrTaskNotFound` (plans are Task rows) | ✅ `not_found` |

**Bug found and fixed: Subtodo `update`/`delete` misclassified as
`arg_invalid`.** `TaskService.UpdateSubtodo`/`DeleteSubtodo`
(`internal/service/task.go`) returned a bare `&ValidationError{Field: "id",
Message: "subtodo not found: " + itemID}` when the item id wasn't found in
the task's checklist. `mapServiceError`'s typed `*service.ValidationError`
check runs *before* the string-match `" not found"` tier, so this always
mapped to `arg_invalid` regardless of message text — exactly the failure
mode the original audit flagged for Artifact ("an FK-miss silently falling
through to `internal`" — here falling through to the wrong non-`internal`
code instead, but the same root cause: an untyped/mistyped error bypassing
the taxonomy). Fixed by returning `&NotFoundError{Entity: "subtodo", ID:
itemID}` instead — "the referenced entity does not exist, correct the id"
is exactly `not_found`'s definition, and unlike the empty-text validation
error on the same method (legitimately `arg_invalid`), an unknown id isn't
a caller-input-shape problem. `MarkSubtodoDone` (`torque_task_subtodo_done`)
was already correct — `sqlstore.SetSubtodoDone` returns an untyped error
that hits the string-match tier successfully. New tests:
`internal/service/subtodo_service_test.go`
(tightened `TestTaskService_UpdateSubtodo`/`DeleteSubtodo` to assert
`*service.NotFoundError`), `internal/mcpadapter/subtodo_tools_test.go`
(`TestSubtodoUpdateDelete_UnknownIDMapsToNotFound`, full-stack
`error.code=not_found` check).

### Docstrings (scope item 6)

Spot-checked every list tool's default-order docstring against its actual
query default (constants, not just prose): Task ("priority ASC, created_at
ASC"), Comment list ("created_at ASC")/search ("created_at DESC"), Project
("name ASC"), Epic ("updated_at DESC"), Sprint ("updated_at DESC"), Issue
("priority ASC"), Plan ("priority ASC"). All match their code (`*SortDefaultBy`/`*SortDefaultDir`
constants or, for Issue/Plan, Task's reused defaults). No FIX-004-class
regression found.

### Drift summary

| Finding | Class | Action taken |
|---|---|---|
| Sprint `update`/`bulk_update` value-based field detection | Update-semantics regression (FIX-001 class) | **Fixed directly** |
| Epic `update`/`bulk_update` value-based field detection | Update-semantics regression (FIX-001 class) | **Fixed directly** |
| Subtodo `update`/`delete` unknown-id → `arg_invalid` instead of `not_found` | Error-taxonomy bug (audit's Artifact-class finding) | **Fixed directly** |
| Sprint had zero `not_found` test coverage | Test-coverage gap | **Fixed directly** (added) |
| `torque_task_search` never merged into `torque_task_list` (Task's `_list`/`_search` split) | "One way to find things" violation, ADR-0004 §2/§3/Consequences | **Spun out** — `tasks/phase-5-consistency/FIX-006-task-list-search-merge.md` (test-migration surface too large: 11+ call sites, differing numeric defaults, across 2 test files) |
| Comment vs Subtodo `bulk_add` shape not literally identical | Shape difference | **Reviewed, not a bug** — each independently justified by real structural differences (scalar vs compound identity); documented above |

### Verification

`go build ./...` clean. `go test ./...` clean (full suite, all packages,
`(cached)` on the untouched ones, 10.6s fresh run on `internal/mcpadapter`).
`gofmt -l` clean on every changed file. `go vet ./...` clean.
