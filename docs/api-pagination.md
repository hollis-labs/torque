# API list and pagination contract

**Status: S1 target specification, pending implementation reconciliation.**
CW-20261001-0561 defines the HTTP and MCP list contract for
EP-20261001-0013, *Server-driven lists — query, sort, cursor-page, never
fetch-all*. The normative sections below describe the intended completed
contract. The matrices explicitly separate it from the source-checked baseline
at `a672493` (2026-10-01); they do not claim the target is already deployed.
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
`a672493`; `T` means S1 target, still pending. `paged` means cursor plus the
shared 50/200 policy and metadata above. `opt-in total` means
`include_total=true`, not an always-computed count. `none` means no public
facet endpoint was identified in that family's handlers, not that the cohort
is empty. Filter cells summarize families; exact parameter schemas remain in
source and MCP `tools/list`.

| Endpoint family | Cursor | Sort | Filters/query | Total | Facets/counts | MCP parity |
|---|---|---|---|---|---|---|
| Tasks `/tasks` | C: yes, offset also supported; T: paged by default | C: task allow-list; T: retain | C: scopes, status/priority, tags, text, dates, budgets, presence, internal visibility; T: retain shared validation | C: HTTP always; MCP opt-in; T: opt-in total on both | C: task facets + HTTP scope rollup; T: retain | C: `torque_task_list`, filters shared but envelope/count differ; T: common payload/count policy |
| Epics `/epics` | C: HTTP advanced opt-in, MCP yes, 100/500; T: paged | C: named entity allow-list; T: retain | C: project, status, archive, search; T: retain | C: none in cursor envelope; T: opt-in total | C: none; T: task rollup supplies progress, entity facets pending | C: `torque_epic_list`, cursor filters shared; T: remove HTTP legacy mode |
| Sprints `/sprints` | C: HTTP advanced opt-in, MCP yes, 100/500; T: paged | C: named entity allow-list; T: retain | C: project, status, archive, over-budget/budget bounds; T: retain | C: none in cursor envelope; T: opt-in total | C: none; T: task rollup supplies progress, entity facets pending | C: `torque_sprint_list`, cursor filters shared; T: remove HTTP legacy mode |
| Projects `/projects` | C: HTTP advanced opt-in, MCP yes, 100/500; T: paged | C: named entity allow-list; T: retain | C: status, archive; T: retain, server text query pending | C: none in cursor envelope; T: opt-in total | C: none; T: task rollup supplies progress, entity facets pending | C: `torque_project_list`, cursor filters shared; T: remove HTTP legacy mode |
| Issues `/issues`, `/issues/search` | C: HTTP advanced opt-in, MCP yes, 50/200; T: paged | C: task allow-list; T: retain | C: project, status, query, fixed issue kind; T: retain | C: legacy HTTP total can be page length; cursor none; T: opt-in cohort total | C: no dedicated issue facets; T: task facets with issue kind | C: `torque_issue_list`; T: common envelope/count semantics |
| Comments `/comments`, `/comments/search`, `/tasks/{id}/comments` | C: HTTP list advanced opt-in/MCP cursor; search 25/100, list 50/200; T: paged for both | C: `created_at`; T: retain | C: entity scope(s), author, dates, search; T: retain nested task restrictions | C: none; T: opt-in total | C: none; T: count through include_total, facets pending | C: `torque_comment_list`, `torque_comment_search`; T: common policy for search too |
| Runs `/runs` | C: none, HTTP 200/1000; T: paged | C: fixed order; T: runs allow-list below | C: HTTP task/project/status/since, MCP task only; T: shared filters + server query pending | C: none; T: opt-in total | C: no list facets; T: aggregate/facet schema pending | C: `torque_run_list` task-only, byte capped; T: HTTP/MCP query parity |
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
| Runs | `started_at`, `status`, `duration`, `cost` | `started_at desc`, numeric `id asc` | Confirmed S1 target; duration is completed elapsed milliseconds, unfinished sentinel -1 |
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
Those pages describe pre-S1 behavior, including legacy opt-in shapes; their
older pagination prose is not the target contract.

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
