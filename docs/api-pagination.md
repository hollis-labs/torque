# API list and pagination contract

**Status: S1 target specification, pending implementation reconciliation.**
CW-20261001-0561 defines the HTTP and MCP list contract for
EP-20261001-0013, *Server-driven lists — query, sort, cursor-page, never
fetch-all*. The normative sections below describe the intended completed
contract. The matrices explicitly separate it from the source-checked baseline
at `a672493` (2026-10-01), with runs reconciled for CW-20261001-0562 and the five
adjacent-family rows updated for CW-20261001-0563. They do not claim the complete target is already deployed.
Reconcile this document against the merged S1 implementation before declaring
it a current API reference.

## One list contract

Every resource list and search is server-filtered, server-sorted, and paged by
default, including nested lists. HTTP returns `{items, meta}`; MCP puts that
same payload inside its existing `{ok:true, data:{items, meta}}` envelope.
Projection differences (brief, verbose, summary, typed records) do not change
paging or cohort semantics.

Legacy fetch-all shapes no longer exist in the completed contract. Responses
such as `{sprints:[...]}`, `{runs:[...]}`, `{tasks:[...]}`, raw arrays, and the
legacy/advanced opt-in mode switch are removed in a **clean break, with no
backwards compatibility**. GUI, Tachyon, other clients, and documentation must
move together. Omitting query parameters returns the first bounded page; it
never opts into fetching everything.

### Request parameters

| Parameter | Contract |
|---|---|
| `limit` | Default **50**, maximum effective **200**, for every list/search on HTTP and MCP. No per-resource exceptions. Oversized positive values clamp to 200. Zero selects the default. Explicit malformed, fractional, overflow or negative values reject; omit to select the default. |
| `cursor` | Opaque continuation from `meta.next_cursor`; omit on the first request. Cursor pagination is the default. |
| `sort_by` | One field from the resource's allow-list below. Unknown fields reject. |
| `sort_dir` | `asc` or `desc`; omit for the resource default. |
| `offset` | Non-negative integer, only for a supported jump-to-page UI. Do not use it for ordinary traversal. Availability must be documented per endpoint; it is not an automatic requirement for every resource. |
| `include_total` | Boolean, default false; true requests the exact whole-cohort count. |
| Resource filters/query | Applied before sorting and paging, shared between transports. Preserve required path scopes. Unsupported or malformed explicit values reject rather than silently widening the query. |

Cursor and sort work together: send the same resolved `sort_by` and `sort_dir`
on continuation requests. A malformed cursor or one issued for another sort
rejects. A positive `offset` cannot be combined with a `cursor`, including a
request that also supplies sort parameters. Sort plus offset without a cursor
is allowed where offset is supported. `offset=0` does not advance the page.
For tasks (`/tasks`, `/tasks/search`, `torque_task_list`) and runs (`/runs`,
`torque_run_list`), an **explicit `offset` parameter, including `offset=0`,
with no non-empty cursor selects offset mode**. Its metadata includes `offset`
and `next_offset`: the latter is `offset + returned` when `has_more=true` and
null otherwise. Without an explicit offset, the default is cursor mode and
both offset fields are omitted. `offset=0` with a non-empty cursor remains
cursor mode; a positive offset with a non-empty cursor rejects. Clients starting
an offset traversal must send `offset=0` explicitly on its first page.

Keep filters and scope identical while traversing; when either changes, discard
the cursor and start again. Cursors are not portable between resources.

Sorting uses the selected field and a deterministic unique tie-break key
(usually `id asc`; models require provider plus model identity). Clients must
not decode or construct cursor tokens. Changing data between pages is not a
snapshot guarantee: updates to sort keys can change membership/order. Offset
paging is additionally subject to rows shifting before the requested offset.

### Response metadata

```json
{
  "items": [],
  "meta": {
    "returned": 0,
    "limit": 50,
    "has_more": false,
    "next_cursor": null
  }
}
```

`returned` is the number of items actually emitted, never a cohort count.
`limit` is the effective page size. `has_more` indicates whether another page
exists; the server can read `limit+1` rows internally to determine it without a
COUNT. `next_cursor` identifies the last emitted row when more rows exist, and
is null on the final page, including an empty result. Follow it until
`has_more=false`, requesting pages as the consumer needs them.

