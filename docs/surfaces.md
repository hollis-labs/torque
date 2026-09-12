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
