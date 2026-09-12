# MCP Tools Reference — Data Entities

Agent-facing reference for the `torque_*` MCP tools covering Torque's core
data-management entities: **Task, Subtodo, Comment, Project, Epic, Sprint,
Issue, Plan**. This is the scope ADR-0004 locked and Phase 0–5 of
`tasks/INDEX.md` implemented (all phases `done` as of 2026-08-20) — the
"MCP discoverability/schema reference layer" that ADR-0004's Consequences
section named as a deferred follow-up.

**Out of scope for this doc:** Run, Session, Scheduler, Settings, Model,
Broker, Collection, Artifact, Template, Checkpoint. Those tools exist and
work, but weren't part of this ergonomics pass — see
`docs/surfaces.md` for the full tool-area list and
`docs/architecture/mcp-service-layer-audit.md` for their pre-existing shape.

**Source of truth.** Every tool's exact parameter list and full description
lives in `internal/mcpadapter/*.go` as the literal `mcp.WithDescription(...)`
/ `mcp.WithString(...)` calls the MCP server returns from `tools/list` — an
agent can always read those verbatim over the wire. This doc is a
navigational index and a conventions guide, not a schema dump; it will drift
less by pointing at the code than by duplicating it. File:line pointers are
given per entity below.

## Conventions shared by every tool in this doc

### Response envelope

Every tool call returns `{ok, data, error}`:

- `ok=true`: `data` holds the payload (a singleton record, a `{items, meta}`
  list envelope, or a minimal scalar-op map like `{id, deleted: true}`).
- `ok=false`: `data` is omitted, `error` is `{code, message, field?}`.

`error.code` is one of: `arg_invalid`, `not_found`, `conflict`, `domain`,
`permission`, `internal` — see `internal/mcpadapter/errors.go` (`ErrorCode`)
for the exact mapping rules. Agents can branch on `code` instead of parsing
free-text messages; `field` is set when a single input field is at fault
(usually paired with `arg_invalid`).

### List envelope and pagination

List/search tools return `data = {items: [...], meta: {...}}`. Two envelope
shapes exist:

- **Cursor-paginated** (Task, Comment, Project, Epic, Sprint, Issue, Plan
  list/search tools): `meta = {truncated, returned, limit, has_more,
  next_cursor}`. Pass a previous call's `meta.next_cursor` back as `cursor`
  to fetch the next page; `next_cursor` is `null` once exhausted. A cursor
  is only valid for the exact `sort_by`/`sort_dir` it was issued under —
  changing either without dropping `cursor` returns `error.code=arg_invalid`.
  The cursor is an opaque, versioned, base64url-encoded token
  (`internal/service/pagination/cursor.go`); do not attempt to construct or
  parse one client-side.
- **Byte-capped only, no cursor** (Subtodo list, Plan `list_children`):
  `meta = {truncated, returned, limit, hint?}` — no `has_more`/`next_cursor`.
  These lists are bounded by their parent (a task's checklist, a plan's
  children) rather than needing real pagination.

Either way, `truncated=true` means the ~100KB per-response byte cap
(`internal/mcpadapter/response.go`, `maxMCPResponseBytes`) trimmed the page
itself — orthogonal to `has_more`, which reflects whether the underlying
query has more rows beyond this page.

### Sort

List tools that support cursor pagination also take `sort_by` (an
entity-specific allow-list) and `sort_dir` (`asc`/`desc`). An unrecognized
value returns `error.code=arg_invalid`. Per-entity allow-lists and defaults
are in the tables below.

### Brief vs. verbose

Most list tools default to a small "brief" projection (~150 bytes/record —
id, name/title, status, a couple of facets, `updated_at`; large free-text
columns and JSON blobs are dropped) so large fan-outs fit under the byte
cap. Pass `verbose="true"` for full records.

### Bulk operations

