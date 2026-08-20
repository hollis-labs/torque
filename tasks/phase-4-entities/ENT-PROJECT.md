# ENT-PROJECT — Project: get/update (new), create fields, list wiring, archive

**Phase:** 4 — Per-entity rollout
**Status:** done
**Depends on:** FIX-004, PRIM-001, PRIM-002, PRIM-004, DEC-002
**Blocks:** SWEEP-001
**Source:** ADR-0004 §5 (Project); audit "Per-Entity Findings → Project
[high][high]", "Executive Summary #8", "Proposed Direction E"

## Summary

Project is "underbuilt relative to its sibling entities" — per the audit,
likely "the highest-priority item in this whole list": no
`torque_project_get`, no `torque_project_update` exist at all, despite both
`ProjectService.Get`/`.Update` already being implemented and fully
functional at the service layer.

## Scope

**New tools (the headline gap)**:
- `torque_project_get` — fetch a single project's full record. Does not
  exist today at all.
- `torque_project_update` — edit any field (name, description, repo_path,
  agent_path, path arrays, permissions, rules, icon, status) without
  delete+recreate. Does not exist today at all. True partial-patch
  semantics (presence-in-payload), matching the design principle used
  system-wide.

**`create` field expansion**: `agent_path`, `icon`, and all path/permission
arrays are already settable in `ProjectCreateInput` but absent from
`torque_project_create`'s MCP schema (`project_tools.go:12-21`) — expose
them.

**`list` wiring**: `torque_project_list` currently hardcodes
`Project.List("")` — the `Status` field `ProjectFilter` already supports is
unused (`project_tools.go:56`). Wire it. Apply PRIM-001 (pagination — no
limit param today) and PRIM-002 (sort — apply the FIX-004 docstring fix if
it hasn't landed yet for this entity, don't duplicate).

**Archive/unarchive**: apply PRIM-004's generic archive primitive here —
`archived_at` + `archive`/`unarchive` tools. Previously moot since `Update`
(which could flip `status`) wasn't reachable via MCP at all; now that
`update` exists (this task), archive as an orthogonal concept becomes
meaningful.

**Registration**: apply whatever DEC-002 decided about feature-flag gating
vs always-on for this entity's tools.

## Acceptance criteria

- [x] `torque_project_get` exists, returns the full project record by id.
- [x] `torque_project_update` exists, true partial patch, covers every
      field listed above.
- [x] `torque_project_create` accepts `agent_path`, `icon`, and the
      path/permission arrays.
- [x] `torque_project_list` actually filters by `status` when passed, and
      supports pagination + sort per PRIM-001/PRIM-002.
- [x] `archive`/`unarchive` tools exist and work per PRIM-004's pattern.
- [x] Tool registration matches DEC-002's decision.

## Out of scope

