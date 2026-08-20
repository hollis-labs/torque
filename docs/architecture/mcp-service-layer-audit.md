---
intent: system_doc
audience: humans
---

# MCP Tool & Service Layer Audit — 2026-08-20

## Purpose

Torque's MCP surface (`internal/mcpadapter/`, ~104 registered tools) is the primary way agents
interact with the system today. This audit catalogues agent-DX gaps in that surface and in the
service layer it wraps (`internal/service/`), ahead of a dedicated hardening pass. Per the
request that triggered it, this is **documentation and architecture only** — no code changes.
A follow-up session will bring CLI/HTTP up to parity once the MCP + service-layer shape is
agreed.

Four things were explicitly in scope:
1. No hard result-count ceilings — real pagination instead.
2. Every entity filterable and sortable on its real facets/dimensions.
3. Bulk/batch operations where single-item-only exists today.
4. Convenience tools (move, archive, etc.) where the domain calls for them.
5. **The organizing constraint**: business logic lives in `internal/service/*.go`; `internal/mcpadapter` stays a thin wrapper over it.

## Methodology

The tool surface was split into four domain groups and audited in parallel: Task/Subtodo/
Checkpoint/Comment; Issue/Epic/Sprint/Plan/Project; Collection/Template/Artifact; and
Session/Run/Scheduler/Settings/Model/Broker. Each group read its `mcpadapter` tool files, the
backing `service/*.go` files, and the `sqlstore` filter/record structs, then evaluated against a
fixed 8-point rubric: tool inventory, pagination, filter/facet coverage, sorting, bulk/batch,
convenience tools, service/adapter alignment, and cross-entity consistency. Findings below are
deduplicated and cross-referenced across groups. All file:line references were verified against
the current `main` branch tree.

---

## Executive Summary

Eight themes recur across nearly every entity:

1. **Pagination is fake past page one.** Several stores (`tasks`, `collections`, `epics`,
   `sprints`, `projects`) already have working `Limit`/`Offset` support at the SQL layer — but
   almost nothing wires `Offset` through the service or MCP layer. Every list tool does a full
   (or `limit`-truncated) unbounded fetch and hands back `{truncated, returned, limit}` with
   **no `total_count` and no cursor** — an agent can never reliably ask for "the next page."
2. **Sort support is zero, system-wide.** No `OrderBy`/`SortBy` field exists anywhere in
   `sqlstore`. Every list is stuck with whatever `ORDER BY` its query happens to hardcode.
3. **Several list-tool docstrings actively misdescribe their own default order** — a
   correctness bug independent of the audit's pagination/sort ask. Confirmed on Task, Epic,
   Sprint, Project, and Artifact (details below).
4. **Bulk operations are almost nonexistent.** The only real bulk primitives in the entire
   system are `TaskService.BulkTransition` (status only), `CollectionService`'s task-reorder,
   and Sprint's `Start`/`ApproveAll` (sprint-scoped implicit bulk-promote). Every other
   create/update/delete is strictly single-item.
5. **Convenience-tool pairs are inconsistent.** Collection is the only entity with a clean
   archive/unarchive pair. Template has archive but no unarchive. Task has neither archive nor
   move (only raw field-edit `update`, which does cover "move" semantics — see per-entity notes).
6. **Service-layer alignment is strong for true domain entities, and cleanly *not* applied to
   three legitimate infra exceptions** (Scheduler, Broker, Model catalog) **— but Session is a
   fourth bypass that should not be an exception.** Session has a full CRUD-shaped lifecycle
   (create/get/list/stop/attach/checkpoint/resume) implemented directly against
   `runtime/agent.Manager` with no `service.SessionService`, unlike every comparable entity.
7. **Artifact is the weakest-aligned domain entity.** It's the only one of the group-3 trio that
   skips the input-DTO pattern Collection/Template use, has no field validation, and a missing
   FK maps to `error.code=internal` instead of `not_found`.
8. **Project is underbuilt relative to its sibling entities.** No `torque_project_get`, no
   `torque_project_update`, and a whole project-scoped artifact CRUD surface
   (`ProjectService.{List,Create,Get,Update,Delete}Artifact`) with zero MCP exposure.

---

## Cross-Cutting Findings

### 1. Pagination