Every `*_bulk_*` tool returns `data = {succeeded: [...], failed: [{id,
error: {code, message, field}}...]}`. **Partial success is not a call-level
error** — `ok=true` even when some ids fail; agents must inspect `failed[]`
themselves. Only malformed call-level args (bad `ids` JSON, empty `ids`)
return a call-level `errResult`. `torque_comment_bulk_add` and
`torque_task_subtodo_bulk_add` are creation-shaped bulk ops, so their
`failed[]` is keyed by target/positional-index rather than a pre-existing
id — see their entity sections.

### Parameter encoding

- Numeric and boolean params are declared as MCP **strings** (e.g.
  `"limit": "50"`, `"manual": "true"`) so LLM clients that emit
  string-encoded values don't trip schema validation; the adapter coerces
  string/number/bool interchangeably (`reqInt`/`reqFloat`/`reqBool` in
  `internal/mcpadapter/adapter.go`). `-1` is the unlimited sentinel on budget
  fields (`cost_budget`, `token_budget`, `max_duration_ms`).
- List-of-string params (`tags`, `depends_on`, `ids`, `statuses`, etc.) are
  declared as a JSON-encoded string (e.g. `"tags":"[\"p0\",\"backend\"]"`)
  but the adapter also accepts a native JSON array where the transport
  allows it.
- **Update tools are true partial patches.** Presence in the payload is the
  only "change this" signal — an omitted key leaves the field untouched; an
  explicit empty string clears most nullable scalars. This is uniform across
  every `*_update`/`*_bulk_update` tool in this doc (locked by FIX-001 for
  Task, then applied to every other entity in SWEEP-001).

### Feature flags

Project, Epic, and Sprint tools only register when their feature flag
(`features.projects` / `features.epics` / `features.sprints`) is enabled —
`registerOptInTools` in `internal/mcpadapter/adapter.go`. `torque_health`
reports currently-enabled features in `enabled_features[]`. Restart the MCP
process after toggling a flag so `tools/list` refreshes. Task/Subtodo/
Comment/Issue/Plan tools are always registered.

---

## Task

The core work-item entity — everything else in this doc either contains
Tasks (Project/Epic/Sprint) or *is* a Task row under the hood (Issue, Plan).
Full field reference: ADR-0004 §4. Source: `internal/mcpadapter/task_tools.go`,
`task_bulk_tools.go`.

| Tool | Purpose |
|---|---|
| `torque_task_create` | Create a task. Safety override forces `manual=true` on every create (an agent must promote it via `torque_task_update {"manual":false}` before the scheduler dispatches it) — the response's `dispatch_notice` spells out the exact promotion call. Optional `subtodos[]` seeds an initial checklist atomically. |
| `torque_task_get` | Fetch one task by id, **including its 10 most recent comments by default** (CW-20260910-0057). `comments="false"` opts out; `comments_limit` widens the window (max 100). |
| `torque_task_list` | Filter + free-text `search` + sort + cursor-paginate, all in one tool (no separate search tool). Shares the public task-query contract with HTTP: status/statuses[]/priority/kind/trust/checkpoint_mode/parent_id/project_id/sprint_id/epic_id/tags[]/manual/agent_profile/launch_profile, `created_*`/`updated_*` RFC3339 ranges, and `*_gte`/`*_lte` budget/duration range filters. `include_internal` (default false) hides `kind=internal` automation rows unless `kind=internal` is requested explicitly. |
| `torque_task_update` | Partial patch. Numeric sentinel `-1` = unlimited on budget fields. `status` is accepted and routed through the same path as `torque_task_transition` — before CW-20260909-0011 the arg was silently dropped and the call still answered `ok:true`. |
| `torque_task_delete` | Hard delete (runs/artifacts/comments cascade). Prefer `transition` to `abandoned` for an audit-preserving close — reachable from any status in one call. |
| `torque_task_transition` | Set a status. Permissive: any status reaches any other in one call (`todo→done` included). Vocabulary: `backlog`, `todo`, `queued`, `doing`, `review`, `done`, `blocked`, `paused`, `archived`, `abandoned`, `cancelled`. Only two refusals — a status outside that list, and leaving `done`/`archived`, which needs `force=true`. Optional `comment`/`comment_author` posts a comment atomically with the transition (one transaction). |
| `torque_task_bulk_transition` | Same status applied to many ids; PRIM-003 `{succeeded, failed}` envelope. |
| `torque_task_bulk_update` | Same field set/semantics as `torque_task_update`, applied across `ids[]`. |
| `torque_task_bulk_delete` | Hard-delete many ids in one call. |
| `torque_task_bulk_tag` | Add/remove tag slugs across many ids — additive, unlike `update`'s `tags` (which replaces the full set). |

