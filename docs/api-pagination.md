# API list and pagination contract

**Status: S1 target specification, pending implementation reconciliation.**
CW-20261001-0561 defines the HTTP and MCP list contract for
EP-20261001-0013, *Server-driven lists — query, sort, cursor-page, never
fetch-all*. The normative sections below describe the intended completed
contract. The matrices explicitly separate it from the source-checked baseline
at `a672493` (2026-10-01), with S1 rows reconciled for CW-0562, CW-0563, CW-0565,
CW-0626 and the merged facets/index tasks. They do not claim the complete target is already deployed.
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

### Static task eligibility

`eligible=true` restricts `/api/v1/tasks` (including search),
`/api/v1/tasks/facets`, `/api/v1/tasks/rollup`, `torque_task_list`, and
`torque_task_facets` to the data-only eligibility checks in `Picker.Pick`.
Omitting it or passing `false` adds no restriction; it does not select the
inverse cohort. Boolean values accept `true/false`, `1/0`, or `yes/no`
(case-insensitive); malformed or empty explicit values reject. MCP also
accepts native booleans.

All other filters intersect with this predicate. Counts and facets use the
same cohort before paging. Keep `eligible` and every other filter unchanged
when following a cursor. Internal tasks remain hidden by the normal visibility
rule unless `include_internal=true` or `kind=internal` is supplied.

The shared SQL predicate is:

```sql
tasks.status = 'todo'
AND tasks.manual = 0
AND tasks.kind NOT IN ('parent', 'plan', 'issue')
AND (tasks.kind NOT IN ('agent', 'internal')
     OR tasks.agent_profile <> '' OR tasks.launch_profile <> '')
AND NOT EXISTS (
    SELECT 1 FROM task_dependencies td
    LEFT JOIN tasks dep ON dep.id = td.depends_on_task_id
    WHERE td.task_id = tasks.id
      AND (dep.id IS NULL OR dep.status <> 'done')
)
```

Only `done` satisfies a dependency; `archived` does not. Dependency deletion
prunes its edge through the existing foreign key. Profile selectors are tested
for a nonempty stored string, without trimming or verifying that the named
profile exists. There is no executor-specific condition; other allowed kinds
can have empty profile selectors.

This is **static eligibility**, not a dispatch guarantee. It deliberately does
not evaluate the configured project allowlist, projects occupied by doing tasks,
projects allocated earlier in the current pick pass, worker capacity/pick limit,
scheduler enablement, the global cost ceiling, or runtime executor/profile
availability and launch readiness. Retry, checkpoint, and lifecycle handling
remain the scheduler's responsibility; their persisted status changes affect
this filter only through the `status='todo'` condition. Picker behavior is
unchanged.

For example, `/api/v1/tasks?eligible=true&project_id=PRJ-1&include_total=true`
and `/api/v1/tasks/facets?eligible=true&project_id=PRJ-1` count the same cohort.
The rollup additionally excludes tasks without the requested grouping scope,
as it does for every other task filter.

### COUNT cost evidence

Keep `include_total` opt-in on every list, including small tables. Exact
COUNT visits the entire matching cohort; a bounded page can stop after its
continuation probe. Migration 035 supplies sort/cohort indexes but does not
make COUNT constant-cost.

CW-20261001-0570 measured PostgreSQL 17.11 using synthetic data at the current
4,330-task cardinality (read-only `/tasks/facets`, 2026-10-01) and 43,300 tasks.
The run scenario uses the same sizes; these are not observed live run counts.
The table gives median server-side execution milliseconds from seven warm
`EXPLAIN (ANALYZE, BUFFERS)` samples after one warm-up and VACUUM ANALYZE,
with migration 035 applied. Transport, cold caches, write churn and concurrent
application load are excluded. Exact SQL, plans, ranges, fixture sizes and
reproduction steps are in the [index/COUNT audit](list-index-plans.md).

