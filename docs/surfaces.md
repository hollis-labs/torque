# Surfaces

This page is intentionally high level. It lists the major live surfaces without trying to exhaustively duplicate schemas already enforced in code and tests.

## HTTP API

Base path: `/api/v1`

Main route groups:

- `/tasks`
- `/checkpoints`
- `/checkpoint-workflows` for HITL checkpoint schema lookup
- `/templates`
- `/projects`
- `/sprints`
- `/epics`
- `/plans`
- `/tags`
- `/runs`
- `/artifacts`
- `/comments`
- `/settings`
- `/scheduler`
- `/features`
- `/admin`
- `/events` for SSE

The same `serve` process also serves the GUI SPA.

Task-list query contract (`GET /api/v1/tasks`): omitted filters are
unfiltered/defaulted, while malformed explicit values return `400` with
`error` and `field`. HTTP and MCP use the same service-level task query
contract for filters, validation, default sort, paging, and optional count
metadata; HTTP encodes multi-value filters as CSV query parameters where MCP
uses JSON/native arrays.

Supported HTTP filters are `status` (CSV OR-list), `priority` (CSV exact
integer OR-list), `priority_gte`/`priority_lte`, `sprint_id`, `project_id`, `epic_id`, `executor`, `kind`,
`include_internal`, `source_type`, `source_ref`, `trust`, `checkpoint_mode`,
`parent_id`, `manual`, `tags`/`tag`, `tags_any`, `tags_none`, `missing`, `present`, `search`, `agent_profile`,
`launch_profile`, RFC3339 `created_after`/`created_before` and
`updated_after`/`updated_before`, and numeric range bounds
`cost_budget_gte`/`cost_budget_lte`, `token_budget_gte`/`token_budget_lte`,
`max_duration_ms_gte`/`max_duration_ms_lte`, and
`max_retries_gte`/`max_retries_lte`. `sort_by` accepts
`priority|status|updated_at|created_at`; `sort_dir` accepts `asc|desc`.
The public default order is `priority asc, id asc`. The lower-level
`TaskService.List`/store default used by scheduler/internal callers is
unchanged.

`priority` values are exact integers, including `0` and full signed int64
values on 64-bit builds; there are no urgency labels or 1-5 list-query
restrictions. Repeated query keys, malformed query strings, blank/overflow/
non-integer priority members, malformed `limit`/`offset`, invalid
`manual`/`include_internal` aliases, unknown keys, invalid sort/date/cursor
values, non-finite cost bounds, negative offsets, and positive
`offset`+`cursor` combinations all return `400` rather than being ignored.

The same operators apply to task lists and task facets. All filter families
combine by AND: `tags` requires every selected tag, `tags_any` at least one,
and `tags_none` excludes every selected tag. Tag lists trim whitespace and
drop blank/duplicate slugs; empty lists add no restriction. Matching uses task
links, so an unused catalog tag matches no tasks. Priority bounds are inclusive,
exact integers and intersect with any exact priority set. Reversed bounds or
contradictory filters produce an empty cohort.

`missing` and `present` accept CSV field lists from this whitelist:
`project_id`, `sprint_id`, `epic_id`, `parent_id`, `source_ref`,
`collection_id`, `cost_budget`, `token_budget`, `max_duration_ms`, `tags`.
SQL NULL is missing; an empty stored string or numeric zero is present.
For `tags`, missing means no task-tag links and present means at least one.
Duplicate fields are ignored; unknown/blank fields reject. Exact empty CSV
(`missing=`) is an empty list; whitespace-only input is invalid.
For example, `?tags_any=api,ui&tags_none=blocked&missing=sprint_id&priority_lte=2`
selects unassigned tasks with either tag, without the excluded tag, inside the
numeric bound. These names carry no scheduling meaning.