With `include_total=true`, add `meta.total`, an exact non-negative count of the
matching cohort **before cursor, offset, and limit**, using the same filters,
visibility rules, and required scope as the items query. Return `total:0` for an
empty cohort. Omit `total` otherwise; do not substitute page length or an
approximation. Additional metadata such as resolved sort, offset, or MCP
`truncated`/`hint` may describe the page without changing these core fields.

COUNT can scan many matching rows on Postgres even when the page query uses an
index. Keep it opt-in, measure filtered and unfiltered cohorts with
`EXPLAIN (ANALYZE, BUFFERS)`, and record query, data size, plan, and timings
before claiming it is cheap. CW-20261001-0570 owns the index/COUNT evidence;
measurements are pending and this draft makes no latency claim.

MCP transport byte limits are distinct from row page size. If a byte limit trims
a page, `returned`, `has_more`, and `next_cursor` must reflect the last item
actually sent so continuation cannot skip omitted rows. A byte cap must not
turn a traversable cohort into an unrecoverable fetch-all cutoff.

### Counts, facets, and exports

Use aggregate endpoints for dashboard counts and filter choices; do not fetch
all list pages to count them in the browser. Existing task aggregates are:

- HTTP `GET /api/v1/tasks/facets`, MCP `torque_task_facets`: filtered cohort
  `matching_count` plus buckets for requested dimensions. Dimensions are
  `status`, `priority`, `manual`, `kind`, `executor`, `agent_profile`,
  `launch_profile`, `project_id`, `sprint_id`, `epic_id`, `parent_id`, `tags`.
  `bucket_limit` defaults to 50/max 200; this bounds buckets, not task rows.
- HTTP `GET /api/v1/tasks/rollup?group_by=project_id|epic_id|sprint_id`:
  task counts by scope and raw status. This counts tasks within scopes, not
  the number of projects, epics, or sprints.

Both count the whole matching cohort and reject row `limit`, `offset`,
`cursor`, `sort_by`, and `sort_dir`. Facets apply all active filters, including
the facet's own dimension. Buckets are ordered count descending then value
ascending (typed values, with null treated separately); they report truncation
explicitly. Other facet/count routes in the target matrix remain pending until
implementation supplies their exact names and schemas. `include_total` is the
list's count facility and does not imply a facets endpoint exists.

**Export all means a streaming NDJSON endpoint, never a larger page.** Export
uses the same authorized scope and filters and streams records without a
fetch-all allocation. Its route/schema is pending implementation; this draft
does not advertise a working export URL. There are no hard-coded fetch-all
caps in GUI, HTTP, MCP, service, or store. The 200-row maximum bounds one page,
not the cohort or export. A consumer must not preload every page on mount.

## Endpoint capability matrix

All HTTP paths below are relative to `/api/v1`. `C` means current source at
`a672493`, except implemented cells marked CW-0562/CW-0563; `T` means S1 target.
CW-0563 cells describe the implementation in that task; other target cells
remain pending. `paged` means cursor plus the
shared 50/200 policy and metadata above. `opt-in total` means
`include_total=true`, not an always-computed count. `none` means no public
facet endpoint was identified in that family's handlers, not that the cohort
is empty. Filter cells summarize families; exact parameter schemas remain in
source and MCP `tools/list`.