| Entity | Store has `Offset`? | Wired to service? | Wired to MCP tool? |
|---|---|---|---|
| Task | Yes (`tasks.go:108`) | No — `handleTaskList`/`handleTaskSearch` never read an `offset` arg | No param in schema |
| Collection | Yes (`collections.go:30-34`) | No — `CollectionService.List` always passes `Offset: 0` | No |
| Epic | Yes (`epics.go:23-28`) | No — `EpicService.List` never sets it | No |
| Sprint | Yes (`sprints.go:26-30`) | No — `SprintService.List` never sets it | No |
| Project | Yes (`projects.go:29-33`) | No — `ProjectService.List` never sets it | No |
| Issue, Plan, Template, Artifact, Comment, Checkpoint, Subtodo, Run, Session | No `Offset` field exists at all | — | — |

Five filter structs carry `Offset` as dead code — real, tested SQL-level pagination that the
service and adapter layers simply never reach. This is the single cheapest, lowest-risk fix
available: wiring existing plumbing, not building new capability.

Independent of `Offset`, `internal/mcpadapter/response.go`'s `cappedJSONResult` applies a
**second, silent truncation** by response byte size (100KB cap, `maxMCPResponseBytes`) on top of
whatever `limit` was applied — a full page can still get chopped with only a generic
`"response too large; add filters or lower limit"` hint, no indication of how many records exist
beyond what's returned.

`RunService.ListFiltered` (`service/run.go:35-37`, `sqlstore/runs.go:168-174`) is the one
counter-example worth calling out: it already supports `TaskID, ProjectID, Statuses, Since,
Limit` and is **already used by HTTP** (`httpserver/runs.go:29-56`) — but `torque_run_list` only
calls the older single-arg `Run.List(taskID)` (`run_tools.go:30`). This is the cleanest
pagination/filter fix in the whole audit: swap one method call, done.

`torque_session_list`'s `meta.limit` is hardcoded to `defaultGenericListLimit` regardless of the
caller-supplied limit that was actually honored at the query level (`session_tools.go:193`) —
the response lies about what limit was applied. `model_tools.go:67,81` shows the correct pattern
(clamp once, reuse the clamped value in the query and the meta).

### 2. Sorting

Zero `OrderBy`/`SortBy` fields exist anywhere in `internal/persistence/sqlstore`. Every list is
permanently stuck at its query's hardcoded order. Several docstrings claim an order that isn't
what the query does:

| Tool | Docstring claims | Actual query order | Evidence |
|---|---|---|---|
| `torque_task_list` | "ordered updated_at DESC" | `priority ASC, created_at ASC` | `task_tools.go:83` vs `tasks.go:403` |
| `torque_epic_list` | "ordered updated_at DESC" | `created_at DESC` | `epic_tools.go:51` vs `epics.go:82` |
| `torque_sprint_list` | "ordered updated_at DESC" | `created_at DESC` | `sprint_tools.go:56` vs `sprints.go:97` |
| `torque_project_list` | "ordered updated_at DESC" | `name ASC` | `project_tools.go:24` vs `projects.go:90` |
| `torque_artifact_list` | "newest first" | `created_at ASC` (oldest first) | `artifact_tools.go:24` vs `artifacts.go:65-68` |

`torque_task_search`'s docstring, by contrast, correctly states "priority ASC, created_at ASC"
and matches — so the boilerplate appears to have been copy-pasted from a `_list` template that
was never checked against each entity's real query. This is a correctness bug independent of
adding real sort support, and worth fixing regardless of what else this audit leads to.

### 3. Filter/facet coverage

`TaskFilter` (`tasks.go:97-135`) is the richest filter struct in the system — `Status, Statuses,
Priority, SprintID, ProjectID, EpicID, Executor, TagSlugs, Search, Limit, Offset, Kind,
SourceType, SourceRef, Trust, CheckpointMode, ParentID, ParentIDNull, Manual, ExcludeInternal` —
but `torque_task_list`'s schema doesn't expose all of it (`Statuses`, the multi-status OR filter
HTTP already has via `httpserver/tasks.go:420`, has no MCP equivalent — only single `status`).
And several `TaskRecord` columns have **no filter support at any layer**, not just missing MCP
wiring: `cost_budget`, `token_budget`, `max_duration_ms`, `max_retries`, `agent_profile`,
`launch_profile`, `created_at`/`updated_at` ranges. "Show me over-budget tasks" or "tasks created
since yesterday" can't be built from the store up.