Task lists keep the legacy HTTP envelope and always include `total`, where
`total` is the full matching cohort under the supplied filters, excluding
cursor/offset/limit. Page-specific metadata is additive: `returned`, effective
`limit`/`offset`, `has_more`, `next_offset`, `next_cursor`, `sort_by`,
`sort_dir`, and `continuation` describe the current page. Offset calls keep
offset-shaped `continuation`; cursor calls advance with `cursor`, `sort_by`,
and `sort_dir` and do not emit a misleading positive `next_offset`. The bounded
default page size is 50 and the maximum effective `limit` is 200; `limit<=0`
uses the default and oversized limits are clamped. Offset pagination is not a
snapshot: concurrent writes between requests can move later pages.
Clients that need a complete refreshed task set, including the GUI shared API
client, must follow `continuation` until `has_more=false`; clients that pass a
positive `limit` should treat it as their own overall cap.

Tag catalog contract (`GET /api/v1/tags`): with no query string, the endpoint
keeps the legacy GUI-compatible shape `{tags:[...]}` and legacy ordering by
`name COLLATE NOCASE ASC`. Supplying any query string opts into the bounded
catalog query path; supported keys are exactly `query`, `color`, `limit`, and
`cursor`, and unknown or repeated keys return `400` with `error` and `field`.
`query` is a literal substring over slug/name/description; `%`, `_`, and
backslash are escaped rather than treated as wildcards. `color` is exact
equality, not trimmed. The opt-in response is `{tags,total,returned,limit,
has_more,next_cursor,sort_by,sort_dir}` where `total` is the full filtered
catalog count excluding cursor/limit. Ordering and cursor predicates both use
`name COLLATE NOCASE ASC, slug ASC`, so duplicate/case-variant display names
page stably. Default limit is 50, max is 200; explicit `limit<=0`, fractions,
and overflows are rejected.

Adjacent entity HTTP lists (`/projects`, `/sprints`, `/epics`, `/issues`, and
`/comments`) preserve their legacy fetch-all response shapes for existing
simple calls, and opt into bounded cursor mode only when an advanced key is
present. Before that mode choice, handlers reject malformed raw query strings,
unknown keys, and repeated scalar keys with `400` plus `field`; an
unknown-only request never falls through to a successful unfiltered legacy
list. Explicit blank, malformed, fractional, overflow, unsafe, or negative
`limit` values are rejected. Cursor tokens are opaque and tied to the
`sort_by`/`sort_dir` that issued them.

Advanced adjacent responses use `{items:[...], meta:{returned, limit,
has_more, next_cursor, sort_by, sort_dir}}`. They deliberately make no
whole-cohort `total` claim unless a count is actually computed. Legacy
`GET /api/v1/issues/search?q=...` keeps its old `total=len(page)` value; that
is page length compatibility, not a cohort count.

| Route | Legacy keys and shape | Advanced opt-in keys | Default/max/order |
|---|---|---|---|
| `GET /projects` | `status`, `include_archived`; `{projects:[...]}` fetch-all | `limit`, `cursor`, `sort_by`, `sort_dir` | 100/500, `name asc`; sort fields `name,status,updated_at,created_at` |
| `GET /sprints` | `status`, `project_id`; `{sprints:[...]}` fetch-all | `include_archived`, `over_budget`, `cost_budget_min`, `cost_budget_max`, `limit`, `cursor`, `sort_by`, `sort_dir` | 100/500, `updated_at desc`; sort fields `name,status,updated_at,created_at` |
| `GET /epics` | `status`, `project_id`; `{epics:[...]}` fetch-all | `include_archived`, `search`, `limit`, `cursor`, `sort_by`, `sort_dir` | 100/500, `updated_at desc`; sort fields `name,status,updated_at,created_at` |
| `GET /issues` | `project_id`; `{issues:[...], total}` fetch-all | `status`, `query`, `limit`, `cursor`, `sort_by`, `sort_dir` | 50/200, `priority asc`; sort fields `priority,status,updated_at,created_at` |
| `GET /issues/search` | `q`, optional `project_id`, `limit`; `{issues:[...], total=len(page)}` | `status`, `query`, `cursor`, `sort_by`, `sort_dir`; `limit` is shared | 50/200, `priority asc`; `q` and `query` together are rejected as ambiguous |
| `GET /comments` | `entity_type`, `entity_id`; raw `[...]` fetch-all | `author`, `created_after`, `created_before`, `entity_ids`, `limit`, `cursor`, `sort_by`, `sort_dir` | 50/200, `created_at asc`; only sort field `created_at` |
| `GET /comments/search` | none | `query`, optional `entity_type`, `entity_id`, `entity_ids`, `author`, `created_after`, `created_before`, `limit`, `cursor`, `sort_by`, `sort_dir` | 25/100, `created_at desc`; only sort field `created_at` |

