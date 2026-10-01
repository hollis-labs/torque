# MCP Tools Reference — Data Entities

Agent-facing reference for the `torque_*` MCP tools covering Torque's core
data-management entities: **Task, Tag, Subtodo, Comment, Project, Epic, Sprint,
Issue, Plan**. Task/Subtodo/Comment/Project/Epic/Sprint/Issue/Plan are the
scope ADR-0004 locked and Phase 0–5 of
`tasks/INDEX.md` implemented (all phases `done` as of 2026-08-20) — the
"MCP discoverability/schema reference layer" that ADR-0004's Consequences
section named as a deferred follow-up.

**Out of scope for this doc:** Run, Session, Scheduler, Settings, Model,
Broker, Collection, Artifact, Template, Checkpoint. Those tools exist and
work, but weren't part of this ergonomics pass — see
[`docs/surfaces.md`](surfaces.md#consumer-write-contracts) for their current
consumer write contracts and the full tool-area list, and
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

The shared [API pagination contract](api-pagination.md) defines the S1 target
for HTTP and MCP: cursor defaults, universal 50/200 page sizes, opt-in totals,
clean-break envelopes, and the [current/target endpoint matrix](api-pagination.md#endpoint-capability-matrix).
The adjacent entity section below reflects CW-20261001-0563. Other families
still require S1 reconciliation; their current descriptions do not override
the target. See also the
[sort allow-lists](api-pagination.md#sort-allow-lists-and-defaults) and
[counts, facets, and exports](api-pagination.md#counts-facets-and-exports).

List/search tools return `data = {items: [...], meta: {...}}`. The shared
public page-size policy is **default 50 / maximum 200**, from
`pagination.DefaultLimit` and `pagination.MaxLimit`. It limits one response,
not the number of rows that continuation can traverse.

Cursor metadata includes `{returned, limit, has_more, next_cursor, truncated}`
and optional `total`/`hint`. `next_cursor` is null on the final page. `returned`
is the number of records actually emitted, including after byte-cap trimming.
Cursors bind `sort_by` and `sort_dir`; changing either while retaining a cursor
returns `arg_invalid`. Keep filters unchanged when continuing. Treat the token
as opaque; never construct or parse it client-side.

Task, Run, Comment, Project, Epic, Sprint, Issue, Plan, Tag, and Subtodo lists
currently use cursor envelopes. Subtodos retain checklist position order
(`sort_by=position`, `sort_dir=asc` by default; desc is supported); their parent
stores the whole checklist as JSON, so paging bounds responses rather than
avoiding that parent read. Concurrent checklist insertions/deletions can shift
positions between calls, just as concurrent writes can shift offset pages.

**Pending CW-20261001-0565 integration:** Sessions/session checkpoints,
Artifacts, Collections/collection tasks/inbox, Plan children, Checkpoints/pending
checkpoints, Templates, Models, and message read lists. Their older byte-only
adapters do not yet satisfy the cursor contract. The MCP contract test table
names those families as pending instead of claiming they are converted. Broker
inbox/poll drain operations remain actions, with delivery semantics.

The ~100KB response cap is orthogonal to the row limit. `truncated=true` means
the cap trimmed the page itself, and also sets `has_more=true`. The cursor is
built from the **last emitted row**, so trimmed rows remain reachable. Cursor
pages include a concrete next-call hint preserving filters, sort and projection;
that hint is included in the byte budget. If even one record cannot fit, the
server returns `arg_invalid` with brief/single-record guidance rather than an
empty page that cannot advance.

### Torque server pages and downstream host previews

A host may show only a preview of a tool result and offer a cache pointer or a
`fetch_tool_result` continuation. That continuation recovers the bytes of the
**same Torque response**. It does not fetch remaining matching Torque records.
Read the page's `meta`, then make another call to the **Torque list tool** with
`meta.next_cursor` as `cursor`. Continue until `has_more=false`.

For example, start with:

```json
{"status":"doing","search":"pagination","tags":"[\"api\"]","sort_by":"updated_at","sort_dir":"desc","limit":"50"}
```

If `torque_task_list` returns `meta.next_cursor`, the next server call is:

```json
{"status":"doing","search":"pagination","tags":"[\"api\"]","sort_by":"updated_at","sort_dir":"desc","limit":"50","cursor":"<meta.next_cursor from the preceding Torque page>"}
```

The response's `meta.hint` supplies that call with the real token. Do not replace
it with a host cache cursor or drop filters/sort. For an activity summary, call
`torque_task_facets` with the same cohort filters and
`dimensions="status,project_id"` instead of downloading every task to count
statuses; list cursors and limits do not apply to facets. `torque_task_query`
keeps its existing semantics.

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

- Many numeric and boolean params are declared as MCP **strings** (e.g.
  `"limit": "50"`, `"manual": "true"`) so LLM clients that emit
  string-encoded values don't trip schema validation. Older handlers use
  coercing helpers; strict query and write fields validate exact values and
  may advertise native/string unions. Check the tool's current schema and
  description rather than relying on coercion. `-1` is the unlimited sentinel
  on budget fields (`cost_budget`, `token_budget`, `max_duration_ms`).
- List-of-string params (`tags`, `depends_on`, `ids`, `statuses`, etc.) are
  declared as a JSON-encoded string (e.g. `"tags":"[\"p0\",\"backend\"]"`)
  but the adapter also accepts a native JSON array where the transport
  allows it.
- Adjacent list/search tools use strict query decoding. Explicit malformed,
  blank, fractional, overflow, unsafe native-float, negative `limit`, nonfinite
  numeric bounds, invalid booleans, invalid dates, invalid sorts, and
  sort-mismatched cursors return `arg_invalid` with `field`. Omitted values
  remain defaulted/unfiltered. `entity_ids` on comment list/search accepts a
  JSON array string or native string array; whole null, `[null]`, non-string
  members, and malformed JSON reject; an empty list is rejected only when it is
  the sole scope for `torque_comment_list`.
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
| `torque_task_create` | Create a task. Safety override forces `manual=true` on every create (an agent must promote it via `torque_task_update {"manual":false}` before the scheduler dispatches it) — the response's `dispatch_notice` spells out the exact promotion call. Optional `subtodos[]` seeds an initial checklist atomically. Optional `deliverable_preset` is preserved on create and get, matching HTTP. |
| `torque_task_get` | Fetch one task by id, **including its 10 most recent comments by default** (CW-20260910-0057). `comments="false"` opts out; `comments_limit` widens the window (max 100). |
| `torque_task_list` | Filter + free-text `search` + sort + cursor-paginate, all in one tool (no separate search tool). Shares the public task-query contract with HTTP: status/statuses[]/priority/kind/trust/checkpoint_mode/parent_id/project_id/sprint_id/epic_id/tags[]/manual/agent_profile/launch_profile, `created_*`/`updated_*` RFC3339 ranges, and `*_gte`/`*_lte` budget/duration range filters. `include_internal` (default false) hides `kind=internal` automation rows unless `kind=internal` is requested explicitly. |
| `torque_task_facets` | Count distinct values for supported task dimensions over the same filtered cohort as `torque_task_list`, without fetching task records or counting a page. HTTP equivalent: `GET /api/v1/tasks/facets`. |
| `torque_task_update` | Partial patch. Numeric sentinel `-1` = unlimited on budget fields. `status` is accepted and routed through the same path as `torque_task_transition` — before CW-20260909-0011 the arg was silently dropped and the call still answered `ok:true`. |
| `torque_task_delete` | Hard delete (runs/artifacts/comments cascade). Prefer `transition` to `abandoned` for an audit-preserving close — reachable from any status in one call. |
| `torque_task_transition` | Set a status. Permissive: any status reaches any other in one call (`todo→done` included). Vocabulary: `backlog`, `todo`, `queued`, `doing`, `review`, `done`, `blocked`, `paused`, `archived`, `abandoned`, `cancelled`. Only two refusals — a status outside that list, and leaving `done`/`archived`, which needs `force=true`. Optional `comment`/`comment_author` posts a comment atomically with the transition (one transaction). |
| `torque_task_bulk_transition` | Same status applied to many ids; PRIM-003 `{succeeded, failed}` envelope. |
| `torque_task_bulk_update` | Same field set/semantics as `torque_task_update`, applied across `ids[]`. |
| `torque_task_bulk_delete` | Hard-delete many ids in one call. |
| `torque_task_bulk_tag` | Add/remove tag slugs across many ids — additive, unlike `update`'s `tags` (which replaces the full set). |

Task metadata may include `{"review":{"mode":"parent"}}` to leave a
`kind=agent` task in `review` without enqueueing `reviewer-end-agent`.
Omission preserves the default internal reviewer. Task reads expose the
resolved behavior as `effective_review`; see `docs/review-routing.md`.

---

## Tag

Global tag catalog entity. Source: `internal/mcpadapter/tag_tools.go`.

| Tool | Purpose |
|---|---|
| `torque_tag_list` | Discover the global catalog, including unused tags. Not a usage/count surface; use `torque_task_facets` for scoped tag counts. |
| `torque_tag_get` | Fetch one tag by slug. Historical stored slugs are treated as opaque lookup values and are not revalidated on read. |
| `torque_tag_create` | Create a tag through `TagService` validation/defaults. Slug derives from name only at create when omitted; duplicate slug returns `conflict`. |
| `torque_tag_update` | Partial metadata update. Slug is identity and cannot be changed; omitted fields are untouched, explicit empty description clears, explicit empty color resets to `zinc`. |
| `torque_tag_delete` | Destructive global delete: removes the catalog row and cascades all `task_tags` links. No undo. |
| `torque_tag_merge` | Destructive merge: source and destination must be distinct existing slugs. Destination metadata is preserved, task links are rewritten/deduped to destination, source is deleted, and no alias is retained. |

Tag records use HTTP-aligned snake_case fields:
`slug`, `name`, `description`, `color`, `created_at`, `updated_at`. `slug`
is lookup identity; `name` is mutable display text.

`torque_tag_list` response shape:

```json
{
  "items": [{"slug": "api", "name": "API", "description": "", "color": "zinc", "created_at": "...", "updated_at": "..."}],
  "meta": {"truncated": false, "returned": 1, "limit": 50, "total": 1, "has_more": false, "next_cursor": null}
}
```

Default limit is 50 and max is 200. Explicit `limit<=0`, fractional strings,
and overflows return `arg_invalid`; native JSON numbers are accepted only when
integral. Ordering is `name COLLATE NOCASE ASC, slug ASC`, matching HTTP
`GET /api/v1/tags` default ordering and the cursor predicate. `query` is a
literal substring over slug/name/description with `%`, `_`, and backslash
escaped; `color` is exact equality. `meta.next_cursor` is derived from the last
tag actually emitted after the MCP 100KB response cap, so byte-cap trimming
does not skip catalog rows.

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

Priority is validated identically on the **write** paths. `torque_task_create`,
`torque_task_update`, `torque_task_bulk_update`, `torque_plan_create`,
`torque_plan_update`, `torque_epic_create`, `torque_epic_update` and
`torque_epic_bulk_update` reject a non-integer `priority` with
`error.code=arg_invalid, field=priority` rather than coercing it. Before this,
writes ran the argument through a lenient parser that yielded `0` for anything
unparseable, so `{"priority":"high"}` on create silently stored the default `2`
and `{"priority":"banana"}` on update silently overwrote a real priority with
`0` while still answering `ok=true`. String-encoded integers (`"3"`) remain
accepted everywhere, and `priority=0` on create remains the "unset" sentinel
that defaults to `2`. A rejected write modifies nothing.

`include_total` is optional on MCP and defaults to false to preserve cheap
legacy list calls. When `include_total=true`, `meta.total` is the full matching
cohort count excluding cursor/offset/limit, and it is part of the normal
100KB-capped response sizing. HTTP includes `meta.total` only with `include_total=true` in its
`{items,meta}` task-list envelope. HTTP keeps its GUI compatibility alias `priority=1,2` as a
comma-separated exact-integer OR-list, including `0`; repeated HTTP query keys,
malformed raw query strings, malformed/overflow/blank CSV members, unknown
keys, and invalid shared-query fields return `400` with `error` and `field`.

### Task set, range, and presence filters (CW-20260911-0088)

Task lists and facets share these operators through the same service/store
predicate path; all active filter families combine by AND.

| Operator | Meaning |
|---|---|
| `tags` | Has every selected tag (existing behavior). |
| `tags_any` | Has at least one selected tag. |
| `tags_none` | Has none of the selected tags. |
| `priority_gte`, `priority_lte` | Inclusive exact integer bounds, intersected with any exact priority set. |
| `missing`, `present` | Every named field is missing/present, respectively. |

Tag values remain case-sensitive opaque slugs; lists trim whitespace and
drop blank/duplicate slugs. Empty normalized tag lists add no filter.
An unused catalog tag does not imply task membership. Reversed priority bounds,
missing and present on the same field, or `missing=["tags"]` with a nonempty
`tags_any` produce an empty cohort without hidden precedence.

Presence fields are limited to `project_id`, `sprint_id`, `epic_id`,
`parent_id`, `source_ref`, `collection_id`, `cost_budget`, `token_budget`,
`max_duration_ms`, and `tags`. SQL NULL is missing; empty strings and zero
are present. Tags are present when a task has at least one tag link.
Duplicate field names are ignored; unknown/blank names reject. This does not
query metadata paths or alter the legacy `parent_id=""`/`"null"` sentinel.

MCP `tags_any`, `tags_none`, `missing`, and `present` accept native string
arrays or JSON array strings. `[]` is an explicit no-op; null, empty strings,
non-string/null members, and malformed/trailing JSON reject with
`arg_invalid` on the parameter. HTTP uses CSV instead; exact empty CSV is
an empty list, while whitespace-only presence fields and blank members reject.
Bounds use the strict integer parser (including zero/negative values); use
strings for exact values beyond native JSON safe-integer precision.

Example MCP list:
`{"tags_any":["api","ui"],"tags_none":["blocked"],"missing":["sprint_id"],"priority_lte":"2","include_total":"true"}`.
The equivalent HTTP query is
`/api/v1/tasks?tags_any=api,ui&tags_none=blocked&missing=sprint_id&priority_lte=2`.
Use the same cohort filters with `torque_task_facets` or
`/api/v1/tasks/facets` plus `dimensions` to get matching counts.

### Adjacent entity query defaults

Project, Epic, Sprint, Issue, and Comment list/search tools share cursor
validation with their HTTP counterparts. Both always return the paged
`{items,meta}` payload (MCP inside `data`), default 50/max 200. `include_total`
defaults to false; true adds `meta.total` for the full filtered cohort, excluding
cursor/limit. Otherwise total is omitted. Legacy HTTP fetch-all envelopes are
removed, and offset is unsupported on these lists.

| Tool | Filters/search | Default/max/order |
|---|---|---|
| `torque_project_list` | `status`, `include_archived`, `search` over ID/name/description | 50/200, `name asc`; sort fields `name,status,updated_at,created_at` |
| `torque_sprint_list` | `status`, `project_id`, `include_archived`, `over_budget`, `cost_budget_min`, `cost_budget_max`, `search` over ID/name/goal | 50/200, `updated_at desc`; sort fields `name,status,updated_at,created_at` |
| `torque_epic_list` | `status`, `project_id`, `include_archived`, `search` over id/name/description | 50/200, `updated_at desc`; sort fields `name,status,updated_at,created_at` |
| `torque_issue_list` | `project_id`, `status`, `query` over id/title/body | 50/200, `priority asc`; sort fields `priority,status,updated_at,created_at` |
| `torque_comment_list` | Required `entity_type` plus `entity_id` or non-empty `entity_ids`; optional `author`, `created_after`, `created_before` | 50/200, `created_at asc`; only sort field `created_at` |
| `torque_comment_search` | Required `query`; optional `entity_type`, `entity_id`, `entity_ids`, `author`, `created_after`, `created_before` | 50/200, `created_at desc`; only sort field `created_at` |

When both `entity_id` and `entity_ids` are supplied to comment tools,
`entity_id` takes precedence in the store predicate. Use RFC3339 for
`created_after`/`created_before`; cursors preserve subsecond timestamp
precision and must be passed back unchanged.

### Task facets and counts (CW-20260911-0086)

`torque_task_facets` and `GET /api/v1/tasks/facets` share the task-list cohort
contract. Every active filter applies to every requested facet, including a
filter on the same dimension; facets answer "what values exist inside this
exact cohort," not "what filters could I add if this one were removed."

Supported dimensions are `status`, `priority`, `manual`, `kind`, `executor`,
`agent_profile`, `launch_profile`, `project_id`, `sprint_id`, `epic_id`,
`parent_id`, and `tags`. Omitted `dimensions` returns all of them. HTTP encodes
dimensions as comma-separated CSV (`dimensions=status,priority,tags`); MCP
accepts a native string array or JSON array string. Explicit empty/blank
dimensions are invalid. Duplicates are removed deterministically.

`bucket_limit` defaults to 50 and caps at 200. Task-list row controls
(`limit`, `offset`, `cursor`, `sort_by`, `sort_dir`) are rejected on the
dedicated facet endpoint/tool by presence, including zero or empty-string
spellings, because facet counts are always whole-cohort SQL aggregates.

Response shape:

```json
{
  "matching_count": 42,
  "bucket_limit": 50,
  "dimensions": ["status"],
  "facets": [
    {
      "dimension": "status",
      "total_distinct": 2,
      "returned": 2,
      "truncated": false,
      "buckets": [{"value": "doing", "count": 31}, {"value": "review", "count": 11}]
    }
  ]
}
```

Bucket values are native JSON scalars or `null`; JSON `null` is the null/missing
bucket and is distinct from the literal strings `""` and `"null"`. `manual`
values are booleans and `priority` values are numbers. `total_distinct`
includes the null bucket when present. Buckets sort by `count DESC`, then among
equal counts non-null values before null, then typed value ascending.

Tags are multi-valued: a matching task contributes once to each distinct linked
tag bucket. Untagged matching tasks contribute once to the `null` bucket.

Examples:

```json
{"project_id":"PRJ-1","sprint_id":"SP-1","manual":"auto","dimensions":["status","priority","tags"]}
{"status":"doing","include_internal":"true","agent_profile":"codex-implementer","dimensions":["executor","launch_profile","tags"]}
```

HTTP returns the normal JSON body without the MCP tool-response byte cap. MCP
facet responses enforce the same 100KB tool cap as list responses by omitting
whole buckets only; values are never truncated into different opaque values.
`matching_count` and `total_distinct` remain whole-cohort counts; `returned`
and `truncated` describe the buckets actually emitted after any MCP byte-cap
trim.

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
covered via `entity_type="task"` since they're Task rows). Edit and default
delete require an exact match to the original `author` string (mismatch →
`error.code=permission`). Delete accepts an explicit `force=true` override
for that match only. Author is free text, not an authenticated principal. Source:
`internal/mcpadapter/comment_tools.go`.

Delete's `force` is a native boolean, unlike older string-encoded boolean
parameters. Omitting it is equivalent to `false`.

| Tool | Purpose |
|---|---|
| `torque_comment_add` | Post a comment on one entity. |
| `torque_comment_list` | Chronological thread for one `entity_id`, or for a caller-resolved set via `entity_ids[]` (same `entity_type`) — e.g. every task in a sprint. Default oldest-first. |
| `torque_comment_search` | Free-text `query` over comment content, optionally scoped by entity/entity_ids/author/date range. Default newest-first. Always returns full records (no brief/verbose toggle). |
| `torque_comment_update` | Author-scoped content edit. |
| `torque_comment_delete` | Hard delete with an exact-author guard; optional `force=true` bypasses only that guard. Author must still be supplied; explicit `""` is allowed. No undo. HTTP equivalent: `DELETE /api/v1/comments/{id}?author=...&force=true`. |
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
| `torque_project_create` | Create. Optional `status` is exactly `active` or `inactive`; omission defaults to `active`. `repo_path` must resolve to an existing directory (`~` expands) — a missing path is `error.code=arg_invalid, field=repo_path`. |
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
| `torque_plan_list_children` | List tasks under the plan (optionally narrowed to one `phase_id`). Cursor-paged with the shared 50/200 policy and opt-in cohort total; phase/task filters apply before paging. |
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
The match is retained as an accidental-deletion guard. `force=true` now
provides a deliberate override without claiming an authorization model.

## Related docs

- `docs/adr/0004-mcp-data-entity-agent-ergonomics.md` — the target-shape
  decision this doc reflects the implementation of.
- `docs/architecture/mcp-service-layer-audit.md` — the pre-implementation
  gap audit (historical; describes the *old* surface, not current behavior).
- `tasks/INDEX.md` — the phased task breakdown that executed the ADR.
- `docs/surfaces.md` — the full HTTP + MCP tool-area list, including
  entities out of this doc's scope.

### Run list cursor contract

`torque_run_list` queries across tasks by default; `task_id` is optional.
It accepts task/project/sprint/epic scopes, CSV `status`, inclusive `since`
and `until` (RFC3339 or Unix milliseconds), `limit`, `offset`, `cursor`,
`sort_by`, `sort_dir`, `include_total`, and `verbose`. Sorts are `started_at`
(default descending), `status`, `duration` and `cost`, with numeric ID
ascending for ties. Duration is rounded completed elapsed milliseconds;
unfinished runs sort at -1.

The payload is `{items,meta:{returned,limit,has_more,next_cursor,total?}}`: page
size defaults to 50 and clamps at 200, total is opt-in, and the final cursor
is null. Cursor/sort mismatches and positive offset plus cursor reject with
`arg_invalid`. Byte trimming preserves continuation from the last emitted
row. See [the runs contract](api-pagination.md#runs-implementation-reconciliation-cw-20261001-0562).

## Entity and run facets (CW-20261001-0564)

Use facets for stat cards and filter choices without paging list records.
These tools are read-only and idempotent. Parent tools follow their entity's
feature flag; run facets are always registered.

| Tool | Filters (same as list) | Dimensions |
|---|---|---|
| `torque_project_facets` | `status`, `search`, `include_archived` | `status` |
| `torque_epic_facets` | `status`, `project_id`, `search`, `include_archived` | `status`, `project_id`, `priority` |
| `torque_sprint_facets` | `status`, `project_id`, `search`, `include_archived`, `over_budget`, `cost_budget_min`, `cost_budget_max` | `status`, `project_id`, `approval_mode` |
| `torque_run_facets` | `task_id`, `project_id`, `sprint_id`, `epic_id`, CSV `status`, inclusive `since`/`until` (RFC3339 or Unix milliseconds) | `status`, `executor`, `profile` |

`dimensions` is CSV (defaults to all supported, duplicates removed).
`bucket_limit` defaults to 50, zero uses the default, values above 200 clamp,
and negative/malformed values reject. Row `limit`, `offset`, `cursor`,
`sort_by`, `sort_dir` reject, even when blank. Unknown arguments reject.

Response `data` is `{matching_count,bucket_limit,dimensions,facets:[{dimension,
buckets:[{value,count}],total_distinct,returned,truncated}]}`. Buckets order by
count descending, then typed value ascending with null last among equal counts.
Null is distinct from empty strings. Filters apply to every dimension, including
its own dimension. Empty cohorts return empty buckets and zero counts.

Parent responses add `task_rollups:{scopes:[{scope_id,total,counts}],
total_distinct,returned,truncated}`. Each matching parent contributes a scope,
including parents with no tasks. Internal tasks are excluded. `counts` maps raw
task statuses to counts; all statuses for each returned parent are complete.
Parents order by task total descending, then ID ascending. `bucket_limit`
bounds the number of parent groups independently of each facet dimension.

Run responses add `totals:{cost,prompt_tokens,completion_tokens}`. Cost sums
canonical `cost_ledger` entries joined by run ID to the exact matching runs;
run records supply tokens. `since`/`until` filter run start times, not ledger
entry timestamps. Runs without ledger entries contribute zero cost. Multiple
ledger entries never multiply token/count totals. `profile` is the **current
task profile, not the profile at run time**: nonempty task `launch_profile`,
fallback `agent_profile`. No historical profile snapshot is stored on runs.

MCP's byte budget can further trim value buckets or whole parent groups;
`returned` and `truncated` describe that reduction. Exact `matching_count`,
`total_distinct`, and run totals are retained. HTTP routes are `/api/v1/projects/facets`,
`/epics/facets`, `/sprints/facets`, and `/runs/facets` under the same `/api/v1`
prefix. GUI consumption is S3 work.

Example: `torque_run_facets` with `{"project_id":"PRJ-1",
"since":"2026-10-01T00:00:00Z","dimensions":"status,profile","bucket_limit":"20"}`.


## Run time-series aggregate

`torque_run_timeseries` / HTTP `GET /api/v1/runs/timeseries` returns
`{bucket,tz_offset_minutes,since,until,buckets,totals}`. Each bucket has
`{start,count,prompt_tokens,completion_tokens,cost,status_counts}`; totals
has `{count,prompt_tokens,completion_tokens,cost}`. Empty buckets are present
with zero totals and an empty status map. Buckets are chronological and
`start` is the UTC instant of the local bucket edge.

Required `since`/`until` use the inclusive run-start filters from
`torque_run_list` (RFC3339 or Unix millis). Optional `task_id`, `project_id`,
`sprint_id`, `epic_id` and CSV `status` share the same cohort predicates.
`bucket` is hour or day, default day; `tz_offset_minutes` is a fixed offset
integer between -840 and 840, default 0. Fixed offsets do not model DST.
An inclusive upper bound exactly on an edge includes that edge's bucket.

The requested window may intersect at most 200 buckets; larger windows return
`arg_invalid` on `until`, without truncation. Row limit/offset/cursor/sort,
include_total and bucket_limit are rejected. Ledger cost sums by run ID
through the matching cohort; ledger dates never define membership. Tokens
come from runs and are not multiplied by ledger rows. Sums equal run facets
and optional list counts for the same filters. If the whole MCP envelope
exceeds the byte budget, it rejects on `until` and asks for a shorter window
or narrower filters, retaining the complete-series contract.

Example: `{"since":"2026-10-01T00:00:00Z","until":"2026-10-01T23:59:59.999999999Z","bucket":"hour","tz_offset_minutes":"0"}`.

## Remaining cursor list families (CW-20261001-0565)

Sessions, artifacts, collections and their task/inbox views, plans and their
children, task/pending/recovery checkpoints, templates, and models now use
`{items,meta}` with default 50/max 200, cursor continuation, optional explicit
offset, and opt-in `include_total`. Their existing brief/verbose record shapes
remain. HTTP and MCP share the normalized service/store queries; MCP byte
trimming resumes after the last emitted item. See the implemented
[endpoint and sort matrices](api-pagination.md#endpoint-capability-matrix).

`torque_session_checkpoint_list` takes required `session_id` and the shared
page query; it lists runtime recovery records, separate from task HITL records.
Task checkpoint lists keep required `task_id`; pending checkpoints retain the
pending scope. Templates list version rows with ID/version identity, and
models use provider/model identity.

There is no pure-read MCP message-thread list. `torque_broker_inbox` and
`torque_inbox_poll` remain delivery actions. HTTP inbox uses exact-size drain
batches with a non-mutating continuation peek; HTTP thread uses bounded
federated read pages. The [messages contract](api-pagination.md#endpoint-capability-matrix)
describes their count and continuation differences.
