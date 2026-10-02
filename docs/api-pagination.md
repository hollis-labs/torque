# API list and pagination contract

**Status: implementation reconciliation in progress (CW-20261001-0627).**
The reference below describes merged S1 HTTP/MCP behavior for
EP-20261001-0013. Stable sections were read against `e57ba81` on 2026-10-01.
The models numeric sorts and remaining GUI consumers are merged at `46089e4`.
Final verification awaits CW-0677 tag/checklist paging; its merged verification
commit will be recorded here before this reconciliation PR opens.
Source state does not establish what binary is deployed.

## One list contract

The endpoint families in the matrix are server-filtered, server-sorted, and
paged by default, including their nested lists. Message inbox draining and the
HTTP tag/checklist exceptions below have distinct response or traversal rules. HTTP returns `{items, meta}`; MCP puts that
same payload inside its existing `{ok:true, data:{items, meta}}` envelope.
Projection differences (brief, verbose, summary, typed records) do not change
paging or cohort semantics.

The matrix families no longer return legacy fetch-all shapes. Responses
such as `{sprints:[...]}`, `{runs:[...]}`, `{tasks:[...]}`, raw arrays, and the
legacy/advanced opt-in mode switch are removed in a **clean break, with no
backwards compatibility**. GUI, Tachyon, other clients, and documentation must
move together. Omitting query parameters returns the first bounded page; it
never opts into fetching everything.

### Request parameters

| Parameter | Contract |
| --- | --- |
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

With `include_total=true`, local list queries add `meta.total`, an exact
non-negative count of the
matching cohort **before cursor, offset, and limit**, using the same filters,
visibility rules, and required scope as the items query. Return `total:0` for an
empty cohort. Omit `total` otherwise; do not substitute page length or an
approximation. Federated message threads instead omit unavailable totals and
report `meta.total_unavailable="federated"`, as described below. Additional metadata such as resolved sort, offset, or MCP
`truncated`/`hint` may describe the page without changing these core fields.

### MCP byte-cap continuation