HTTP `entity_ids` is a JSON-string query value such as
`entity_ids=["T-1","T-2"]`; URL-encode it in real requests. Whole `null`,
`[null]`, non-string members, and malformed JSON are invalid. An empty
`entity_ids=[]` is invalid as the only scope for `GET /comments`, but remains
valid with `entity_id` present or for unscoped `GET /comments/search`. When
both `entity_id` and `entity_ids` are supplied on flat comment routes,
`entity_id` takes precedence in the store predicate. Nested
`/tasks/{id}/comments` remains fixed to that task: `entity_type` must be
`task` if supplied, `entity_id` must match the path id if supplied, and
`entity_ids` is rejected rather than widening scope. Comment date filters use
RFC3339 timestamps.

## MCP

Core MCP areas exposed by the adapter:

- Health
- Tasks
- Tags
- Runs
- Artifacts
- Comments
- Settings
- Scheduler
- Checkpoints
- Templates
- Subtodos
- Plans
- Issues

Feature-flagged MCP areas:

- Projects
- Sprints
- Epics
- Collections

The MCP adapter is thin over the service layer. The code under `internal/mcpadapter/` is the authoritative place to inspect current tool names and descriptions.

Task-list MCP query contract (`torque_task_list`): explicit malformed
numbers, booleans, dates, directions, cursors, and wrong native types return
`error.code=arg_invalid` with `error.field`; omitted values remain
unfiltered/defaulted. Priority filters are exact arbitrary integers, including
`0`. Use scalar `priority` for one value or `priorities` as a non-empty JSON
array for OR-matching; passing both is rejected as ambiguous, and
`priorities=[]` is rejected. `include_total=true` additively includes
`meta.total` for the full matching cohort, excluding cursor/offset/limit; it
defaults to false so legacy lightweight calls avoid a full count. Cursor
pagination still derives `meta.next_cursor` from the last row actually emitted
after the 100KB response cap is applied.

MCP task lists and facets expose the same `priority_gte`/`priority_lte`,
`tags_any`, `tags_none`, `missing`, and `present` operators described above.
The four new array parameters accept native arrays or JSON array strings;
`[]` explicitly adds no filter. Whole null, empty strings, malformed arrays,
and non-string/null members reject. Use string-form integer bounds when
values exceed native JSON safe-integer precision.

Tag catalog MCP tools are always registered: `torque_tag_list`,
`torque_tag_get`, `torque_tag_create`, `torque_tag_update`,
`torque_tag_delete`, and `torque_tag_merge`. Records use snake_case fields
aligned with HTTP tags: `slug`, `name`, `description`, `color`, `created_at`,
`updated_at`. `slug` is the immutable lookup identity; `name` is mutable
display text. `torque_tag_list` is global catalog discovery, including unused
tags, with the same query/color scope and `name COLLATE NOCASE ASC, slug ASC`
ordering as HTTP. Its cursor comes from the last tag actually emitted after
the MCP 100KB cap, so byte trimming does not skip values. Delete removes the
global tag and cascades task-tag links; merge preserves destination metadata,
dedupes/repoints task links, deletes the source, and stores no alias.

For the eight data-management entities (Task, Subtodo, Comment, Project, Epic, Sprint, Issue, Plan), [mcp-tools-reference.md](mcp-tools-reference.md) is a per-tool index plus the shared conventions (response envelope, pagination, sort, bulk-op shape) — the "MCP discoverability/schema reference layer" [ADR-0004](adr/0004-mcp-data-entity-agent-ergonomics.md) scoped and deferred.

## Consumer write contracts

HTTP and MCP share the service operations below. These fields carry consumer
data or execution settings; they do not introduce a prescribed naming vocabulary.