| Query | 4,330 rows | 43,300 rows |
|---|---:|---:|
| Tasks COUNT, all | 0.446 | 4.706 |
| Tasks COUNT, status = todo | 0.179 | 1.126 |
| Tasks COUNT, project cohort | 0.093 | 0.165 |
| Tasks page, priority ASC + id ASC, LIMIT 51 | 0.101 | 0.153 |
| Runs COUNT, all | 0.462 | 3.932 |
| Runs COUNT, status = running | 0.161 | 1.013 |
| Runs COUNT, project join | 0.278 | 1.816 |
| Runs page, started_at DESC + id ASC, LIMIT 51 | 0.090 | 0.225 |

Status cohorts match 1,083 / 10,830 rows, and project cohorts match 101 / 1,010.
The unfiltered totals grow with table size while page work stays bounded.
Small synthetic tables were cheap in this measurement (projects 43/430,
epics 200/2,000, sprints 400/4,000), but that does not establish an always-on
exception or a production latency bound. Keep one opt-in policy; dashboards
can request explicit counts/facets when they need whole-cohort figures.

MCP transport byte limits are distinct from row page size. If a byte limit trims
a page, `returned`, `has_more`, and `next_cursor` must reflect the last item
actually sent so continuation cannot skip omitted rows. A byte cap must not
turn a traversable cohort into an unrecoverable fetch-all cutoff.

### Counts, facets, and exports

Use aggregate endpoints for dashboard counts and filter choices; do not fetch
all list pages to count them in the browser. Available aggregates are:

- HTTP `GET /api/v1/tasks/facets`, MCP `torque_task_facets`: filtered cohort
  `matching_count` plus buckets for requested dimensions. Dimensions are
  `status`, `priority`, `manual`, `kind`, `executor`, `agent_profile`,
  `launch_profile`, `project_id`, `sprint_id`, `epic_id`, `parent_id`, `tags`.
  `bucket_limit` defaults to 50/max 200; this bounds buckets, not task rows.
- HTTP `GET /api/v1/tasks/rollup?group_by=project_id|epic_id|sprint_id`:
  task counts by scope and raw status. This counts tasks within scopes, not
  the number of projects, epics, or sprints.

- HTTP `GET /api/v1/projects/facets`, `/epics/facets`, `/sprints/facets`;
  MCP `torque_project_facets`, `torque_epic_facets`, `torque_sprint_facets`:
  entity `matching_count` and dimension buckets using the corresponding
  list filters (including parent `search`). Dimensions: projects `status`;
  epics `status`, `project_id`, `priority`; sprints `status`, `project_id`,
  `approval_mode`.
  `dimensions` is CSV, defaults to all supported dimensions, and deduplicates.
  `task_rollups` contains `{scopes:[{scope_id,total,counts}],total_distinct,
  returned,truncated}` for the matching parents. `counts` is keyed by raw task
  status, excludes internal tasks, and includes parents with zero tasks.
  Parent groups sort by task total descending then parent ID ascending;
  `bucket_limit` bounds parent groups independently of each dimension's buckets.
  All statuses for a returned parent are complete.
- HTTP `GET /api/v1/runs/facets`, MCP `torque_run_facets`: the exact `/runs`
  cohort filters (`task_id`, `project_id`, `sprint_id`, `epic_id`, CSV `status`,
  inclusive `since`/`until` as RFC3339 or Unix milliseconds). Dimensions are
  `status`, `executor`, `profile`. The profile bucket is the **current task
  profile, not the profile at run time**: nonempty `launch_profile`, falling
  back to `agent_profile`. Runs do not store a historical profile snapshot.
  `totals` is `{cost,prompt_tokens,completion_tokens}`: cost from canonical
  `cost_ledger` rows joined by run ID to the matching runs, tokens from run
  records. The date window filters run start times, never ledger timestamps.
  Runs without ledger rows contribute zero cost. Ledger multiplicity never
  multiplies token or run counts. Empty cohorts return zero totals.