Other confirmed gaps:
- **Epic**: `Priority` exists on `EpicRecord`, `EpicCreateInput`, and `EpicUpdateInput`, and is
  even surfaced in `briefEpic` list responses (`response.go`) — but neither
  `torque_epic_create` nor `torque_epic_update` expose a `priority` param
  (`epic_tools.go:12-40`). An epic's priority can be read but never set via MCP.
- **Comment**: no date-range filter despite `CommentRecord.CreatedAt` existing; `EntityID` is
  single-valued only — "comments across every task in sprint X" isn't answerable.
- **Checkpoint**: no `CheckpointFilter` exists at any layer. Only `ListForTask(taskID)` and a
  global `ListPending()` (status='pending' only) — no filter by `type`, terminal status, or
  `run_id`.
- **Artifact**: `ListArtifacts` takes only `task_id` — no `type`, `run_id`, or date filter, and
  `RunID` exists on the record but is neither settable nor filterable via MCP.
- **Template**: only `Kind` + `IncludeArchived` are filterable — no `auto_execute`, `executor`,
  `launch_profile`, or `checkpoint_mode`.
- **Project**: `torque_project_list` hardcodes `Project.List("")` — the `Status` field
  `ProjectFilter` already supports is unused (`project_tools.go:56`).

### 4. Bulk/batch operations

The entire audited surface has exactly three bulk primitives:

- `TaskService.BulkTransition` (`service/task.go:720`) — status transitions only, but the
  **reference-good pattern**: bulk logic lives in the service, per-item partial-success result
  (`succeeded []string, errs []error`), adapter (`task_tools.go:704`) just thin-wraps it.
- `CollectionService`'s `torque_collection_task_reorder` — "bulk-rewrite collection_position for
  tasks within a single collection" (`collection_tools.go:79`) — a genuine bulk primitive for a
  narrow case.
- `SprintService.Start`/`ApproveAll` (`sprint_tools.go:74-80`, `65-72`) — sprint-scoped implicit
  bulk-promote / bulk-approve. Good precedent for "convenience bulk," worth reusing as a pattern
  elsewhere (e.g. an epic-scoped or plan-scoped bulk task-status op).

Everything else — task field-edit updates, task delete, subtodo add/done, checkpoint
emit/respond, comment add, artifact create/delete, template create/update/archive, issue
create/update, epic/sprint/project create/update/delete — is strictly single-item. Notably,
`RunService.Cancel/Kill/Supersede` (`service/run.go:54-71`) exist specifically "for operator-facing
surfaces (HTTP/MCP)" per their own doc comment but have **no MCP tool at all** (nor an HTTP
route — this is a cross-surface gap, not MCP-specific).

### 5. Convenience tools (move, archive, duplicate)

| Entity | Archive/unarchive | Move | Duplicate/clone |
|---|---|---|---|
| Collection | Clean pair (`archive`/`unarchive`) | `task_move` between collections exists | No |
| Template | Archive only — **no unarchive at any layer** (no store method, no service method, no tool) | N/A | No — versioning evolves forward via `update`, but no fork-to-new-id |
| Task | None (hard delete or `abandoned` status only) | Covered by plain `torque_task_update` (sprint_id/epic_id/project_id are just fields) — not a gap, just undocumented as "move" | No |
| Project | Hard delete only; `status` field exists but `torque_project_update` doesn't exist to reach it (see below) | N/A | No |
| Epic/Sprint | Hard delete + status field, no archive concept | N/A | No |

Template's one-way archive is the sharpest gap here: `ArchiveTemplate` exists
(`sqlstore/templates.go:212-223`) but nothing reverses it — a specific (id, version) is
permanently retired from the live catalog once archived, mirroring Collection's clean pair
exactly backwards.

### 6. Service-layer alignment

Confirmed backing layer for every tool file:

| File | Backing | Alignment |
|---|---|---|
| task/issue/epic/sprint/plan/project/collection/template/comment/checkpoint/subtodo/run/settings_tools.go | `service.Service` domain services | Aligned |
| `session_tools.go` | `*agent.Manager` directly (`a.sessions`) | **Gap** — full lifecycle CRUD with no `SessionService` |
| `scheduler_tools.go` | `*scheduler.Scheduler` directly (`a.sched`) | Legitimate exception — process-runtime infra, not durable domain state |
| `broker_tools.go` | `*broker.Broker` / `*steering.PollRegistry` directly | Legitimate exception — message-transport infra; its own validation already lives in `internal/broker`, not the adapter |
| `model_tools.go` | `*modelcatalog.Catalog` (raw field on `Service`, not a domain service by design) | Legitimate exception — read-only external reference data, no business rules to extract |

