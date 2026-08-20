# ADR-0004 — MCP Data-Entity Ergonomics: Target Shape for Task/Project-Level Data

**Status:** Proposed
**Date:** 2026-08-20
**Supersedes:** —
**Source:** Direct design session (audit: `docs/architecture/mcp-service-layer-audit.md`)

---

## Context

An MCP-tool-surface audit (`docs/architecture/mcp-service-layer-audit.md`, same
session) found recurring agent-DX gaps across all ~104 registered `torque_*`
tools: hard result-count ceilings instead of real pagination, zero sort
support system-wide, near-zero bulk/batch operations, inconsistent
convenience tools (archive/move/duplicate), and a handful of places where
`internal/mcpadapter` carries logic that belongs in `internal/service`.

This ADR scopes down to the **task/project-level data-management entities** —
the ones a user works with whether or not they ever run Torque's execution
engine — and defines the target shape for their MCP surface. Explicitly
**out of scope for this ADR**: Run, Session, Scheduler, Settings, Model,
Broker, Collection, Artifact, Template, and any execution/dispatch behavior.
Those get their own pass later.

**Entities in scope:** Task, Subtodo, Comment, Project, Epic, Sprint, Issue,
Plan.

**Goal:** first-class agent ergonomics for reading, searching, organizing,
and writing this data — an agent using Torque purely as a task/project
tracker (no execution) should find the surface just as natural as an agent
that dispatches work through it.

This ADR does not sequence, scope, or resource the implementation — that is
deliberately deferred to a follow-up planning session. It defines the target
end-state a task-creation pass can decompose into concrete work.

---

## Decision

### 1. Task field-level fixes (locked)

These were audited field-by-field against the DB schema (`tasks` table,
`internal/persistence/sqlstore/migrations/`), `TaskService.Create`/`Update`
(`internal/service/task.go`), and the MCP tool schemas
(`internal/mcpadapter/task_tools.go`). Four concrete deltas:

1. **Drop `mcp.Required()` from `description` on `torque_task_create`.**
   Neither the DB (`description TEXT NOT NULL DEFAULT ''`) nor
   `TaskService.Create` (only checks `Title`) requires it — the MCP schema
   was the only layer over-requiring.
2. **Fix `handleTaskUpdate`'s field-detection bug for `title`/`description`/
   `priority`.** These three use value-based detection
   (`if v := reqStr(req, "title"); v != ""`), unlike every other field on
   the same tool, which correctly uses presence-based detection
   (`if _, ok := args["executor"]; ok`). Effect: `description` (and `title`)
   can never be cleared via update — `"description": ""` silently no-ops,
   contradicting the tool's own docstring ("empty string clears most
   nullable scalars"). Fix: switch to presence-based detection for all
   three, matching the rest of the tool. Implementation note: once `title`
   is presence-based, an explicit `"title": ""` will reach
   `TaskService.Update` — add an explicit non-empty check there (a clean
   `ValidationError`, not a raw DB `NOT NULL` failure).
3. **Auto-generate Subtodo IDs.** `torque_task_subtodo_add`'s `id` is
   currently required and caller-invented, enforced in
   `TaskService.AddSubtodo` (`service/task.go:637-639`) — the only entity
   in the system without a server-generated ID. Target: `id` becomes
   optional; when omitted, the server generates a unique id (scheme TBD at
   implementation — short random slug or sequential, doesn't need deciding
   here); when the caller does supply one, existing uniqueness validation
   still applies, so meaningful slugs (`"check-auth-flow"`) stay possible.
4. **Subtodo stays Task-scoped only.** Considered and explicitly rejected
   making it polymorphic (Epic/Sprint/Project checklists) for this round —
   noted so it isn't re-litigated; Issue/Plan get subtodos for free since
   they're rows in `tasks`.

**Known limitation, not actioned this round:** `priority` (int, docs say
1–5) has no enforced range at any layer — not the DB, not
`task_validation.go`, not the MCP schema. Any integer is currently
accepted. Flagged for awareness; pick up only if/when it blocks something
concrete.

### 2. Design principles for the target MCP surface