- HTTP `GET /api/v1/runs/timeseries`, MCP `torque_run_timeseries`:
  gap-free hourly/daily aggregates over the same run cohort. `since` and
  `until` are required inclusive run-start bounds (RFC3339 or Unix millis).
  `bucket=hour|day` defaults to day. `tz_offset_minutes` is a fixed offset
  from UTC, default 0, integer -840 through 840; it does not apply DST rules.
  Every intersecting bucket is present, including empty and partial edge
  buckets. An inclusive `until` exactly on a bucket edge includes that bucket.
  At most **200 buckets** may intersect the requested window. Larger windows
  reject on `until`; output is never silently truncated. To request exactly
  one local day, end just before the next day's edge.

  Response:
  ```json
  {
    "bucket": "day",
    "tz_offset_minutes": 0,
    "since": "2026-10-01T00:00:00Z",
    "until": "2026-10-01T23:59:59.999999999Z",
    "buckets": [{
      "start": "2026-10-01T00:00:00Z",
      "count": 0,
      "prompt_tokens": 0,
      "completion_tokens": 0,
      "cost": 0,
      "status_counts": {}
    }],
    "totals": {"count": 0, "prompt_tokens": 0, "completion_tokens": 0, "cost": 0}
  }
  ```
  `start` is the UTC instant of the offset-local bucket edge; buckets are
  chronological. `status_counts` preserves the exact stored statuses.
  Summed counts match `/runs?include_total=true`; token/cost sums match
  `/runs/facets` for the same filters. Cost is the canonical ledger total per
  matching run, regardless of ledger date, and runs without ledger rows
  contribute zero. MCP rejects on `until` if the complete series exceeds its
  byte budget; callers shorten the window or narrow filters. Row paging/sort
  and facet `bucket_limit` parameters are unsupported. There is no run-row cap.

Entity and run facets return `{matching_count,bucket_limit,dimensions,
facets:[{dimension,buckets:[{value,count}],total_distinct,returned,truncated}]}`
plus the parent `task_rollups` or run `totals` described above. `bucket_limit`
defaults to 50, clamps above 200, and rejects negatives; zero uses the default.
MCP may trim buckets or whole parent groups to its response byte budget,
updating `returned`/`truncated` while preserving exact counts and totals.
GUI stat-card consumption is tracked separately in S3; these routes require
no list fetch to obtain cohort counts or rollups.

All aggregates count the whole matching cohort and reject row `limit`, `offset`,
`cursor`, `sort_by`, and `sort_dir`. Facets apply all active filters, including
the facet's own dimension. Buckets are ordered count descending then value
ascending (typed values, with null treated separately); they report truncation
explicitly. Remaining facet/count routes in the target matrix remain pending
until implementation supplies their exact names and schemas. `include_total` is the
list's count facility and does not imply a facets endpoint exists.

**Export all means a streaming NDJSON endpoint, never a larger page.** Export
uses the same authorized scope and filters and streams records without a
fetch-all allocation. Its route/schema is pending implementation; this draft
does not advertise a working export URL. There are no hard-coded fetch-all
caps in GUI, HTTP, MCP, service, or store. The 200-row maximum bounds one page,
not the cohort or export. A consumer must not preload every page on mount.

## Endpoint capability matrix

All HTTP paths below are relative to `/api/v1`. `C` means current source at
`a672493`, except implemented cells marked with S1 task IDs, including CW-0565; `T` means S1 target.
Marked cells describe their task implementations; other target cells
remain pending. `paged` means cursor plus the
shared 50/200 policy and metadata above. `opt-in total` means
`include_total=true`, not an always-computed count. `none` means no public
facet endpoint was identified in that family's handlers, not that the cohort
is empty. Filter cells summarize families; exact parameter schemas remain in
source and MCP `tools/list`.