**Session is the one bypass that doesn't fit the "legitimate infra exception" bucket.** Unlike
Scheduler/Broker/Model, sessions are listable/filterable/queryable, referenced by
`task_id`/`project_id` like every other entity, and persist across the lifecycle exactly like a
domain record. The adapter handlers are mostly thin delegation already (light validation:
`session_tools.go:131-143`), so today it's "thin wrapper over a different backing package," not
"logic embedded in the adapter" — but there is no testable, HTTP-reusable `SessionService`, which
is precisely the gap the user's stated goal is aimed at closing. A concrete, low-risk motivating
example: `agent.Manager.ListCheckpoints` (`manager.go:445`) is a plain read call with no
streaming/PTY dependency, and it's currently unreachable from any MCP tool — an agent can create
a checkpoint (`torque_session_checkpoint`) but never list a session's checkpoint history.

`torque_plan_start` is a second, narrower instance of the same shape: it bypasses
`service.Service` to call `internal/planstart` directly with `a.sessions`
(`plan_tools.go:161-197`) — worth handling as part of the same Session-service decision rather
than separately.

**Artifact is the weakest-aligned true domain entity.** `handleArtifactCreate` imports
`sqlstore` directly and builds a raw `*sqlstore.ArtifactRecord{}` in the adapter
(`artifact_tools.go:1-8, 49-61`) — the only one of the Collection/Template/Artifact trio that
skips the input-DTO pattern (`CollectionCreateInput`, `TemplateCreateInput`) the sibling entities
use. `ArtifactService` itself is a 4-method, zero-logic pass-through (`service/artifact.go`).
Concretely: there's no `task_id` existence check (contrast Collection's
`assertTaskExistsTx`/`ErrTaskNotFound`, `collections.go:502-509`), so a bad `task_id` only fails
via the raw SQLite FK constraint, which `mapServiceError` (`errors.go:164-224`) doesn't
recognize — it falls through to generic `error.code=internal` instead of `not_found`. `type` also
has no validation against the documented `file|url|inline|diff|...` set.

### 7. Loopback-mode tool duplication

`internal/mcpadapter/loopback.go` and `loopback_aar.go` register a second, task-scoped tool set
(`torque_task_get`, `torque_comment_add`, `torque_task_subtodo_add`/`done`,
`torque_task_checkpoint_emit`/`respond`, plus loopback-only `torque_task_summary`/`_blocked`/
`_review`/`torque_steering_dismiss`/`torque_aar_submit`) with names identical to the core tool
set. **Confirmed intentional, not a naming collision**: `New()` and `NewLoopback()` build two
entirely separate `*server.MCPServer` instances (`"Torque"` vs `"Torque Loopback"`,
`adapter.go:70-80` vs `loopback.go:61-78`), and `internal/runtime/bootstrap/loopback.go:95-178`
gives every per-task/session loopback its own dedicated HTTP listener bound to exactly one
Adapter — no agent session ever sees both tool sets at once.

Every loopback handler correctly delegates to the same service-layer calls as its core
counterpart (verified pairwise: `handleLoopbackTaskGet` → `a.svc.Task.Get` +
the same `taskResult` helper as core; `handleLoopbackCheckpointEmit/Respond` →
`a.svc.Checkpoint.Emit/Respond` with an added ownership check layered on top, not reimplemented
logic). So there is **no business-logic drift** — but the `mcp.NewTool(...)` schema declarations
and response shaping are hand-duplicated in two files, a real ongoing maintenance cost (two
places to update by hand on any schema or response-shape change) even though functional risk is
low.

---

## Per-Entity Findings

Findings already covered in the cross-cutting section above aren't repeated here in full detail
— see the tables/prose above for Task/Epic/Sprint/Project sort-order bugs, the pagination table,
and the service-alignment table. This section adds entity-specific items not captured above.