1. **One way to find things.** Every entity gets exactly one list-shaped
   tool doing filter + free-text search + sort together — no parallel
   `_list`/`_search` pair that can silently diverge (Task's pair already
   has: `_list`'s docstring claims an order the query doesn't produce,
   `_search`'s docstring is correct).
2. **Nothing required that isn't truly required.** The DB-required vs
   service-required vs MCP-required audit done for Task (§1 above) applies
   to all seven other entities during implementation.
3. **A list response always says if there's more**, and gives something
   reliable to page with.
4. **Bulk is a first-class shape**, not bolted on per entity.
5. **Relationships that are real are enforced as real** — not "checked in
   Go, unenforced in SQLite."
6. **Update is always a true partial patch** — presence-in-payload is the
   only "change this" signal, everywhere.

### 3. Cross-cutting primitives

**Pagination.** Target: keyset/cursor pagination. Rationale: once `sort_by`
is selectable, offset pagination silently duplicates/skips rows across
calls when data changes between them, and ties in the sort column make page
boundaries nondeterministic without a stable tiebreaker. Every list tool
takes an opaque `cursor` (returned from the previous call) and internally
breaks sort ties on `id`. `meta` gains `next_cursor` (null when exhausted)
and `total_count` (or at minimum `has_more`). This is more expensive than
offset — the sequencing session should weigh it against a cheaper
offset+`total_count` compromise; this ADR states cursor as the target, not
a hard requirement.

**Sorting.** Every list tool gets `sort_by` (allow-listed per entity) and
`sort_dir` (`asc`/`desc`). Every entity's actual default order gets
documented accurately regardless of what else lands (the audit found five
tools whose docstrings misdescribe their own default order).

**Filtering.** Every real facet/relationship column becomes filterable:
status/kind/trust-style enums, all container relationships (sprint/
project/epic/parent), `created_at`/`updated_at` ranges, and — where they
exist — budget/limit state. Multi-value filters that already exist at the
store layer (e.g. Task's `Statuses` OR-filter, live on HTTP) get MCP
parity.

**Search folds into list where it's the same query.** Task and Issue's
`_search` tools run the identical query `_list`'s `search` param already
runs — merge into one tool per entity. Comment is the exception:
`comment_list` (chronological thread for one entity) and `comment_search`
(cross-entity, recency-ordered) are genuinely different query shapes — both
stay, both get real pagination/sort/filter instead of today's hardcoded,
unbounded-then-truncated behavior.

**Bulk operations**, generalized from `TaskService.BulkTransition`'s
already-good shape (service-layer method, per-item partial-success
result):
- `bulk_update` — same field set as single `update`, applied across `ids[]`
- `bulk_delete` — where hard delete is kept at all (see Archive below)
- `bulk_transition` — kept as its own verb only where a real FSM exists to
  protect (Task, and Issue since it shares Task's FSM); other entities'
  status changes fold into `bulk_update`
- `bulk_tag` — add/remove tag slugs across many IDs in one call
- All bulk ops return `{succeeded: [...], failed: [{id, error}]}` —
  partial success is not an error state
- Sprint's `Start`/`ApproveAll` stays as the reference pattern for
  *scoped* convenience-bulk (bulk-promote everything inside a container);
  worth reusing for an epic- or plan-scoped equivalent

**Archive**, as a concept independent of `status`. Target: a real nullable
`archived_at` on Project, Epic, Sprint, Issue, Plan, with `archive`/
`unarchive` tools, kept orthogonal to `status` (archiving an epic isn't the
same fact as the epic being "done"). Task keeps hard-delete + `abandoned`
status as its audit-preserving close path — archive is optional parity
there, not a gap.

**Response envelope.** `{ok, data}` stays. List `data` becomes
`{items, meta: {returned, limit, total_count|has_more, next_cursor, hint?}}`.
Error taxonomy (`arg_invalid`/`not_found`/`conflict`/`domain`/`permission`/
`internal`) stays as-is — every entity's write paths should map failures
into it correctly (the audit found at least one FK-miss silently falling
through to `internal` instead of `not_found`; that pattern shouldn't exist
anywhere in the target state).

**Real foreign keys.** `tasks.sprint_id`, `tasks.project_id`,
`tasks.epic_id` are plain `TEXT` columns today with no `REFERENCES` clause
— existence is checked in Go at write time only, so a deleted sprint/
project/epic leaves a dangling reference SQLite won't stop. Target: real
`REFERENCES ... ON DELETE SET NULL` columns, matching the existing
`parent_id`/`collection_id` pattern. Implementation note: SQLite requires a
full table rebuild to add this (the codebase already has this pattern —
see any `tasks_new` migration, e.g. `025_kind_issue.sql`); existing rows
with stale/orphaned IDs need a backfill/cleanup pass before the constraint
can be added cleanly. Not solved here — flagged for the implementer.

### 4. Task field reference (for implementation context)

Full current-state field inventory, with relationship type called out,
captured during this session:

**Core identity/content:** `id` (auto-gen PK), `title` (only truly required
field), `description`, `status` (free TEXT, no DB CHECK/FK — FSM lives
entirely in the service-layer transition method, not the schema — relevant
to the future opt-in-lifecycle design), `priority` (unenforced range, see
above), `metadata` (JSON), `created_at`/`updated_at`.

**Real FKs today:** `parent_id → tasks(id) ON DELETE SET NULL`,
`collection_id → collections(id)`, `last_run_id → runs(id)` (execution-only,
not on `TaskRecord`, out of scope here).

**Soft/unenforced relationships (target: promote per §3):** `sprint_id`,
`project_id`, `epic_id` — plain TEXT, Go-checked only. `depends_on` — JSON
array of task IDs inside a single TEXT column; existence-checked at write
time but nothing stops a referenced task from being deleted later and
leaving a dangling ID inside the array; this one can't become a real FK
without normalizing into a join table, which is a bigger structural change
— flagged as an open question (§6), not decided.

**Real relational, not an inline column:** `tags` — proper many-to-many
join table via `TagService` (migration 005 dropped the old inline `tags`
column) — already done right structurally.

**Execution/lifecycle-oriented fields** (relevant to the future opt-in
design, not touched by this ADR): `manual`, `executor`, `launch_profile`,
`agent_profile`, `working_dir`, `system_prompt`, `agent_file`, `tools`,
`permissions`, `environment`, `cost_budget`, `max_retries`,
`max_duration_ms`, `token_budget`, `on_done`, `on_fail`, `on_review`,
`on_done_merge`, `escalation_chain`, `quality_gates`, `deliverables`,
`deliverable_preset`, `kind`, `source_type`, `trust`, `checkpoint_mode`,
`on_checkpoint_response`, `blocked_reason` (ambiguous — useful for pure
tracking too, e.g. "blocked, waiting on legal"). `retry_count`,
`escalation_step` are DB-only, scheduler-internal, never exposed on
`TaskRecord`/`TaskCreateInput`/`TaskUpdate` at all — already fully siloed
from the general data-management surface, a good sign for the future
opt-in boundary.

**Not columns on `tasks`, but part of the Task object conceptually:**
Subtodos (JSON blob column, separate accessor methods), Comments (separate
polymorphic table), Artifacts (separate table, out of scope here).

`TaskCreateInput` also currently omits several fields the service already
accepts on create — see §5 Task below.

### 5. Per-entity target tool inventory

Capability targets, not literal `mcp.NewTool` signatures — exact parameter
schemas are an implementation detail for the follow-up build session.

**Task** (closest to done already):
`create` (title-only required, post-§1 fix), `get`, `list` (merged with
`search`; full filter+sort+cursor), `update` (true partial patch,
post-§1 fix), `delete`, `transition` (optionally takes a `comment` string,
atomically posting a comment with the status change), `bulk_transition`,
`bulk_update`, `bulk_delete`, `bulk_tag`. `create` gains the fields the
service already accepts but the tool doesn't expose today: `tools`,
`on_review`, `files`, `cost_budget`, `max_retries`, `permissions`,
`environment`, `max_duration_ms`, `token_budget`, `escalation_chain`,
`quality_gates`, `deliverables`, `blocked_reason`, and an optional initial
`subtodos[]` seed list.

**Subtodo:** `add` (auto-gen id per §1), `list`, `update`, `done`,
`delete`, `bulk_add` (seed a whole checklist in one call). Task-scoped
only, per §1.

**Comment:** `add`, `list` (per-entity thread, now paginated/sortable/
filterable by author+date range), `search` (cross-entity, stays distinct
— see §3), `update`, `delete` (new — a correction path; "comments never
drive lifecycle" stays true, they just become editable/removable by their
author), `bulk_add` (same note to many entity refs at once).
`entity_type` genuinely supports `task`, `project`, `epic`, `sprint`
(Issue/Plan are already covered via `entity_type=task` since they're Task
rows) — closes the "documented as future, never shipped" gap.

**Project:** `create` (schema gains `agent_path`, `icon`, path/permission
arrays already in `ProjectCreateInput` but absent from the tool), `get`
(**missing entirely today**), `update` (**missing entirely today**),
`list` (filter+sort+cursor, `status` actually wired — today's tool
hardcodes an empty status filter), `delete`, `archive`/`unarchive`.

**Epic:** `create`, `get`, `list` (merged search, sort, cursor), `update`,
`delete`, `archive`/`unarchive`, `bulk_update`. `priority` becomes settable
via `create`/`update` (currently read-only through MCP despite existing on
the record and appearing in list responses). Status vocabulary bug
resolved — `torque_epic_update`'s own documented example (`status=closed`)
currently always fails validation because `EpicService.Update` only
accepts `active`/`inactive`; reconcile the enum one way or the other.

**Sprint:** `create`, `get`, `list` (sort, cursor; search if a search
concept proves useful), `update`, `delete`, `archive`/`unarchive`,
`bulk_update`. Keep `start`/`approve` as the scoped-bulk-promote pattern.
Add budget-range filtering.

**Issue:** `create`, `get`, `list` (merged with `search` — same redundancy
as Task), `update`, `delete`, `bulk_update`, `bulk_transition` (shares
Task's FSM since issues are Task rows). `list` pushes `limit` to the DB
layer instead of fetching everything and truncating in Go; gains a
`status` filter.

**Plan:** `create`, `get`, `list` (**dedicated tool — today only reachable
via `torque_task_list kind=plan`**), `update`, `delete`, `add_phase`,
`remove_phase`, `list_children` (limit becomes a real default — today it
hardcodes the system *maximum* as the only limit, regardless of caller
intent). `plan_start`'s bypass of the service layer is execution-adjacent
(it dispatches) — out of scope here, deferred with the rest of execution.

### 6. Open questions (not decided — for the sequencing session)

- **`depends_on` normalization.** Promote to a real join table (enables a
  true FK, but is a bigger structural change) or accept it stays a
  JSON-blob-of-IDs with app-level existence checks and document the
  staleness risk?
- **Feature-flag gating vs. pure-tracking-mode always-on.** Project/Epic/
  Sprint tools are currently registered only when their feature flag is
  enabled (`registerOptInTools`). If "pure tracking mode" (no execution)
  is meant to be first-class, should these always be available regardless
  of execution-related feature flags? Needs a decision that may have
  implications beyond MCP (GUI, HTTP).
- **Cursor vs. offset pagination**, cost/benefit, per §3.
- **Cross-entity Subtodo polymorphism** — explicitly deferred (§1.4), not
  an open question, listed here only as "intentionally not revisited."

---

## Consequences

- **Breaking surface change for existing MCP callers**, if/when
  implemented: merging `_list`/`_search` pairs removes tools
  (`torque_task_search`, `torque_issue_search`); any caller hardcoded to
  the old names needs updating. Not a concern for this ADR (no compat
  guarantee has been made to external callers yet), but worth the
  sequencing session flagging explicitly.
- **Task field fixes (§1) are small, contained, and independent** of the
  larger surface work — they can land on their own schedule.
- **The real-FK promotion (§3) requires a migration** (SQLite table
  rebuild) and a data-cleanup pass for existing orphaned references —
  nontrivial, flagged for scoping.
- **Nothing here touches execution.** Run/Session/Scheduler/Settings/
  Model/Broker/Collection/Artifact/Template are unaffected; a Torque
  install using only these eight entities gets first-class ergonomics
  without ever enabling execution.
- **Discoverability/schema layer is intentionally deferred** — a
  machine-readable reference (enums, required fields, relationship types)
  an agent can query is the last piece, built once the surface above is
  locked, so it doesn't need revision the moment implementation starts.
- **The opt-in lifecycle mechanism itself is intentionally deferred** —
  this ADR identifies which fields are execution/lifecycle-oriented
  (§4) as groundwork, but does not design the opt-in switch.

## Related

- `docs/architecture/mcp-service-layer-audit.md` — full current-state gap
  inventory with file:line citations for every finding referenced above
- Follow-up (not yet written): opt-in lifecycle-mode design
- Follow-up (not yet written): MCP discoverability/schema reference layer
- Follow-up (not yet written): sequencing/scoping plan derived from this ADR