- Project's artifact-scoped CRUD surface (`ProjectService.{List,Create,
  Get,Update,Delete}Artifact`, 5 methods, zero MCP exposure per the audit)
  — this is Artifact-entity-shaped work, and Artifact is explicitly out of
  ADR-0004's scope. Do not expose these here even though they live on
  `ProjectService`; they belong to Artifact's own future pass.

## Execution notes

**Worktree/dependency mismatch found and resolved first.** This worktree
branched from a point in `main` that predated FIX-004/PRIM-001/PRIM-002/
PRIM-004/DEC-002 landing (those merges existed only on the local `main`
branch and sibling worktrees, 33 commits ahead of this worktree's original
HEAD). `tasks/`, `docs/adr/0004-*.md`, and `docs/architecture/mcp-service-
layer-audit.md` were also absent from this worktree for the same reason.
Fast-forward-merged local `main` (`git merge --ff-only main`, no conflicts
beyond an untracked stub `tasks/phase-4-entities/ENT-PROJECT.md` created
before the merge, which was removed first) to pick up all five declared
dependencies before starting implementation. No new commits were needed on
`main`'s side — this was a pure catch-up.

**`torque_project_get`** (new): fetches by id via `ProjectService.Get`,
returns the full `ProjectRecord`. `not_found` on unknown id (existing
string-match tier in `mapServiceError` already covers the store's
`"project %s not found"` shape).

**`torque_project_update`** (new): true partial-patch, presence-in-payload
for every field — `name`, `description`, `repo_path`, `agent_path`, `icon`,
`status` (scalars, presence-checked against `req.GetArguments()`, not
value-based), plus `read_paths`/`write_paths`/`context_paths`/`rules`
(JSON-array-of-strings, tolerant of the raw-JSON-string/`[]any` shapes via
`reqStrSlice`, re-marshaled to canonical JSON; an explicit empty array
clears the column) and `permissions` (JSON-object-of-strings, stored as-is
once validated; empty string clears). `repo_path` cannot be cleared to
empty — `ProjectService.Update` already rejects that with `arg_invalid`/
`repo_path`, unchanged. No-op payload (id only) returns `updated: false`
without a store call, matching Epic/Collection's convention.

**`torque_project_create` field expansion**: schema gained `agent_path`,
`icon`, `read_paths`, `write_paths`, `context_paths`, `permissions`,
`rules` — all pass straight through to the already-implemented
`ProjectCreateInput` fields.

**`torque_project_list` wiring**: `status` filter now actually reaches
`ProjectFilter.Status` (was hardcoded empty). Added PRIM-001/PRIM-002
cursor pagination + sort (`sort_by`: name|status|updated_at|created_at,
default `name`/`asc`, matching the tool's historical documented order) and
`include_archived`, mirroring `torque_task_list`'s reference
implementation. This required extending `sqlstore.ProjectFilter` with
`SortBy`/`SortDir`/`AfterSortValue`/`AfterID` and `ListProjects`'s query
builder with `projectSortColumn`/`projectCursorArg` (new,
`internal/persistence/sqlstore/projects.go`) — Task's `ListTasks` was the
template. `SortBy == ""` still falls back to the original hardcoded
`ORDER BY name ASC`, so HTTP (`/api/v1/projects`) and any other caller that
hasn't adopted the primitive are unaffected. Added
`ProjectService.ListPage(sqlstore.ProjectFilter)` as a sibling to the
existing `List(status, includeArchived)` rather than changing `List`'s
signature — `List` is still used by `internal/httpserver/projects.go` and
three service-layer tests; changing it would have forced an out-of-scope
HTTP-layer touch.

**`archive`/`unarchive`**: wired `torque_project_archive`/
`torque_project_unarchive` onto PRIM-004's already-built
`ProjectService.Archive`/`.Unarchive`; `include_archived` exposed on
`torque_project_list` (see above). Descriptions needed 4 lines minimum
(not 3) to satisfy `descriptions_test.go`'s Phase C contract check, caught
by the existing `TestDescriptionInventory_AssertsMinimumsAndEmitsArtifact`
test — expanded both beyond the initial draft.

**Registration**: per DEC-002 (decision 1, "keep current gating"), both
new tools and the archive pair register inside the existing
`registerProjectTools()`, called from `registerOptInTools()` unchanged —
no new gating mechanism.

**Tests added**: `internal/persistence/sqlstore/projects_test.go` (default
order preserved without `sort_by`, cursor tiebreak-on-id over duplicate
`status` values, `updated_at` cursor round-trip, invalid-cursor-value
error, archived-row exclusion interacting with a sorted page) and a new
`internal/mcpadapter/project_tools_test.go` (get + not_found, create field
round-trip, update true-partial-patch semantics including the omit-vs-
explicit-empty distinction and the repo_path-can't-clear/invalid-status
rejections, list status filter + full cursor walk + sort_dir=desc +
invalid sort_by + cross-sort_by cursor rejection, archive/unarchive
round-trip including default-list exclusion and `not_found` on an unknown
id).

**Verification**: `go build ./...` and `go test ./...` (full repo) both
clean; `gofmt -l` and `go vet ./...` clean on all touched files.

**Known limitations / out of scope not otherwise noted**: `ProjectUpdate`
zero-value semantics for the JSON columns match Task's own convention
(`buildTaskUpdateInput`) rather than inventing a new one — an explicit
empty array/object is indistinguishable from "no elements", both clear the
column; there is no way to distinguish "set to an empty array" from "clear"
at this layer, same limitation Task already has for its own blob fields.