`torque_task_list` sort: `sort_by` ∈ `priority\|status\|updated_at\|created_at`,
default `priority asc` (tiebreak `id asc`). HTTP `GET /api/v1/tasks` uses the
same public default order; lower-level service/store list calls used by engine
internals retain their legacy ordering.

`torque_task_list` validates explicit query arguments instead of broadening
bad filters into successful unfiltered lists. Malformed numbers, booleans,
RFC3339 dates, sort directions, cursors, malformed `tags` JSON, and wrong
native types return `error.code=arg_invalid` with `error.field` when one input
is at fault. Omitted values remain unfiltered/defaulted, `manual` keeps
`manual`/`true`/`1`, `auto`/`false`/`0`, and `both`/empty sentinels, and
`parent_id` keeps the empty/`null` root sentinel. Cursor tokens are opaque,
sort-specific, and filter-specific by caller contract: when reusing
`meta.next_cursor`, retain the same filters plus the same `sort_by`/`sort_dir`.

Priority list filters are exact arbitrary integers, not a 1-5 vocabulary:
`0`, negative values, and large int64 values are legal exact values. Use
`priority` for one value or `priorities` as a non-empty JSON array for an
OR-match; passing both is rejected as ambiguous, and `priorities=[]` is
rejected rather than treated as omission.

`include_total` is optional on MCP and defaults to false to preserve cheap
legacy list calls. When `include_total=true`, `meta.total` is the full matching
cohort count excluding cursor/offset/limit, and it is part of the normal
100KB-capped response sizing. HTTP always includes `total` in its existing
task-list envelope. HTTP keeps its GUI compatibility alias `priority=1,2` as a
comma-separated exact-integer OR-list, including `0`; repeated HTTP query keys,
malformed raw query strings, malformed/overflow/blank CSV members, unknown
keys, and invalid shared-query fields return `400` with `error` and `field`.

### Unknown arguments are rejected, not dropped (CW-20260907-0060)

Neither the MCP protocol layer nor mcp-go validates an incoming argument
against the tool's schema, and every Torque handler reads its inputs
presence-based. An argument no handler read was therefore simply not read: no
rejection, and `ok: true` either way. A caller passing `body` where the schema
says `content` was told the call succeeded while the value went nowhere.

Every tool registered through `addTool` — the whole surface, loopback subset
included — now rejects an argument its schema does not declare, with
`error.code=arg_invalid` naming the offenders and enumerating the accepted set.
`_`-prefixed transport arguments (`_traceparent`, `_tracestate`) are exempt.

Scope evidence: an audit across the global, loopback and all-features adapters
found zero handlers reading an argument their tool does not declare, so no
legitimate call is newly rejected. The only field on `torque_task_create` and
not on `torque_task_update` is `subtodos` (there is a dedicated
`torque_task_subtodo_*` family) — every other create field is writable on
update, so the reported per-field asymmetry was narrower than suspected.

### `kind=decision` carries its own `checkpoint_mode` default (CW-20260907-0060)

`checkpoint_mode`'s documented default is `none`, but `kind=decision` requires
`blocking`. That combination made the documented default invalid for a
documented kind, discoverable only by being rejected. The kind now carries the
stricter default: `torque_task_create {"kind":"decision"}` and
`torque_task_update {"kind":"decision"}` both apply `checkpoint_mode=blocking`
when the caller supplies none, and both persist it.

An **explicit** `checkpoint_mode=none|non_blocking` alongside `kind=decision`
is still rejected — and since an omitted value is now defaulted, that rejection
is reachable only from an explicit override, so the error says so rather than
restating the requirement. Both fields' descriptions state the coupling.