| Endpoint family | Cursor | Sort | Filters/query | Total | Facets/counts | MCP parity |
|---|---|---|---|---|---|---|
| Tasks `/tasks`, `/tasks/search` | CW-20261001-0626: 50/200 pages; explicit offset incl. 0 without cursor emits offset/next_offset, otherwise cursor mode | `priority`, `status`, `updated_at`, `created_at`; default `priority asc`, `id asc` | Shared scopes/status/priority/tags/text/dates/budgets/presence/internal filters; search requires `q` | Opt-in `include_total` on HTTP/MCP, exact filtered cohort before paging | Task facets + HTTP scope rollup | `torque_task_list` shares filters/query/meta; brief/verbose/typed projection differences retained |
| Epics `/epics` | C (CW-0563): paged by default; T: met | C: named entity allow-list; T: retain | C: project, status, archive, search; T: retain | C (CW-0563): opt-in total; T: met | C: none; T: task rollup supplies progress, entity facets pending | C (CW-0563): `torque_epic_list`, common query/count envelope; T: met |
| Sprints `/sprints` | C (CW-0563): paged by default; T: met | C: named entity allow-list; T: retain | C (CW-0563): project, status, archive, search, over-budget/budget bounds; T: retain | C (CW-0563): opt-in total; T: met | C: none; T: task rollup supplies progress, entity facets pending | C (CW-0563): `torque_sprint_list`, common query/count envelope; T: met |
| Projects `/projects` | C (CW-0563): paged by default; T: met | C: named entity allow-list; T: retain | C (CW-0563): status, archive, search; T: met | C (CW-0563): opt-in total; T: met | C: none; T: task rollup supplies progress, entity facets pending | C (CW-0563): `torque_project_list`, common query/count envelope; T: met |
| Issues `/issues`, `/issues/search` | C (CW-0563): paged list/search by default; T: met | C: task allow-list; T: retain | C: project, status, query, fixed issue kind; T: retain | C (CW-0563): opt-in cohort total; T: met | C: no dedicated issue facets; T: task facets with issue kind | C (CW-0563): `torque_issue_list`, common envelope/count semantics; T: met |
| Comments `/comments`, `/comments/search`, `/tasks/{id}/comments` | C (CW-0563): paged list/search/nested task list; T: met | C: `created_at`; T: retain | C: entity scope(s), author, dates, search; T: retain nested task restrictions | C (CW-0563): opt-in total; T: met | C: none; T: count through include_total, facets pending | C (CW-0563): `torque_comment_list`, `torque_comment_search`, shared 50/200/count policy; T: met |
| Runs `/runs` | Implemented in CW-20261001-0562: cursor default, 50/200 policy; explicit offset incl. 0 without cursor emits offset/next_offset (CW-0626) | `started_at`, `status`, `duration`, `cost`; default `started_at desc`, numeric `id asc` | Shared task/project/sprint/epic scopes, CSV status, inclusive since/until (RFC3339 or Unix millis) | Opt-in `include_total`, cohort before cursor/offset/limit | Facets pending CW-20261001-0564 | `torque_run_list` shares service query and items/meta; MCP byte trims preserve continuation |
| Sessions `/sessions` | C: limit only; T: paged | C: fixed newest-first; T: proposed session fields below | C: state, task, project; T: retain + server query pending | C: none; T: opt-in total | C: none; T: count through include_total, facets pending | C: `torque_session_list` without continuation; T: paged parity |
| Artifacts `/artifacts`, `/tasks/{id}/artifacts` | C: none; T: paged | C: fixed newest-first; T: proposed artifact fields below | C: required task; T: retain + type/run/query pending | C: none; T: opt-in total | C: none; T: count through include_total, facets pending | C: `torque_artifact_list`, brief/verbose byte cap; T: paged parity |
| Collections `/collections` | C: none; T: paged | C: no public sort; T: proposed collection fields below | C: status; T: retain + server query pending | C: none; T: opt-in total | C: none; T: count through include_total, facets pending | C: `torque_collection_list` byte cap; T: paged parity |
| Collection tasks `/collections/{id}/tasks`, `/collections/inbox/tasks` | C: none; T: paged | C: collection order, no public sort; T: position + task fields proposed | C: collection path/inbox scope; T: retain + task filters pending | C: none; T: opt-in total | C: none; T: task facets under equivalent collection scope | C: `torque_collection_tasks_list`, `torque_collection_inbox_list`; T: paged parity |
| Plans `/plans` | C: HTTP none/MCP cursor; T: paged | C: MCP task allow-list; T: same on HTTP | C: HTTP fixed plan kind, MCP scopes/status/search; T: shared query, fixed kind | C: none; T: opt-in total | C: no dedicated plan facets; T: task facets with plan kind | C: `torque_plan_list`, transport mismatch; T: common query contract |
| Plan children `/plans/{id}/children` | C: none; T: paged | C: no public sort; T: task fields proposed | C: plan path, optional phase; T: retain + task filters pending | C: none; T: opt-in total | C: none; T: task facets under equivalent parent/phase scope | C: `torque_plan_list_children` byte cap only; T: paged parity |
| Checkpoints `/tasks/{id}/checkpoints`, `/checkpoints/pending`, `/sessions/{id}/checkpoints` | C: task/pending no cursor, session limit only; T: paged | C: fixed per-family order; T: proposed checkpoint fields below | C: task/session path or pending status; T: preserve these distinct scopes | C: none; T: opt-in total | C: none; T: count through include_total, facets pending | C: task list/pending and session checkpoint tools, no cursor; T: same paging, preserve different record types |
| Templates `/templates` | C: none; T: paged | C: no public sort; T: proposed template fields below | C: kind, archive; T: retain + server query pending | C: none; T: opt-in total | C: none; T: count through include_total, facets pending | C: `torque_template_list` byte cap; T: paged parity |
| Models `/models` | C: none; T: paged | C: no public sort; T: proposed model fields below | C: provider; T: retain + catalog query pending | C: none; T: opt-in total | C: none; T: count through include_total, facets pending | C: `torque_models_list` byte cap, HTTP fetch-all; T: common paging |
| Messages `/messages/inbox`, `/messages/thread/{thread_id}` | C: limit only; T: paged read lists | C: no public sort; T: proposed message fields below | C: recipient/thread, kind/channel/limit; T: retain read scope | C: none; T: opt-in total on read lists | C: none; T: count through include_total, facets pending | C: broker inbox drains delivery, no read-list parity; T: read parity pending, never equate a drain with a read |