| Endpoint family | Cursor | Sort | Filters/query | Total | Facets/counts | MCP parity |
|---|---|---|---|---|---|---|
| Tasks `/tasks`, `/tasks/search` | CW-20261001-0626: 50/200 pages; explicit offset incl. 0 without cursor emits offset/next_offset, otherwise cursor mode | `priority`, `status`, `updated_at`, `created_at`; default `priority asc`, `id asc` | Shared scopes/status/priority/tags/text/dates/budgets/presence/internal filters; search requires `q` | Opt-in `include_total` on HTTP/MCP, exact filtered cohort before paging | Task facets + HTTP scope rollup | `torque_task_list` shares filters/query/meta; brief/verbose/typed projection differences retained |
| Epics `/epics` | C (CW-0563): paged by default; T: met | C: named entity allow-list; T: retain | C: project, status, archive, search; T: retain | C (CW-0563): opt-in total; T: met | C: `/epics/facets`, `torque_epic_facets` + bounded parent task rollups; T: retain | C (CW-0563): `torque_epic_list`, common query/count envelope; T: met |
| Sprints `/sprints` | C (CW-0563): paged by default; T: met | C: named entity allow-list; T: retain | C (CW-0563): project, status, archive, search, over-budget/budget bounds; T: retain | C (CW-0563): opt-in total; T: met | C: `/sprints/facets`, `torque_sprint_facets` + bounded parent task rollups; T: retain | C (CW-0563): `torque_sprint_list`, common query/count envelope; T: met |
| Projects `/projects` | C (CW-0563): paged by default; T: met | C: named entity allow-list; T: retain | C (CW-0563): status, archive, search; T: met | C (CW-0563): opt-in total; T: met | C: `/projects/facets`, `torque_project_facets` + bounded parent task rollups; T: retain | C (CW-0563): `torque_project_list`, common query/count envelope; T: met |
| Issues `/issues`, `/issues/search` | C (CW-0563): paged list/search by default; T: met | C: task allow-list; T: retain | C: project, status, query, fixed issue kind; T: retain | C (CW-0563): opt-in cohort total; T: met | C: no dedicated issue facets; T: task facets with issue kind | C (CW-0563): `torque_issue_list`, common envelope/count semantics; T: met |
| Comments `/comments`, `/comments/search`, `/tasks/{id}/comments` | C (CW-0563): paged list/search/nested task list; T: met | C: `created_at`; T: retain | C: entity scope(s), author, dates, search; T: retain nested task restrictions | C (CW-0563): opt-in total; T: met | C: none; T: count through include_total, facets pending | C (CW-0563): `torque_comment_list`, `torque_comment_search`, shared 50/200/count policy; T: met |
| Runs `/runs` | Implemented in CW-20261001-0562: cursor default, 50/200 policy; explicit offset incl. 0 without cursor emits offset/next_offset (CW-0626) | `started_at`, `status`, `duration`, `cost`; default `started_at desc`, numeric `id asc` | Shared task/project/sprint/epic scopes, CSV status, inclusive since/until (RFC3339 or Unix millis) | Opt-in `include_total`, cohort before cursor/offset/limit | `/runs/facets`, `torque_run_facets`: status/executor/current task profile + ledger cost/run tokens | `torque_run_list` shares service query and items/meta; MCP byte trims preserve continuation |
| Sessions `/sessions` | CW-0565: cursor default, optional offset, shared 50/200 | `created_at`, `state`; newest first, session ID asc | state, task/project scope, search on ID/task/workdir | Opt-in filtered cohort total | List count only; facets pending | `torque_session_list`, same query/meta; existing Session snapshot preserved |
| Artifacts `/artifacts`, `/tasks/{id}/artifacts` | CW-0565: cursor default, optional offset, shared 50/200 | `created_at`, `type`; newest first, numeric ID asc | Required task (path wins), type, run_id, content/URL/path search | Opt-in filtered cohort total | List count only; facets pending | `torque_artifact_list`, same query/meta; brief/verbose and byte continuation retained |
| Collections `/collections` | CW-0565: cursor default, optional offset, shared 50/200 | name/status/creation/update; default name asc, ID asc | active/archived/all (default active), ID/name/description search | Opt-in filtered cohort total | List count only; facets pending | `torque_collection_list`, same query/meta |
| Collection tasks `/collections/{id}/tasks`, `/collections/inbox/tasks` | CW-0565: cursor default, optional offset, shared 50/200 | position + task allow-list; default position asc, task ID asc | Path/inbox scope; task status/priority/project/sprint/epic, tags, text search | Opt-in scoped filtered cohort total | Task facets under equivalent scope remain separate | `torque_collection_tasks_list`, `torque_collection_inbox_list`; task projection retained |
| Plans `/plans` | CW-0565: cursor default, optional offset, shared 50/200 | Task allow-list; priority asc, ID asc | Fixed plan kind; status/priority/project/sprint/epic, tags, search | Opt-in filtered plan cohort total | Task facets with plan kind | `torque_plan_list`, same query/meta; task projection retained |
| Plan children `/plans/{id}/children` | CW-0565: cursor default, optional offset, shared 50/200 | Task allow-list; priority asc, task ID asc | Required plan path, phase_id; status/priority/project/sprint/epic, tags, search | Opt-in scoped filtered cohort total | Task facets under equivalent parent/phase remain separate | `torque_plan_list_children`, same query/meta; task projection retained |
| Checkpoints `/tasks/{id}/checkpoints`, `/checkpoints/pending`, `/sessions/{id}/checkpoints` | CW-0565: cursor default, optional offset, shared 50/200 | Task: creation/status; history desc, pending asc, correlation ID asc. Recovery: creation desc, checkpoint ID asc | Preserve task/session/pending scopes; task history status; recovery ID/note or HITL correlation/type/payload search | Opt-in scoped filtered cohort total | List count only; facets pending | `torque_task_checkpoint_list`, `torque_task_checkpoints_pending`, `torque_session_checkpoint_list`; HITL and recovery projections stay distinct |
| Templates `/templates` | CW-0565: cursor default, optional offset, shared 50/200 | name/kind/creation/update; name asc, ID/version identity asc | kind, include_archived, ID/name/description search | Opt-in filtered version-row cohort total | List count only; facets pending | `torque_template_list`, same query/meta; versions remain separate rows |
| Models `/models` | CW-0565: cursor default, optional offset, shared 50/200 | name/provider_id/id; name asc, provider/model identity asc | provider, case-insensitive name/model/provider search; catalog snapshot | Opt-in filtered catalog-row total (cold cache 0) | List count only; facets pending | `torque_models_list`, same query/meta; HTTP and brief/verbose MCP projections retained |
| Messages `/messages/inbox`, `/messages/thread/{thread_id}` | CW-0565: inbox drain list: each call delivers its page, no cursor/offset. Thread: cursor default; offset available on one store, positive federated offset rejects with use-cursor guidance | `created_at`; thread asc/desc, message ID asc; inbox asc only | Recipient/thread scopes, kind/channel/thread filters retained | Inbox: undelivered cohort before this drain. Thread: exact with one store; multiple stores omit total and emit total_unavailable=federated | Non-mutating count/peek; no message facets | No pure-read MCP thread list exists; broker inbox/inbox_poll remain actions unchanged. CW-20260912-0114 remains separate |