### `torque_task_get` comments (CW-20260910-0057)

`torque_task_get` — the cross-task tool **and** the loopback's worker-pinned
one — returns a tail window of the task's comments alongside the record.
Corrections and scope changes posted after a task is written live in the
thread; reading the description alone is how a session executes a framing that
has since been superseded.

| Arg | Default | Meaning |
|---|---|---|
| `comments` | `"true"` | include the comment tail |
| `comments_limit` | `10` | window size, clamped to 100 |

The window is the **newest** `comments_limit` comments, presented **oldest →
newest** so successive corrections read forward — matching
`torque_comment_list`'s per-entity `created_at ASC` default inside the window.

`CommentsMeta` is **always present** when comments were requested, including
when nothing was cut (`omitted: 0, truncated: false`). Inferring completeness
from array length is the exact failure this fixes, so the response states it:

```json
"Comments": [ {"id": 4096, "author": "planner", "content": "...",
               "created_at": "...", "updated_at": "..."} ],
"CommentsMeta": {"returned": 10, "total": 34, "omitted": 24, "truncated": true,
                 "hint": "24 older comments omitted — torque_comment_list ..."}
```

`total` comes from a `COUNT` query, not from loading the thread. With
`comments="false"` both keys are absent entirely, not empty.

This path also enforces `maxMCPResponseBytes` (100KB), which
`torque_task_get` did not do at all before — the description column is
unbounded. On overflow it drops the **oldest** comment in the window first,
the opposite direction from `cappedJSONResult`'s tail-trim, because here the
newest comments are the ones being kept. If the record alone exceeds the cap,
every comment is dropped and `CommentsMeta` says so; the record is never
silently trimmed.

`comments` is a separate argument rather than a reuse of `task_list`'s
`verbose`: on `task_list` `verbose` selects brief-vs-full *task* records, while
here the axis is whether a *different entity* is included.

### Task typed read format (CW-20260911-0085)

`torque_task_get` and `torque_task_list` accept an additive
`format="typed"` response option. Omit it, or pass `format="legacy"`, to keep
the existing MCP defaults: `torque_task_get` and verbose list return the
PascalCase `TaskRecord` shape with `sql.Null*` wrappers, and default list keeps
the compact lowercase `briefTask`.

Typed format is read-only and never changes the query cohort, ordering,
pagination, cursor, comment-tail, or byte-cap rules. In typed mode task records
use snake_case keys and native JSON values: nullable scalar columns become JSON
`null`, explicit zero numeric values remain numbers, tags/dependencies are JSON
arrays, and stored JSON blob columns decode to arrays/objects rather than
JSON-encoded strings. `torque_task_get format="typed"` keeps the existing
comment-tail behavior but spells the added keys `comments` and
`comments_meta`; `comments="false"` still omits both. `torque_task_list
format="typed"` without `verbose` returns a richer brief projection with
nullable scope references (`parent_id`, `project_id`, `sprint_id`, `epic_id`,
collection refs) plus tag slugs and dependency ids. `verbose="true"` returns
full typed task records.

For compatibility with HTTP, valid stored empty or `null` JSON blob values read
as the same empty fallback HTTP exposes today (`[]` for array-like fields, `{}`
for object-like fields). Typed MCP additionally reports malformed or
wrong-shaped stored blobs in `decode_errors` so consumers can distinguish
legacy/corrupt storage from an intentional empty value. Intentional differences
from HTTP default task responses are limited to the opt-in `decode_errors`
field, the lowercase MCP comment-tail keys, and JSON-number precision
preservation inside decoded blob fields.

---

## Subtodo

A task-scoped checklist; required items block a task from passing `review`.
Deliberately **not** polymorphic (Epic/Sprint/Project checklists were
considered and rejected — ADR-0004 §1.4). Issue/Plan get subtodos for free
since they're Task rows. Source: `internal/mcpadapter/subtodo_tools.go`.