- Task create accepts `deliverable_preset` on both surfaces. Omission or an
  empty string leaves it unset; named presets round-trip through task get.
  MCP create still forces `manual=true` until explicitly promoted.
- Template create/update expose `cost_budget`, `max_retries`,
  `max_duration_ms`, and `token_budget` on both surfaces, with the shared task
  budget validation. Omitted retries default to 3 on create. Explicit
  `max_retries=0` survives versioning and instantiation and means no retries;
  omitting it on update preserves the stored value. Integer budgets require
  exact integers, and cost values must be finite.
- Project create accepts initial `status` on both surfaces. Omission defaults
  to `active`; explicit values must be exactly `active` or `inactive`, as on
  update. Empty, whitespace-only, and unknown values are rejected.

### Artifacts

`torque_artifact_create` and HTTP `POST /api/v1/artifacts` accept `run_id`
and `metadata`. A linked run must exist and belong to the artifact's task.
Metadata accepts a native JSON object or a string containing one JSON object.
Use the JSON-string form for MCP metadata integers beyond native JSON's safe
integer range; that form preserves number precision. Arrays, scalars, and
malformed metadata are rejected.

`torque_artifact_update` (`artifact_id`) and HTTP
`PATCH /api/v1/artifacts/{id}` patch `type`, `content`, `url`, `file_path`,
`run_id`, and `metadata`. Omitted fields stay unchanged. Empty strings clear
`content`, `url`, and `file_path`; explicit JSON `null` clears `run_id` or
`metadata`. Supplied metadata replaces the whole object. An empty object `{}`
is stored as an object, not cleared.
Supplied `type` must remain nonblank. Updates preserve the artifact ID, task
ownership, and creation timestamp, and invalid patches leave the row unchanged.
Changing or clearing a file pointer does not remove the file.

HTTP create retains its `{id}` response; HTTP patch and MCP update return the
artifact record. Use get for the stored record, including its creation time.

`GET` and `HEAD /api/v1/artifacts/{id}/content` resolve relative `file_path`
against the owning task's existing, absolute `working_dir`. An unusable task
root returns `422`; the server's working directory is never a fallback.
Absolute paths retain their existing behavior. The resolved file must still
fall inside an allowed root after symlink resolution: the task's working
directory, `TORQUE_DATA_DIR`, or `$HOME/Projects-apps`.

### Session launch

Both `torque_session_create` and its `torque_session_launch` alias accept
`env` and `meta` as native objects with string values or JSON-object strings.
HTTP `POST /api/v1/sessions/launch` retains its `env: ["KEY=value"]` shape;
its `meta` accepts the same string-map representations. Both paths feed the
same boot options. Supplied nulls, arrays, scalars, non-string members, and
empty or malformed JSON strings are rejected for string-map inputs.

`launch_profile` retains precedence over `agent_profile`, and launch remains
long-lived. Caller metadata cannot replace the known Torque-owned keys
`torque.mode`, `torque.boot_dir`, `torque.workspace_dir`,
`torque.parent_session_id`, or `torque.run_id`; boot stamps the actual values
when applicable. Other metadata keys are preserved.

### Single-comment deletion

`torque_comment_delete` and HTTP `DELETE /api/v1/comments/{id}` share the
same operation. HTTP takes `author` and optional `force` as query parameters,
for example `?author=reviewer&force=true`. The author must be supplied even
with force; an explicitly empty author matches an unattributed comment.

Omitted `force` or `false` keeps exact author matching. Explicit `true`
bypasses only that match; invalid IDs and missing comments still fail. Author
text is an accidental-deletion guard, not an authenticated identity. Deletion
has no undo. MCP requires a native boolean for `force`; HTTP force values
must be exactly `true` or `false`. Malformed, unknown, and repeated HTTP query
parameters are rejected.

## GUI

The frontend currently includes pages for:

- Dashboard
- Board / operations
- Tasks
- Runs
- Checkpoints
- Templates
- Plans
- Settings
- Project detail
- Sprint detail
- Epic detail

The GUI is served by the same `torque serve` process.