Messages have two distinct traversal models. `GET /messages/inbox` remains
an operator-initiated **drain list: each call delivers its page**. It fetches
exactly `limit` rows and marks only those rows delivered, never `limit+1`.
A separate non-mutating EXISTS after delivery sets `has_more`; `next_cursor`
is always null. Calling again retrieves the next undelivered batch. The opt-in
`total` counts the filtered undelivered cohort immediately before this call's
page is delivered, within the same transaction; it decreases on later calls.
Cursor, offset, and descending delivery order are rejected for inbox batches.
`torque_broker_inbox` and `torque_inbox_poll` remain their existing delivery
actions; this change does not turn them into pure reads or alter ack behavior.
The inbox-mutation question remains [CW-20260912-0114](https://torque.nanite.cloud/tasks/CW-20260912-0114).

`GET /messages/thread/{thread_id}` is a pure read. It preserves federation:
each store contributes at most `limit+1` rows after the cursor in the shared
`(created_at, message ID)` order, merged and deduplicated before emitting one
page. Continuation uses the last emitted row, including when peers hold the
same message ID. Whole-cohort `total` is exact for one store; when multiple
stores participate, `include_total=true` is safe but the response omits
`total` and adds `meta.total_unavailable="federated"`. It never sums overlapping
peer counts. Positive offset with multiple stores returns a 400 error naming
`offset` and advising use of cursor; offset zero and cursor mode remain valid.
Single-store threads support offset/next_offset. Peer HTTP thread reads use the
same bounded envelope; peers without paging support fail explicitly rather
than triggering an unbounded history fetch. Federation authorizes thread
participation through an EXISTS query before any paged data or count is sent.

Session checkpoints describe runtime recovery records, separate from task
HITL checkpoints; their schemas are preserved inside the common envelope.

### Sort allow-lists and defaults

Existing public allow-lists below are checked against service/adapter source.
The rows marked implemented include the owner-confirmed allow-lists,
defaults, unique tie-breaks and null ordering. These reconcile the original
proposed choices for families that had no public sort at the baseline.

| Resource | Allowed `sort_by` | Default | State |
|---|---|---|---|
| Tasks, issues, plans | `priority`, `status`, `updated_at`, `created_at` | `priority asc`, `id asc` tie-break | Current (plans MCP); target HTTP/MCP |
| Projects | `name`, `status`, `updated_at`, `created_at` | `name asc`, `id asc` | Current and target |
| Epics, sprints | `name`, `status`, `updated_at`, `created_at` | `updated_at desc`, `id asc` | Current and target |
| Comments | `created_at` | List `asc`, search `desc`; numeric `id asc` | Current and target |
| Runs | `started_at`, `status`, `duration`, `cost` | `started_at desc`, numeric `id asc` | Implemented CW-20261001-0562; duration is rounded completed elapsed milliseconds, unfinished sentinel -1 |
| Sessions | `created_at`, `state` | `created_at desc`, `id asc` | Implemented CW-0565 |
| Artifacts | `created_at`, `type` | `created_at desc`, numeric `id asc` | Implemented CW-0565; creation order revised from the previous oldest-first source |
| Collections | `name`, `status`, `created_at`, `updated_at` | `name asc`, `id asc` | Implemented CW-0565; status derives active/archived from archived_at |
| Collection tasks | `position`, `priority`, `status`, `updated_at`, `created_at` | `position asc`, task `id asc` | Implemented CW-0565; null position maps to max int64 (last asc, first desc); task ID breaks ties. Inbox has null positions and therefore ID order by default |
| Plan children | `priority`, `status`, `updated_at`, `created_at` | `priority asc`, task `id asc` | Implemented CW-0565; phase filtered in SQL before paging/count |
| Task checkpoints | `created_at`, `status` | Task history `created_at desc`; pending queue `created_at asc`; correlation ID tie-break | Implemented CW-0565; created_at is the emitted_at alias |
| Session checkpoints | `created_at` | `created_at desc`, unique checkpoint ID | Implemented CW-0565; separate recovery record schema |
| Templates | `name`, `kind`, `created_at`, `updated_at` | `name asc`, template ID/version tie-break | Implemented CW-0565; lexical ID + colon + decimal version identity; versions are distinct rows |
| Models | `name`, `provider_id`, `id` | `name asc`, provider/model pair tie-break | Implemented CW-0565; catalog-backed, identity encoded as provider/model JSON pair |
| Messages | `created_at` | `created_at asc`, message ID tie-break | Implemented CW-0565; inbox asc only, thread asc/desc |

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

### Remaining-family reconciliation (CW-20261001-0565)

The rows marked CW-0565 describe authored HTTP, service/store, MCP and GUI
implementations. The old named arrays and raw list shapes are removed. GUI
callers consume one page and retain the current record projections; full
paging/picker UX remains later work, and no live deployment is included.
SQL list/count filters share the same predicates. Cursor keys are non-null:
required strings/timestamps keep their existing values; the only nullable sort
key, collection position, uses max int64 as the sentinel described above.
All sorts use an ascending unique identity tie-break in both primary directions.

Verification includes HTTP/MCP parity over 205 matching rows across eleven
SQL resource families, every allowed sort in both directions, limit clamp,
filtered whole-cohort totals, and cursor traversal through tied sort keys.
Catalog tests cover duplicate model IDs across providers. Message tests cover
exact delivery counts, successive batches, pure-read fractional-time cursors,
federated deduplication at page boundaries, and count availability.