Messages require special reconciliation: `torque_broker_inbox` and
`torque_inbox_poll` atomically drain/mark delivered envelopes. They are actions,
not equivalent to the HTTP message read lists. Adding continuation must preserve
delivery guarantees; do not turn a read-list count or page traversal into a
message acknowledgement. Session checkpoints likewise describe runtime recovery
records, separate from task HITL checkpoints.

### Sort allow-lists and defaults

Existing public allow-lists below are checked against service/adapter source.
Runs fields are the implementation owner's confirmed S1 target. **Proposed**
rows are draft choices for families with no public sort at the baseline, not
claims of implementation or additional epic decisions. Implementation owners
must confirm or revise them at reconciliation, including tie-breaks and null
ordering.

| Resource | Allowed `sort_by` | Default | State |
|---|---|---|---|
| Tasks, issues, plans | `priority`, `status`, `updated_at`, `created_at` | `priority asc`, `id asc` tie-break | Current (plans MCP); target HTTP/MCP |
| Projects | `name`, `status`, `updated_at`, `created_at` | `name asc`, `id asc` | Current and target |
| Epics, sprints | `name`, `status`, `updated_at`, `created_at` | `updated_at desc`, `id asc` | Current and target |
| Comments | `created_at` | List `asc`, search `desc`; numeric `id asc` | Current and target |
| Runs | `started_at`, `status`, `duration`, `cost` | `started_at desc`, numeric `id asc` | Implemented CW-20261001-0562; duration is rounded completed elapsed milliseconds, unfinished sentinel -1 |
| Sessions | `created_at`, `state` | `created_at desc`, `id asc` | Proposed target |
| Artifacts | `created_at`, `type` | `created_at desc`, numeric `id asc` | Proposed target |
| Collections | `name`, `status`, `created_at`, `updated_at` | `name asc`, `id asc` | Proposed target |
| Collection tasks | `position`, `priority`, `status`, `updated_at`, `created_at` | `position asc`, task `id asc` | Proposed target; preserve authored collection order |
| Plan children | `priority`, `status`, `updated_at`, `created_at` | `priority asc`, task `id asc` | Proposed target; preserve phase filter |
| Task checkpoints | `created_at`, `status` | Task history `created_at desc`; pending queue `created_at asc`; correlation ID tie-break | Proposed target |
| Session checkpoints | `created_at` | `created_at desc`, unique checkpoint ID | Proposed target; separate recovery record schema |
| Templates | `name`, `kind`, `created_at`, `updated_at` | `name asc`, template ID/version tie-break | Proposed target |
| Models | `name`, `provider_id`, `id` | `name asc`, provider/model pair tie-break | Proposed target; catalog-backed |
| Messages | `created_at` | `created_at asc`, message ID tie-break | Proposed target; read-list semantics only |

Tag catalog lists also fall under the universal 50/200 and opt-in total policy,
even though they are outside the task's required matrix: the current tag cursor
uses fixed `name COLLATE NOCASE asc, slug asc`, query/color filters, and an
always-computed total. Nested checklists and other lists do not gain an exception
merely because they are often small.

## Source evidence and reconciliation