MCP list page limits share `pagination.DefaultLimit=50` and `MaxLimit=200`.
The approximately 100 KB serialized response budget also includes the
continuation hint. If it trims rows, `meta.returned` matches the emitted
payload, `has_more=true`, and `next_cursor` is recomputed from the last emitted
row. The concrete next-call hint retains filters, scope, sort, and projection;
it distinguishes a Torque server page from a host preview/cache page. A
single first row that cannot fit returns `arg_invalid` with brief/smaller-query
guidance rather than an empty page with no advancing cursor. Counts remain
cohort counts after trimming. See the [continuation example](mcp-tools-reference.md#list-envelope-and-pagination).

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
| --- | ---: | ---: |
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
  Optional `rollup_ids` CSV addresses the parents on the displayed row page:
  trim/deduplicate nonempty IDs, at most 200 distinct IDs (more rejects on
  `rollup_ids`). It selects **only rollups**, not the facet cohort. Cohort IDs
  with zero tasks receive `{total:0,counts:{}}`; non-cohort IDs are omitted.
  Explicit blank CSV selects no rollups. In explicit-ID mode, `bucket_limit`
  does not cap the requested groups; rollup `total_distinct` counts requested
  IDs inside the cohort and `truncated` is false before MCP byte-budget trimming.
  Without the parameter, the existing top-`bucket_limit` selection is retained.
  `task_totals:{total,counts}` always sums every non-internal task under the
  full parent cohort, independently of `rollup_ids` and `bucket_limit`.
  Projects additionally return `children:{sprints,epics}` within each selected
  rollup and `child_totals:{sprints,epics}` over the full project cohort.
  `include_archived=false` excludes archived child sprints/epics as on their
  lists; `true` includes them. Other parent filters select projects, without
  applying project search/status to child records. Zero child counts are explicit.
- HTTP `GET /api/v1/runs/facets`, MCP `torque_run_facets`: the exact `/runs`
  cohort filters (`task_id`, `project_id`, `sprint_id`, `epic_id`, CSV `status`, `executor`, `profile`,
  inclusive `since`/`until` as RFC3339 or Unix milliseconds). Dimensions are
  `status`, `executor`, `profile`. The profile bucket is the **current task
  profile, not the profile at run time**: nonempty `launch_profile`, falling
  back to `agent_profile`. The `profile` filter uses that exact same expression;
  `executor` matches the raw run executor. CSV values are trimmed, deduplicated
  and empty entries ignored, as for status; unknown values simply match no rows.
  Values within each filter use OR; separate filters combine with AND.
  Runs do not store a historical profile snapshot.
  `totals` is `{cost,prompt_tokens,completion_tokens}`: cost from canonical
  `cost_ledger` rows joined by run ID to the matching runs, tokens from run
  records. The date window filters run start times, never ledger timestamps.
  Runs without ledger rows contribute zero cost. Ledger multiplicity never
  multiplies token or run counts. Empty cohorts return zero totals.

- HTTP `GET /api/v1/runs/timeseries`, MCP `torque_run_timeseries`:
  gap-free hourly/daily aggregates over the same run cohort, including CSV
  status/executor/current task profile. `since` and
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
These routes require no list fetch to obtain cohort counts or rollups. The
Dashboard and Board request aggregate cohorts separately from their loaded rows.

All aggregates count the whole matching cohort and reject row `limit`, `offset`,
`cursor`, `sort_by`, and `sort_dir`. Facets apply all active filters, including
the facet's own dimension. Buckets are ordered count descending then value
ascending (typed values, with null treated separately); they report truncation
explicitly. Families marked as having no dedicated facets endpoint expose
only their implemented list total or related task facets. `include_total` is the
list's count facility and does not imply a facets endpoint exists.

**Export is pending.** No streaming NDJSON export route is registered in the
merged HTTP router. A larger `limit` does not export a cohort: the 200-row
maximum still bounds one public page. Export's proposed transport and scope
contract are not an available API. Internal scheduler/recovery methods may
still enumerate whole datasets; they are separate from these public page APIs.

## Endpoint capability matrix

All HTTP paths below are relative to `/api/v1`. `paged` means the shared
50/default, 200/max page policy and metadata above. `opt-in total` means
`include_total=true`; `none` means no dedicated public facets route is
registered for that family. Filter cells summarize implemented families;
exact accepted parameters remain in the handlers and MCP `tools/list`.

| Endpoint family | Cursor | Sort | Filters/query | Total | Facets/counts | MCP parity |
| --- | --- | --- | --- | --- | --- | --- |
| Tasks `/tasks`, `/tasks/search` | Paged; explicit offset incl. 0 without cursor emits offset/next_offset, otherwise cursor mode | `priority`, `status`, `updated_at`, `created_at`; default `priority asc`, `id asc` | Shared scopes/status/priority/tags/text/dates/budgets/presence/internal/static `eligible` filters; search requires `q` | Opt-in `include_total` on HTTP/MCP, exact filtered cohort before paging | Task facets + HTTP scope rollup | `torque_task_list` shares filters/query/meta; brief/verbose/typed projection differences retained |
| Epics `/epics` | paged by default | named entity allow-list | project, status, archive, search | opt-in total | `/epics/facets`, `torque_epic_facets` + requested-ID task rollups and exact task_totals | `torque_epic_list`, common query/count envelope |
| Sprints `/sprints` | paged by default | named entity allow-list | project, status, archive, search, over-budget/budget bounds | opt-in total | `/sprints/facets`, `torque_sprint_facets` + requested-ID task rollups and exact task_totals | `torque_sprint_list`, common query/count envelope |
| Projects `/projects` | paged by default | named entity allow-list | status, archive, search | opt-in total | `/projects/facets`, `torque_project_facets` + requested-ID task rollups, task_totals and children/child_totals | `torque_project_list`, common query/count envelope |
| Issues `/issues`, `/issues/search` | paged list/search by default | task allow-list | project, status, query, fixed issue kind | opt-in cohort total | Task facets with kind=issue; no dedicated route | `torque_issue_list`, common envelope/count semantics |
| Comments `/comments`, `/comments/search`, `/tasks/{id}/comments` | paged list/search/nested task list | `created_at` | entity scope(s), author, dates, search | opt-in total | none | `torque_comment_list`, `torque_comment_search`, shared 50/200/count policy |
| Runs `/runs` | Cursor default, 50/200 policy; explicit offset incl. 0 without cursor emits offset/next_offset (CW-0626) | `started_at`, `status`, `duration`, `cost`; default `started_at desc`, numeric `id asc` | Shared task/project/sprint/epic scopes, CSV status/executor/current task profile (CW-0663), inclusive since/until (RFC3339 or Unix millis) | Opt-in `include_total`, cohort before cursor/offset/limit | `/runs/facets`, `torque_run_facets`: status/executor/current task profile + ledger cost/run tokens | `torque_run_list` shares service query and items/meta; MCP byte trims preserve continuation |
| Sessions `/sessions` | Cursor default, optional offset, shared 50/200 | `created_at`, `state`; newest first, session ID asc | state, task/project scope, search on ID/task/workdir | Opt-in filtered cohort total | List total only; no dedicated facets | `torque_session_list`, same query/meta; existing Session snapshot preserved |
| Artifacts `/artifacts`, `/tasks/{id}/artifacts` | Cursor default, optional offset, shared 50/200 | `created_at`, `type`; newest first, numeric ID asc | Required task (path wins), type, run_id, content/URL/path search | Opt-in filtered cohort total | List total only; no dedicated facets | `torque_artifact_list`, same query/meta; brief/verbose and byte continuation retained |
| Collections `/collections` | Cursor default, optional offset, shared 50/200 | name/status/creation/update; default name asc, ID asc | active/archived/all (default active), ID/name/description search | Opt-in filtered cohort total | List total only; no dedicated facets | `torque_collection_list`, same query/meta |
| Collection tasks `/collections/{id}/tasks`, `/collections/inbox/tasks` | Cursor default, optional offset, shared 50/200 | position + task allow-list; default position asc, task ID asc | Path/inbox scope; task status/priority/project/sprint/epic, tags, text search | Opt-in scoped filtered cohort total | List total only; task facets have no collection_id scope filter | `torque_collection_tasks_list`, `torque_collection_inbox_list`; task projection retained |
| Plans `/plans` | Cursor default, optional offset, shared 50/200 | Task allow-list; priority asc, ID asc | Fixed plan kind; status/priority/project/sprint/epic, tags, search | Opt-in filtered plan cohort total | Task facets with plan kind | `torque_plan_list`, same query/meta; task projection retained |
| Plan children `/plans/{id}/children` | Cursor default, optional offset, shared 50/200 | Task allow-list; priority asc, task ID asc | Required plan path, phase_id; status/priority/project/sprint/epic, tags, search | Opt-in scoped filtered cohort total | Task facets accept parent_id; no phase_id facet filter | `torque_plan_list_children`, same query/meta; task projection retained |
| Checkpoints `/tasks/{id}/checkpoints`, `/checkpoints/pending`, `/sessions/{id}/checkpoints` | Cursor default, optional offset, shared 50/200 | Task: creation/status; history desc, pending asc, correlation ID asc. Recovery: creation desc, checkpoint ID asc | Preserve task/session/pending scopes; task history status; recovery ID/note or HITL correlation/type/payload search | Opt-in scoped filtered cohort total | List total only; no dedicated facets | `torque_task_checkpoint_list`, `torque_task_checkpoints_pending`, `torque_session_checkpoint_list`; HITL and recovery projections stay distinct |
| Templates `/templates` | Cursor default, optional offset, shared 50/200 | name/kind/creation/update; name asc, ID/version identity asc | kind, include_archived, ID/name/description search | Opt-in filtered version-row cohort total | List total only; no dedicated facets | `torque_template_list`, same query/meta; versions remain separate rows |
| Models `/models` | Cursor default, optional offset, shared 50/200 | name/provider_id/id/cost/context/output; name asc, provider/model identity asc | provider, case-insensitive name/model/provider search; catalog snapshot | Opt-in filtered catalog-row total (cold cache 0) | List total only; no dedicated facets | `torque_models_list`, same query/meta; HTTP and brief/verbose MCP projections retained |
| Messages `/messages/inbox`, `/messages/thread/{thread_id}` | Inbox drain list: each call delivers its page, no cursor/offset. Thread: cursor default; offset available on one store, positive federated offset rejects with use-cursor guidance | `created_at`; thread asc/desc, message ID asc; inbox asc only | Recipient/thread scopes, kind/channel/thread filters retained | Inbox: undelivered cohort before this drain. Thread: exact with one store; multiple stores omit total and emit total_unavailable=federated | Non-mutating count/peek; no message facets | No pure-read MCP thread list exists; broker inbox/inbox_poll remain actions unchanged. CW-20260912-0114 remains separate |

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

These are the implemented service/adapter allow-lists, defaults, unique
tie-breaks, and null/sentinel ordering. All listed sorts accept asc/desc except
the inbox drain and tag catalog, which preserve their fixed ascending order.

| Resource | Allowed `sort_by` | Default | Notes |
| --- | --- | --- | --- |
| Tasks, issues, plans | `priority`, `status`, `updated_at`, `created_at` | `priority asc`, `id asc` tie-break | HTTP/MCP; plans fix kind=plan |
| Projects | `name`, `status`, `updated_at`, `created_at` | `name asc`, `id asc` | HTTP/MCP |
| Epics, sprints | `name`, `status`, `updated_at`, `created_at` | `updated_at desc`, `id asc` | HTTP/MCP |
| Comments | `created_at` | List `asc`, search `desc`; numeric `id asc` | HTTP/MCP |
| Runs | `started_at`, `status`, `duration`, `cost` | `started_at desc`, numeric `id asc` | duration is rounded completed elapsed milliseconds, unfinished sentinel -1 |
| Sessions | `created_at`, `state` | `created_at desc`, `id asc` | HTTP/MCP |
| Artifacts | `created_at`, `type` | `created_at desc`, numeric `id asc` | creation order revised from the previous oldest-first source |
| Collections | `name`, `status`, `created_at`, `updated_at` | `name asc`, `id asc` | status derives active/archived from archived_at |
| Collection tasks | `position`, `priority`, `status`, `updated_at`, `created_at` | `position asc`, task `id asc` | null position maps to max int64 (last asc, first desc); task ID breaks ties. Inbox has null positions and therefore ID order by default |
| Plan children | `priority`, `status`, `updated_at`, `created_at` | `priority asc`, task `id asc` | phase filtered in SQL before paging/count |
| Task checkpoints | `created_at`, `status` | Task history `created_at desc`; pending queue `created_at asc`; correlation ID tie-break | created_at is the emitted_at alias |
| Session checkpoints | `created_at` | `created_at desc`, unique checkpoint ID | separate recovery record schema |
| Templates | `name`, `kind`, `created_at`, `updated_at` | `name asc`, template ID/version tie-break | lexical ID + colon + decimal version identity; versions are distinct rows |
| Models | `name`, `provider_id`, `id`, `cost`, `context`, `output` | `name asc`, provider/model pair tie-break | catalog-backed; provider/model JSON identity. Input USD/million tokens and context/output token limits sort numerically before paging. Zero represents free or unknown (first asc, last desc) |
| Messages | `created_at` | `created_at asc`, message ID tie-break | inbox asc only, thread asc/desc |

### HTTP exceptions outside the matrix

`GET /tags` without any query string still returns the complete legacy
`{tags:[...]}` catalog. Supplying a query string selects its bounded 50/200
cursor path with `{tags,total,returned,limit,has_more,next_cursor,sort_by,sort_dir}`,
fixed `name COLLATE NOCASE asc, slug asc`, `query`/`color` filters, and an
always-computed total. This HTTP path does not accept `include_total`.
`torque_tag_list` instead uses the common MCP items/meta envelope and opt-in
filtered total.

`GET /tasks/{id}/subtodos` still returns the whole structural checklist as
`{subtodos:[...]}`. `torque_task_subtodo_list` uses common 50/200 cursor pages,
`position asc` by default (desc supported), and optional total. Because the
checklist is stored as parent JSON, MCP paging bounds the response rather than
avoiding the parent read; edits can move positions between pages.

## Source evidence and reconciliation

Reconciliation reads authored merged code first. Relevant implementations:

- [Task query](../internal/service/task_query.go),
  [adjacent query](../internal/service/adjacent_query.go),
  [resource sort/query policy](../internal/service/resource_pages.go),
  [run query](../internal/service/run_query.go), and
  [cursor/meta implementation](../internal/service/pagination/) define defaults,
  validation, count opt-in, and continuation.
- [Resource store queries](../internal/persistence/sqlstore/resource_pages.go)
  preserve nested scopes before paging/count and implement composite identities.
  [Model paging](../internal/service/model_pages.go) sorts one catalog snapshot.
- [Message page stores](../internal/messaging/pages.go) and
  [message metadata](../internal/messaging/page_wire.go) separate read history
  from delivery batches and preserve federated count limitations.
- [HTTP router](../internal/httpserver/server.go), family handlers, and
  [MCP envelopes](../internal/mcpadapter/response.go) establish public shapes,
  byte-cap continuation, and the explicit tag/checklist exceptions above.

Existing task/run/adjacent/resource/model/message parity tests cover
empty/final pages, equal sort keys, cursor/sort/offset rejection, filtered and
nested totals, and byte-cap continuation. The integrated
[MCP behavioral table](../internal/mcpadapter/list_policy_test.go) covers
50/200, cursor/has_more presence, and returned matching payload length.
No documentation/source-agreement test or inventory guard is added.
COUNT measurements and reproducible index evidence are linked above; no export
route is registered. Final merged-tree verification awaits CW-0677.

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

Runs browsing lazily appends pages with server filters/sorts and exact facet
headers from the same cohort. Task Logs expose older runs via cursor continuation.
Dashboard totals and charts use run facets and server time-series; recent-run
widgets show their loaded page. SSE patches loaded rows; Refresh restarts the
list for new rows. No list client preloads the whole run cohort. Index and
PostgreSQL COUNT evidence is recorded above.

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
and unpaged search body are removed. The Torque GUI decodes `ListPage` and
loads one page per request, with explicit continuation. Board totals come from
facets, and Dashboard task totals come from aggregates; page length is not a
cohort count. Tachyon work-ops and Tangent have separate cross-project adapters.
Merge state does not establish deployment state.

### Remaining-family reconciliation (CW-20261001-0565)

The remaining-family rows describe authored HTTP, service/store, MCP and GUI
implementations. The old named arrays and raw list shapes are removed. GUI
callers consume one page per request and retain record projections. Resource
pages expose continuation; widgets and activity timelines label their loaded
rows. Messaging keeps delivery batches separate from paged thread history.
No live deployment is included.
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