### Task
- **[high]** No `offset` param anywhere despite full store support — see Pagination table.
- **[med]** No `Statuses` (multi-status OR) param on `torque_task_list`, unlike HTTP.
- **[low]** No duplicate/clone tool (templates cover the repeatable case; ad-hoc tasks don't).
- **[low]** No archive/soft-delete — only hard delete or `abandoned` status, inconsistent with
  Collection/Template's archive lifecycle.
- **[low]** `handleTaskCreate` still force-sets `manual=true` itself (`task_tools.go:279-291`)
  even though `TaskService.Create` now force-applies the identical rule authoritatively
  (`service/task.go:216-219`) — vestigial duplication, one more place to keep in sync for a rule
  already enforced downstream.

### Subtodo
- **[med]** Zero cross-task query surface — `torque_task_subtodo_list` is single-task only; no
  way to ask "every not-done required subtodo across my sprint."
- **[low]** No bulk add/done — matches the system-wide bulk gap.
- **[low]** `Subtodo` carries no author/timestamp (`ID, Text, Required, Done, Evidence` only,
  `subtodos.go:13-19`) — thinner audit trail than Comment or Checkpoint.

### Checkpoint
- **[high]** No `CheckpointFilter` at any layer — see Filter/facet section above.
- **[med]** `torque_task_checkpoints_pending` (the global response queue) has no `limit`/`offset`
  param and the underlying `ListPending()` query is unbounded before truncation
  (`checkpoint_tools.go:153-169`).
- **[low]** `CheckpointService.SweepTimedOut` (`service/checkpoint.go:605-607`) has no read-only
  MCP visibility into checkpoints approaching timeout.

### Comment
- **[high]** Comments are immutable and undeletable post-creation — no `Update`/`Delete` method
  exists at any layer (`service/comment.go`). The tool's own docstring already flags this
  (`comment_tools.go:24`), but there's still no correction path for a wrong or sensitive comment.
- **[med]** `torque_comment_list` has no `limit` param and the store query is unbounded before a
  hardcoded 100-item truncation (`comment_tools.go:68-92`).
- **[low]** No bulk comment add across multiple entities (e.g. broadcast a note to every task in
  a sprint).

### Issue
- **[med]** `torque_issue_list` does a full unbounded DB fetch then truncates in Go
  (`IssueService.List` never passes `Limit`) — inconsistent with `torque_issue_search`, which
  correctly pushes `limit` to the DB layer (`issue_tools.go:126-135` vs `138-146`).
- **[low]** No status filter on issue_list (issues start at `backlog` but can transition via the
  generic task surface; issue_list has no way to filter by current status).
- Service/adapter alignment is clean — `IssueService` correctly wraps `TaskService`, adapter is
  thin.

### Epic
- **[high]** **Confirmed correctness bug**, independent of this audit's ask: `torque_epic_update`'s
  own docstring and example (`{"id":"EP-4","status":"closed"}`, "open<->closed transitions,"
  `epic_tools.go:31-38`) will **always fail validation** — `EpicService.Update` only accepts
  `validEpicStatuses = {active, inactive}` (`service/epic.go:33-36`). The DB schema's own
  default is `status TEXT NOT NULL DEFAULT 'open'` (`migrations/001_initial.sql:127`), but
  `CreateEpic` overrides that to `"active"` in Go (`epics.go:41-43`) — so "open" is dead too.
  Either the tool description/example is stale, or the valid-status set needs to include
  open/closed — this should be resolved as a bug fix, not folded into the larger audit backlog.
- **[high]** `Priority` unreachable via MCP — see Filter/facet section above.
- **[med]** Docstring/order mismatch — see Sorting table.
- **[med]** Dead `Limit`/`Offset` in `EpicFilter` — see Pagination table.
- **[low]** No epic search (no analog to `torque_task_search`/`torque_issue_search`).

### Sprint
- **[med]** Dead `Limit`/`Offset` in `SprintFilter`; docstring/order mismatch — see tables above.
- **[low]** No filter by cost-budget range or "over budget."
- `Start`/`ApproveAll` are a genuinely good bulk-convenience pattern — see Bulk section. Service
  layer owns all logic cleanly; adapter is thin throughout.

### Plan
- **[med]** `torque_plan_list_children` hardcodes `maxTaskListLimit` (200) — the system's
  *maximum*, not a default — as the only limit, regardless of caller intent
  (`plan_tools.go:153`). It's the one list tool in the whole surface that hardcodes the ceiling
  rather than a smaller default.
- **[low]** No dedicated `torque_plan_list` — discovering plans means `torque_task_list` with
  `kind=plan`. Reasonable (mirrors how Issue layers on Task) but inconsistent with Issue getting
  its own dedicated list/search pair while Plan doesn't.
- **[low]** No `plan_update`/`plan_delete` — a plan's own title/description can only be reached
  via the generic `torque_task_update`, with no plan-specific kind-guard the way Issue's update
  has one.
- `torque_plan_start` bypasses `service.Service` the same way Session does — see Service-layer
  alignment section; treat as part of the same decision, not separately.
- Otherwise `PlanService` is one of the better-composed services in the codebase (versioned
  metadata, observer hook, clean `Get`/`Progress` separation) — adapter handlers are thin
  throughout.

### Project
- **[high]** **No `torque_project_get` and no `torque_project_update` tool exist at all** —
  `registerProjectTools()` only registers create/list/delete (`project_tools.go:11-38`).
  `ProjectService.Get` and `.Update` are both implemented (`service/project.go:97,113`) but
  completely unreachable via MCP: an agent can't fetch a single project's full record, nor edit
  any field (name, description, repo_path, agent_path, path arrays, permissions, rules, icon,
  status) without deleting and recreating it. The list docstring admits the `get` gap
  ("no dedicated project_get tool," `project_tools.go:25`) but the `update` gap isn't
  acknowledged anywhere.
- **[high]** Five `ProjectService` artifact methods (`ListArtifacts, CreateArtifact, GetArtifact,
  UpdateArtifact, DeleteArtifact` — `service/project.go:148-214`) — a full project-scoped
  document/artifact CRUD surface, distinct from the task-scoped Artifact entity — have **zero
  MCP exposure**.
- **[med]** `torque_project_list` hardcodes the empty status filter and has no limit param — see
  Filter and Pagination sections.
- **[med]** Docstring/order mismatch — the largest gap of the group (`updated_at DESC` claimed,
  `name ASC` actual) — see Sorting table.
- **[low]** `agent_path`, `icon`, and all path/permission arrays are settable in
  `ProjectCreateInput` but absent from `torque_project_create`'s MCP schema
  (`project_tools.go:12-21`).
- **[low]** No archive/soft-delete — moot today since `Update` (which could flip `status`) isn't
  reachable via MCP at all.

### Collection
- **[high]** No caller-adjustable `limit` param on any of `torque_collection_list`,
  `_tasks_list`, or `_inbox_list` (`collection_tools.go:22-28,108-113,102-106`) — all three
  hardcode a limit constant with no `mcp.WithString("limit", ...)` in the schema.
- **[med]** `CollectionFilter.Limit`/`Offset` genuinely work at the SQL layer
  (`collections.go:30-34,122-127`) but `CollectionService.List` never sets them — the one
  real pagination capability in this entity is dead code, same pattern as Epic/Sprint/Project.
- **[med]** `collection_tasks_list`/`collection_inbox_list` have no independent limit and no
  alternate sort (fixed `collection_position ASC` / `added_to_collections_at DESC`).
- **[low]** No `torque_collection_delete` (archive/unarchive only) — may be deliberate
  (audit-preserving), worth confirming intent.
- **[low]** No bulk `task_add` and no collection duplicate/clone.
- **[low]** Documented, deliberate MCP/HTTP semantic divergence: `torque_collection_task_remove`
  is unscoped while the equivalent HTTP route is collection-scoped (`collection_tools.go:213-220`)
  — flagging for awareness, not as a bug.

### Template
- **[high]** Archive is one-way — no `torque_template_unarchive` at any layer — see Convenience
  tools table above.
- **[med]** Filter surface is thin relative to the record: only `Kind` + `IncludeArchived`; no
  `auto_execute`, `executor`, `launch_profile`, or `checkpoint_mode` filter.
- **[med]** `DeleteTemplate`'s referencing-task check is a raw SQL `LIKE` scan on serialized JSON
  metadata (`templates.go:230-233`) with an unescaped, caller-supplied id interpolated into the
  pattern — a template id containing `%` or `_` changes the pattern's meaning silently, risking
  either a false "still referenced" conflict or a missed reference that lets `DeleteTemplate`
  remove a template still in use. Flagging as a correctness/safety issue, not just a DX gap.
- **[med]** No duplicate/fork-to-new-id — `torque_template_update` cleanly handles "evolve
  forward" (always appends a new version under the same id, confirmed not a gap), but cloning an
  existing template into a *new* id requires manually re-specifying every field via `create`.
- **[low]** Naming inconsistency: the instantiate tool is `torque_task_create_from_template`
  rather than `torque_template_*`-prefixed — the only tool in this trio breaking the
  `torque_<entity>_<verb>` convention (likely deliberate, since it primarily creates a Task).

### Artifact
- **[high]** `torque_artifact_list` docstring says "newest first"; actual query is oldest-first
  — see Sorting table. No sort param exists to get true newest-first.
- **[high]** Bypasses the service DTO pattern — see Service-layer alignment section.
- **[high]** Missing-`task_id` failures surface as `error.code=internal` instead of `not_found`
  — see Service-layer alignment section.
- **[med]** No validation on `type` against the documented `file|url|inline|diff|...` set — DB
  column is a bare `NOT NULL TEXT`, so an empty string satisfies it.
- **[med]** No `torque_artifact_update` — correcting a bad `file_path`/`url`/`content` requires
  delete+recreate, losing the original ID and ordering.
- **[med]** No cross-task query surface, and `RunID` on `ArtifactRecord` is neither settable via
  `torque_artifact_create` nor filterable — an artifact can never be linked to a run via MCP.
- **[low]** No bulk create/delete.
- **[low]** `Metadata` field has no MCP param anywhere — unreachable via the tool surface.

### Session
- **[med]** `agent.Manager.ListCheckpoints` has no MCP tool — the single most concrete argument
  for a `SessionService`; see Service-layer alignment section.
- **[low]** No bulk session stop (mass-teardown for orchestrator-spawned sub-agent fleets).
- Everything else the manager exposes (`SendInput`, `Resize`, real PTY `Attach`, `Wait`) is
  legitimately unsuited to MCP's non-streaming transport, and is already documented as such in
  `torque_session_attach`'s description — not a gap.

### Run
- **[high]** `torque_run_list` takes only `task_id`+`verbose` — no `limit`, `status`,
  `project_id`, or `since`, despite `RunService.ListFiltered` already supporting all of them and
  already being used by HTTP — see Pagination section for the concrete fix.
- **[med]** `RunService.Cancel/Kill/Supersede` have no MCP tool (nor HTTP route) despite being
  purpose-built "for operator-facing surfaces" per their own doc comment.
- **[med]** `RunService.Aggregate` (per-task cost/token rollup, used extensively by HTTP) has no
  MCP tool — an agent auditing spend via MCP must pull raw runs and sum client-side.
- This file is otherwise the reference-good example for service-layer alignment — the gap here
  is tool-surface parity with what the service (and HTTP) already support, not architecture.

### Scheduler
- No entity-shaped gaps — this is a singleton runtime-status surface, not a collection. See
  Service-layer alignment section for the "legitimate exception" verdict.

### Settings
- **[med]** `SettingsService.List()` (returns all settings) has no MCP tool — only single-key
  `get`/`save` exist. HTTP has `GET /settings` (list all), `PUT /settings` (bulk save), and a
  curated `/settings/feature-flags` shortcut. An MCP agent can't discover what settings keys
  exist without already knowing the dotted key name.
- **[low]** No bulk-save tool (HTTP has one) — low priority given settings changes are typically
  one-key-at-a-time in agent workflows.

### Model
- No architecture gap — see Service-layer alignment section. This is the cleanest file in the
  runtime-adjacent group precisely because the thing it wraps (a read-only, externally-sourced
  catalog) has no business logic to extract. `model_tools.go` is also the one file in the entire
  audit with a fully correct limit/meta pattern — worth using as the reference example when
  fixing Session's `meta.limit` bug.

### Broker
- **[low]** `Broker.Get(id)`/`Cancel(id)` have no MCP tool — an agent whose
  `torque_broker_request` times out can't inspect or explicitly cancel the pending envelope by
  id (the tool description even acknowledges there's no real cancel path,
  `broker_tools.go:40`). Minor: `Broker.Reply/Notice/Escalation/Handoff/StatusUpdate` are
  deliberately collapsed into one generic `torque_broker_send` with a `kind` discriminator —
  reasonable API compression, not a gap.
- See Service-layer alignment section for the "legitimate exception" verdict — broker validation
  already lives in `internal/broker`, not the adapter, so it already matches the "thin wrapper
  over a real package" shape the user wants, just with the wrapped package outside
  `internal/service`.

---

## Proposed Direction (for discussion)

Not a commitment to build all of this — a menu, roughly ordered by leverage-to-cost ratio, for
the next session to pick from.

### A. Pagination primitive
Wire the `Offset` fields that already exist (Task, Collection, Epic, Sprint, Project) through
service and MCP — cheapest fix in the audit, pure plumbing. Decide on a `total_count` (or at
least `has_more`) field for the shared `{items, meta}` envelope so agents can tell whether a
"page" is really the whole result set. Consider replacing offset with keyset/cursor pagination
if list ordering will become sortable (offset pagination and changing sort keys interact badly),
but that's a bigger call — worth a explicit yes/no rather than defaulting into it.

### B. Sort primitive
Add a small `sort_by`/`sort_dir` param per entity with a short allow-list (e.g. Task: priority,
status, updated_at, created_at; Epic/Sprint/Project: name, status, updated_at, created_at).
Independent of scope: fix the five confirmed docstring/order mismatches (Task, Epic, Sprint,
Project, Artifact) immediately — that's a doc/query-comment fix, not new capability, and today
those docstrings are actively wrong.

### C. Filter/facet closure
Prioritize by value: Task budget/duration fields and `Statuses` multi-status (highest traffic
entity); Epic `priority` (already round-trips through responses, just needs a create/update
param); Comment date-range + multi-entity; Artifact `type`/`run_id`; Project `status`. Skip
low-value ones (e.g. Template's `launch_profile` filter) unless a concrete use case shows up.

### D. Bulk/batch generalization
Generalize `BulkTransition`'s pattern — service-layer method, per-item partial-success result —
to: bulk field-edit update, bulk delete, bulk tag. Keep Sprint's `Start`/`ApproveAll` shape as
the template for scoped "convenience bulk" elsewhere (e.g. an epic- or plan-scoped bulk status
op) rather than building one giant generic bulk-anything tool.

### E. Convenience tools
- Add `torque_template_unarchive` (mirrors Collection's pair) — small, closes an asymmetry.
- Add `torque_project_get` and `torque_project_update` — this one's less "nice to have" and more
  "the entity is missing half its CRUD"; likely the highest-priority item in this whole list.
- Expose `ProjectService`'s artifact CRUD (5 methods, already built, zero MCP wiring).
- Consider duplicate/clone for Template and Task once bulk/filter work lands (lower urgency).

### F. Service-layer alignment fixes
- Introduce a thin `SessionService{mgr *agent.Manager}` in `internal/service`, mirroring how
  `RunService` wraps `sqlstore.Store` — brings Session in line with every comparable entity and
  immediately unlocks `ListCheckpoints`. Fold `torque_plan_start`'s direct `planstart` + 
  `a.sessions` usage into the same decision.
- Leave Scheduler, Broker, and Model as documented exceptions (infra/transport/read-only-catalog)
  — do not build wrapper services for these; a short note in this doc (or a follow-up ADR) saying
  so explicitly would prevent future "why doesn't this go through service.Service" churn.
- Bring Artifact up to the Collection/Template pattern: input DTO, `task_id` existence check
  (`ErrTaskNotFound`-style), `type` validation against the documented enum, and a `not_found`
  mapping for FK failures instead of the current `internal` fallback.

### G. Correctness bugs to fix regardless of audit scope
These aren't "gaps" — they're bugs, and worth pulling out of the backlog and fixing on their own
schedule regardless of what happens with the broader pagination/sort/bulk work:
1. **Epic status vocabulary bug** — `torque_epic_update`'s own documented example fails
   validation. Resolve the active/inactive vs open/closed mismatch one way or the other.
2. **Five docstring/order mismatches** (Task, Epic, Sprint, Project, Artifact) — either fix the
   docs to match the query, or fix the query to match the promise; either is a small change.
3. **Template's unescaped `LIKE` scan** in `DeleteTemplate`'s referencing-task check — a template
   id containing a SQL wildcard character can silently change delete-safety behavior.

---

## Follow-up

The task/project-level data entities (Task, Subtodo, Comment, Project, Epic,
Sprint, Issue, Plan) have a locked target shape and a set of Task-field
decisions in [ADR-0004](../adr/0004-mcp-data-entity-agent-ergonomics.md).
Execution-facing entities (Run, Session, Scheduler, Settings, Model, Broker)
and Collection/Artifact/Template remain open per this audit's findings above.

## Sequencing note

Filter/sort/bulk/convenience work all builds on a stable list/pagination envelope — recommend
landing (A) and the docstring half of (B) first, since everything else either extends the same
envelope or depends on agents being able to trust what a list response says. (F)'s Session
service and (G)'s bug fixes are independent and can land in parallel with no ordering
constraint. This doc doesn't commit to an order — flagging the dependency so the next session
can decide deliberately rather than by accident.