The baseline was read from authored code first, then compared with
[surfaces.md](surfaces.md#http-api) and
[mcp-tools-reference.md](mcp-tools-reference.md#list-envelope-and-pagination).
The adjacent-list sections now reflect CW-0563; other pre-S1 pagination prose
is not the target contract. CW-0563 was verified with HTTP/MCP parity tests over
205-row matching cohorts: default/maximum bounds, all allowed sort directions,
continuation, filtered totals, omitted/empty counts, and default envelopes.
GUI API tests verify one request per call even when has_more is true.
Full GUI paging/picker UX remains with CW-0572/CW-0577; these lists are not a
complete GUI catalog yet and must not be deployed ahead of consumer migration.

- [Service task query](../internal/service/task_query.go),
  [adjacent query](../internal/service/adjacent_query.go), and
  [cursor implementation](../internal/service/pagination/cursor.go) define
  current sorting, validation, default limits, and cursor binding.
- [HTTP routes](../internal/httpserver/server.go), family handlers in
  `internal/httpserver/`, [HTTP adjacent mode selection](../internal/httpserver/adjacent_query.go),
  [MCP family tools](../internal/mcpadapter/), and
  [MCP envelopes](../internal/mcpadapter/response.go) establish the current
  transport differences. MCP task `meta.total` is already opt-in; HTTP tasks
  always count at the baseline.

At the end of S1, check every matrix cell and sort row against the merged tree,
record the commit and verification evidence here, replace proposed rows with
implemented allow-lists/defaults, and update the linked surface/reference prose.
Check empty/final pages, ties/nulls, cursor/sort/offset rejection, nested scopes,
COUNT semantics/cost, byte-trim continuation, legacy-shape removal, and export
availability. Any target not landed remains explicitly pending. This draft does
not fulfill the task's final implemented-behavior reconciliation or merge
acceptance by itself.

### Runs implementation reconciliation (CW-20261001-0562)

HTTP `/runs` and MCP `torque_run_list` now use the same service query, bounded
by `pagination.DefaultLimit`/`MaxLimit`. Omitting filters queries across tasks;
`task_id` is optional on both transports. HTTP record projection remains the
decorated RunRecord, and MCP retains brief/verbose projections. The legacy
HTTP `{runs:[...]}` shape and MCP fetch-all-then-cap path are removed.

SQLite normalizes legacy/canonical UTC timestamp spellings without losing
nanosecond precision for start-time ordering and inclusive bounds. Duration
uses the SQL-computed rounded elapsed milliseconds, with unfinished rows at
-1. A total and its page share one read snapshot; no count runs otherwise.
MCP byte trimming emits a cursor for the last row actually sent. An individual
verbose record exceeding the byte budget returns `arg_invalid` with guidance
to request brief rows or retrieve that run separately.

Runs browsing lazily appends pages and sends its status filter to the server.
Task Logs expose older runs via cursor continuation. Ops/preview widgets
consume the first recent page; usage charts explicitly describe that sample.
SSE patches loaded runs; Refresh restarts Runs browsing for new rows.
No list client preloads the whole run cohort. Index and PostgreSQL COUNT
measurements remain CW-20261001-0570; facets remain CW-20261001-0564.

Verification: `TestRunQueryCursorAndTotals`, `TestRunQuerySortsAndCohort`,
and `TestRunListHTTPMCPParity` cover inserts between pages, tied sort keys,
legacy timestamp formats, all four sorts in both directions, totals, limits,
scopes and validation on temporary databases.

### Task list/search and shared metadata reconciliation (CW-20261001-0626)

HTTP `/tasks` and `/tasks/search` return `{items,meta}` only. Summary projection
(`fields=summary`) uses the same envelope. Search requires `q` and applies the
same filters, paging, sort and validation as task lists; `search` is not accepted
alongside `q`. Both use a limit+1 probe for `has_more` and only count when
`include_total=true`; exactly-full final and empty pages have no continuation.
`pagination.NewPageMeta` owns metadata for HTTP task pages and HTTP/MCP runs
and task-list MCP pages, including mode-specific offset fields.

The former flat task body (`tasks`, top-level count/paging/sort/continuation)
and unpaged search body are removed. The Torque GUI keeps its existing internal
`{tasks,total}` adapter by decoding `items/meta` and explicitly requesting totals.
Its existing traversal behavior is retained for CW-0571/CW-0572 to replace.
Tachyon work-ops CW-0630 and Tangent CW-0631 own cross-project adapters; no live
deploy before those consumers land.