| Tool | Purpose |
|---|---|
| `torque_task_subtodo_list` | List a task's checklist. |
| `torque_task_subtodo_add` | Append one item; `id` is optional (server auto-generates when omitted — supply a slug like `"check-auth-flow"` to keep it deterministic). |
| `torque_task_subtodo_bulk_add` | Seed a whole checklist in one call. Response keys `succeeded`/`failed` on the *resolved* id (caller-supplied or generated), not an input echo — a failed item without an assigned id is keyed by positional placeholder (`"item[1]"`). |
| `torque_task_subtodo_done` | Mark an item done with an `evidence` string (artifact id, commit SHA, URL, note). |
| `torque_task_subtodo_update` | Edit `text`/`required` on an existing item. |
| `torque_task_subtodo_delete` | Remove an item. |

No `sort_by`/cursor — checklists are small and bounded by their parent task.

---

## Comment

Freeform prose attached to an entity — never drives lifecycle. Polymorphic
across `entity_type` ∈ `task\|project\|epic\|sprint` (Issue/Plan are already
covered via `entity_type="task"` since they're Task rows). Author-scoped
edit/delete: only the original `author` string may update or delete a
comment (mismatch → `error.code=permission`). Source:
`internal/mcpadapter/comment_tools.go`.

| Tool | Purpose |
|---|---|
| `torque_comment_add` | Post a comment on one entity. |
| `torque_comment_list` | Chronological thread for one `entity_id`, or for a caller-resolved set via `entity_ids[]` (same `entity_type`) — e.g. every task in a sprint. Default oldest-first. |
| `torque_comment_search` | Free-text `query` over comment content, optionally scoped by entity/entity_ids/author/date range. Default newest-first. Always returns full records (no brief/verbose toggle). |
| `torque_comment_update` | Author-scoped content edit. |
| `torque_comment_delete` | Author-scoped hard delete, no undo. |
| `torque_comment_bulk_add` | Post the same `content` to many `{entity_type, entity_id}` targets (a broadcast, e.g. "sprint review in 1 hour" to every task in a sprint). Creates new rows, so `failed[]` is keyed by `target`, not an existing id. |

`sort_by` is a singleton allow-list (`created_at` — the only sortable
column); `torque_comment_list` defaults `asc`, `torque_comment_search`
defaults `desc`.

---

## Project

Long-lived, repo-scoped grouping (feature-flagged: `features.projects`).
Source: `internal/mcpadapter/project_tools.go`.

| Tool | Purpose |
|---|---|
| `torque_project_create` | Create. `repo_path` must resolve to an existing directory (`~` expands) — a missing path is `error.code=arg_invalid, field=repo_path`. |
| `torque_project_get` | Fetch by id. |
| `torque_project_update` | True partial patch — `repo_path` can be changed but never cleared to empty. |
| `torque_project_list` | Filter by `status`; free-text search is **not** available on Project (no `search` param — this is the one in-scope entity without a merged search). |
| `torque_project_delete` | Hard delete; linked tasks keep their row with `project_id` cleared. |
| `torque_project_archive` / `torque_project_unarchive` | Soft-delete, orthogonal to `status` (an archived project keeps whatever `active`/`inactive` value it had). Idempotent. |

`torque_project_list` sort: `sort_by` ∈ `name\|status\|updated_at\|created_at`,
default `name asc`.

---

## Epic

Multi-sprint initiative grouping (feature-flagged: `features.epics`).
Source: `internal/mcpadapter/epic_tools.go`.

| Tool | Purpose |
|---|---|
| `torque_epic_create` | Create. `priority` is a plain settable integer (no enforced range). |
| `torque_epic_get` | Fetch by id. |
| `torque_epic_update` | Partial patch; `status` ∈ `active\|inactive` — no dedicated approval flow, close via `status=inactive`. |
| `torque_epic_delete` | Hard delete; linked tasks keep their row with `epic_id` cleared. Prefer `archive` or `update status=inactive`. |
| `torque_epic_archive` / `torque_epic_unarchive` | Soft-delete, orthogonal to `status`. Excluded from `torque_epic_list` by default. |
| `torque_epic_list` | Filter (`status`, `project_id`) + free-text `search` (over id+name+description) + sort, merged into one tool. |
| `torque_epic_bulk_update` | Same field set as `torque_epic_update`, applied across `ids[]`. |

`torque_epic_list` sort: `sort_by` ∈ `name\|status\|updated_at\|created_at`,
default `updated_at desc`.

---

## Sprint

Short-cycle execution cohort with an optional cost budget (feature-flagged:
`features.sprints`). Source: `internal/mcpadapter/sprint_tools.go`.

| Tool | Purpose |
|---|---|
| `torque_sprint_create` | Create. `approval_mode` ∈ `auto\|approve_sprint\|approve_each` (default `approve_each`). |
| `torque_sprint_get` | Fetch by id plus derived budget headroom: `{sprint, within_budget, cost_remaining}`. |
| `torque_sprint_update` | Partial patch; `status` transitions (`active\|inactive\|completed`) are handled inline in the same call. |
| `torque_sprint_bulk_update` | Same field set + optional status transition, applied across `ids[]`. |
| `torque_sprint_delete` | Hard delete; tasks keep their row with `sprint_id` cleared. Prefer `archive`. |
| `torque_sprint_archive` / `torque_sprint_unarchive` | Soft-delete, orthogonal to `status`. |
| `torque_sprint_list` | Filter (`status`, `project_id`, `cost_budget_min`/`max`, `over_budget`) + sort. No free-text search on Sprint. |
| `torque_sprint_approve` | **Closes** the review gate: approve one task (`task_id`) or every task currently in `review` (omit `task_id`). Does *not* start dispatch. |
| `torque_sprint_start` | **Opens** the dispatch gate: bulk-promotes every `manual=true` task in the sprint to `manual=false`. This is how you "start" a sprint under `approval_mode=approve_sprint` — a fresh sprint has nothing in `review` yet, so calling `approve` first reports 0 approved. Idempotent. |

Sprint has two distinct bulk-scoped convenience verbs (`approve`, `start`) —
don't confuse them: `approve` closes, `start` opens. `torque_sprint_list`
sort: `sort_by` ∈ `name\|status\|updated_at\|created_at`, default
`updated_at desc`.

---

## Issue

A project-scoped, low-friction backlog item — a `kind=issue` Task row
(always registered, but `project_id` is required so `features.projects`
must be enabled in practice). `body`/`context`/`issue`/`details` are all
aliases for the same underlying `description` column; pass whichever reads
naturally, first non-empty one wins. Source:
`internal/mcpadapter/issue_tools.go`.

| Tool | Purpose |
|---|---|
| `torque_issue_create` | Create. Requires `title`, `project_id`, and a body (via any alias). |
| `torque_issue_get` | Fetch by id; rejects a non-issue task id. |
| `torque_issue_list` | Filter (`project_id`, `status`) + free-text `query` (over id/title/body), merged list+search in one tool, DB-level limit (no full-fetch-then-truncate). |
| `torque_issue_update` | Partial patch; rejects non-issue ids. |
| `torque_issue_delete` | Hard delete (runs/artifacts/comments cascade); rejects non-issue ids. Supersedes the old `torque_task_delete` workaround. |
| `torque_issue_bulk_update` | Same field set as `torque_issue_update`, applied across `ids[]`. |
| `torque_issue_bulk_transition` | Delegates straight to `TaskService.BulkTransition` — issues share Task's status policy. |

A freshly created issue starts at `status=backlog`. That used to be a dead end:
`backlog` was not a key in `TaskService`'s transition table, so Torque created
rows its own FSM could not move and callers needed `force=true` to escape.
Since CW-20260909-0011 `backlog` is a canonical status like any other and moves
out of it need no force.

`torque_issue_list` sort: same allow-list/default as Task
(`priority\|status\|updated_at\|created_at`, default `priority asc`) — issue
rows are Task rows.

---

## Plan

A `kind=plan` Task with an ordered `phases[]` (stored in
`metadata.plan.phases`) and execution children linked via `parent_id` +
`metadata.phase_id`. See `docs/plans-v1.md` for the phase/child model.
Source: `internal/mcpadapter/plan_tools.go`.

| Tool | Purpose |
|---|---|
| `torque_plan_create` | Create with optional initial `phases[]` (`{name, acceptance?}` each). |
| `torque_plan_get` | Fetch with decoded `phases[]` and a child-task roll-up. |
| `torque_plan_list` | Dedicated, hard-scoped `kind=plan` list (the alternative is `torque_task_list {"kind":"plan"}`) — filter + `search` + sort + cursor. |
| `torque_plan_update` | Partial patch of the plan's own fields (title/description/priority/project/sprint/epic/tags). Rejects non-plan ids. |
| `torque_plan_delete` | Hard delete the plan row; children are **not** deleted — their `parent_id` clears (`ON DELETE SET NULL`). |
| `torque_plan_add_phase` | Append a phase; returns the assigned `phase_id` (e.g. `ph-3`). |
| `torque_plan_remove_phase` | Remove a phase; rejected with `error.code=conflict` if any child still references it via `metadata.phase_id`. |
| `torque_plan_list_children` | List tasks under the plan (optionally narrowed to one `phase_id`). Byte-capped only, no cursor. |
| `torque_plan_start` | Boot an Orchestrator session for the plan and transition it to `doing`. Execution-adjacent (out of ADR-0004's data-ergonomics scope) — kept as-is. Idempotent: an already-running session returns `error.code=conflict` with the live `session_id` embedded in the message text. |

`torque_plan_list` sort: same allow-list/default as Task
(`priority\|status\|updated_at\|created_at`, default `priority asc`).

---

## Where the real-FK / `depends_on` work lives

Not a tool-surface change, but relevant to any agent reading `sprint_id`/
`project_id`/`epic_id`/`depends_on` off a `TaskRecord`: as of migration 028
(FK-002) and 029 (FK-003), `tasks.sprint_id`/`project_id`/`epic_id` are real
`REFERENCES ... ON DELETE SET NULL` columns, and `depends_on` is backed by a
`task_dependencies` join table (not a JSON blob) — `torque_task_get`/`_list`
(verbose)/`_create`/`_update` surface it as a plain `DependsOn: []string` of
task ids either way, so no caller-visible shape changed.

## Comment emptiness and unattributed rows (CW-20260903-0044)

`torque_comment_add` declared `content` required and did not check it. A call
that omitted it — or passed `body`, a plausible slip for a prose field — was
accepted, answered `ok: true` with a real comment ID, and persisted a row
storing nothing. `torque_comment_bulk_add` and `torque_comment_update` had
always checked; the single-add path was the only gap.

All three now route through one helper, so the error (`arg_invalid`,
`"content is required"`, `field: content`) cannot drift between them.
Whitespace-only content counts as empty. `CommentService.Add` carries the same
check as a backstop for the HTTP route, and `torque_task_transition` treats a
whitespace-only `comment` as no comment rather than writing a blank row.

**Unattributed comments are now deletable.** `torque_comment_delete` is
author-scoped by exact match, and it rejected `author=""` as missing — so no
argument could ever match a row whose author is `""`. Since `author` is
optional on `torque_comment_add` and an omitted one stores `""`, *every*
unattributed comment was permanently unremovable, not only the ones the
empty-content bug created. Passing `author=""` **explicitly** now matches an
unattributed row; omitting the field entirely is still an error, so a caller
who forgets it is not silently matched against unattributed rows.

This does not change *who* may delete a comment. `author` is free text rather
than a session identity, so declaring a comment unattributed grants no
authority that was not already trivially available by claiming any other slug.
Whether author-scoping is a meaningful check at all is a separate, open
question.

## Related docs

- `docs/adr/0004-mcp-data-entity-agent-ergonomics.md` — the target-shape
  decision this doc reflects the implementation of.
- `docs/architecture/mcp-service-layer-audit.md` — the pre-implementation
  gap audit (historical; describes the *old* surface, not current behavior).
- `tasks/INDEX.md` — the phased task breakdown that executed the ADR.
- `docs/surfaces.md` — the full HTTP + MCP tool-area list, including
  entities out of this doc's scope.
