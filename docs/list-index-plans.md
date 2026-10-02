# List index audit — CW-20261001-0570

Snapshot of the authored SQL at main `37fa8c0` (including `/runs`), plus
migration 035. This is evidence for that migration, not a performance gate.
The [complete result](https://github.com/hollis-labs/torque/blob/a22bcfa9860bc12d59681389d0959e2c9915aaf6/docs/evidence/list-index-plans-20261001.json) records each SQL
query, both SQLite EXPLAIN QUERY PLAN outputs, both PostgreSQL plan trees,
and COUNT queries with EXPLAIN ANALYZE/BUFFERS and timing ranges. The raw JSON
is archived at that immutable PR commit; this summary and the reproduction
script remain in the tree under the [evidence convention](evidence/README.md).

## Method and limits

- SQLite 3.53.4 is Torque's pinned modernc driver, not Python's bundled planner.
  PostgreSQL is 17.11 in a disposable `postgres:17-alpine` container.
- All data is synthetic. The 4,330-task cardinality came from the read-only
  live `/tasks/facets` matching count on 2026-10-01; no operator database was
  opened or copied. Runs/comments/sessions/messages/artifacts/checkpoints each
  have 4,330 rows, templates 4,330 versions, projects 43, epics 200, sprints
  400, collections 40, and cost ledger 4,330 rows. Those other sizes are
  scenarios, not claims about current production cardinalities. Ledger rows
  are separate from run costs and its run_id index is deliberately non-unique.
- SQLite fixture schema comes from migrations through 034. The PostgreSQL plan
  fixture recreates that schema with typed timestamps/serial IDs/bytea and
  integer flags, omitting foreign keys. Separate populated-upgrade tests retain
  and exercise foreign-key constraints on both stores. Fresh PostgreSQL
  bootstrap remains CW-20261001-0622; the plan fixture does not establish that
  historical SQLite migrations or every legacy query are portable.
- Allowed sorts and default common cohorts use a 51-row page (50 plus the
  continuation probe). The runs projection is read from the merged
  `run_query.go`; aliases, computed sort value and cohort predicates match
  that builder. Other list probes project complete rows and preserve their
  filter/order expressions. Equality-filter columns appearing at the start of
  some ORDER BY probes are constants and do not alter result ordering.
- Both directions keep id ASC, except existing task-checkpoint history which
  keeps id DESC. Timestamp sorts on SQLite use the exact normalized key from
  `timestamp_key.go`; raw created_at/updated_at/started_at indexes would not
  serve it. Duration normalizes both timestamps before julianday subtraction;
  cost uses COALESCE(cost,0). No stored timestamps are normalized or rewritten.
- The migration targets observed full/partial sorts and repeated scans. It
  does not add the cross-product of every filter and every sort. PostgreSQL
  still prefers sequential/bitmap scans plus sorting for several small or
  selective cohorts, including project-filtered runs; this is reported below,
  not overridden with planner settings. SQLite's existing runs(status) index
  already includes integer rowid/id ascending, so only PostgreSQL adds the
  explicit status ASC,id ASC variant.
- Models and task-owned subtodos are catalog/in-row data, not SQL list scans.
  Issues and plans use task sorts; plan children use the parent_id cohort.
  Proposed sort fields for other families are not indexed before their query
  expressions exist. Existing session-checkpoint and project-artifact shapes,
  tags, and additional filtered queries are shown as audit-only rows; empty
  auxiliary fixtures are not evidence to add indexes for those families.

Reproduce from this checkout (Go, Python 3 and Docker required):

```sh
python3 scripts/audit-list-indexes.py --output /tmp/list-index-audit.json
```

The script owns a uniquely named, network-disabled Docker container and removes
it and its temporary files on exit. It isolates probe HOME and never invokes a
model CLI. The JSON is a review artifact; timings and optimizer choices are not
asserted by tests.

## Initial-page plans

The named index column associates a probe with the selected migration family;
for runs/status_asc it is PostgreSQL-only, while SQLite retains idx_runs_status.
`audit only` means no extra index was selected for that query. Actual selected
indexes appear in each EXPLAIN output.

| Query | Index family | SQLite before → after | PostgreSQL before → after |
|---|---|---|---|
| `tasks/priority_asc` | `idx_tasks_page_priority_asc` | `SCAN tasks USING INDEX idx_tasks_priority; USE TEMP B-TREE FOR LAST TERM OF ORDER BY` → `SCAN tasks USING INDEX idx_tasks_page_priority_asc` | Limit → Incremental Sort → Index Scan idx_tasks_priority → Limit → Index Scan idx_tasks_page_priority_asc |
| `tasks/priority_desc` | `idx_tasks_page_priority_desc` | `SCAN tasks USING INDEX idx_tasks_priority; USE TEMP B-TREE FOR LAST TERM OF ORDER BY` → `SCAN tasks USING INDEX idx_tasks_page_priority_desc` | Limit → Incremental Sort → Index Scan idx_tasks_priority → Limit → Index Scan idx_tasks_page_priority_desc |
| `tasks/status_asc` | `idx_tasks_page_status_asc` | `SCAN tasks USING INDEX idx_tasks_status; USE TEMP B-TREE FOR LAST TERM OF ORDER BY` → `SCAN tasks USING INDEX idx_tasks_page_status_asc` | Limit → Incremental Sort → Index Scan idx_tasks_status → Limit → Index Scan idx_tasks_page_status_asc |
| `tasks/status_desc` | `idx_tasks_page_status_desc` | `SCAN tasks USING INDEX idx_tasks_status; USE TEMP B-TREE FOR LAST TERM OF ORDER BY` → `SCAN tasks USING INDEX idx_tasks_page_status_desc` | Limit → Incremental Sort → Index Scan idx_tasks_status → Limit → Index Scan idx_tasks_page_status_desc |
| `tasks/updated_at_asc` | `idx_tasks_page_updated_at_asc` | `SCAN tasks; USE TEMP B-TREE FOR ORDER BY` → `SCAN tasks USING INDEX idx_tasks_page_updated_at_asc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_tasks_page_updated_at_asc |
| `tasks/updated_at_desc` | `idx_tasks_page_updated_at_desc` | `SCAN tasks; USE TEMP B-TREE FOR ORDER BY` → `SCAN tasks USING INDEX idx_tasks_page_updated_at_desc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_tasks_page_updated_at_desc |
| `tasks/created_at_asc` | `idx_tasks_page_created_at_asc` | `SCAN tasks; USE TEMP B-TREE FOR ORDER BY` → `SCAN tasks USING INDEX idx_tasks_page_created_at_asc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_tasks_page_created_at_asc |
| `tasks/created_at_desc` | `idx_tasks_page_created_at_desc` | `SCAN tasks; USE TEMP B-TREE FOR ORDER BY` → `SCAN tasks USING INDEX idx_tasks_page_created_at_desc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_tasks_page_created_at_desc |
| `projects/name_asc` | `idx_projects_page_name_asc` | `SCAN projects; USE TEMP B-TREE FOR ORDER BY` → `SCAN projects USING INDEX idx_projects_page_name_asc` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `projects/name_desc` | `idx_projects_page_name_desc` | `SCAN projects; USE TEMP B-TREE FOR ORDER BY` → `SCAN projects USING INDEX idx_projects_page_name_desc` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `projects/status_asc` | `idx_projects_page_status_asc` | `SCAN projects; USE TEMP B-TREE FOR ORDER BY` → `SCAN projects USING INDEX idx_projects_page_status_asc` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `projects/status_desc` | `idx_projects_page_status_desc` | `SCAN projects; USE TEMP B-TREE FOR ORDER BY` → `SCAN projects USING INDEX idx_projects_page_status_desc` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `projects/updated_at_asc` | `idx_projects_page_updated_at_asc` | `SCAN projects; USE TEMP B-TREE FOR ORDER BY` → `SCAN projects USING INDEX idx_projects_page_updated_at_asc` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `projects/updated_at_desc` | `idx_projects_page_updated_at_desc` | `SCAN projects; USE TEMP B-TREE FOR ORDER BY` → `SCAN projects USING INDEX idx_projects_page_updated_at_desc` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `projects/created_at_asc` | `idx_projects_page_created_at_asc` | `SCAN projects; USE TEMP B-TREE FOR ORDER BY` → `SCAN projects USING INDEX idx_projects_page_created_at_asc` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `projects/created_at_desc` | `idx_projects_page_created_at_desc` | `SCAN projects; USE TEMP B-TREE FOR ORDER BY` → `SCAN projects USING INDEX idx_projects_page_created_at_desc` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `epics/name_asc` | `idx_epics_page_name_asc` | `SCAN epics; USE TEMP B-TREE FOR ORDER BY` → `SCAN epics USING INDEX idx_epics_page_name_asc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_epics_page_name_asc |
| `epics/name_desc` | `idx_epics_page_name_desc` | `SCAN epics; USE TEMP B-TREE FOR ORDER BY` → `SCAN epics USING INDEX idx_epics_page_name_desc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_epics_page_name_desc |
| `epics/status_asc` | `idx_epics_page_status_asc` | `SCAN epics; USE TEMP B-TREE FOR ORDER BY` → `SCAN epics USING INDEX idx_epics_page_status_asc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_epics_page_status_asc |
| `epics/status_desc` | `idx_epics_page_status_desc` | `SCAN epics; USE TEMP B-TREE FOR ORDER BY` → `SCAN epics USING INDEX idx_epics_page_status_desc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_epics_page_status_desc |
| `epics/updated_at_asc` | `idx_epics_page_updated_at_asc` | `SCAN epics; USE TEMP B-TREE FOR ORDER BY` → `SCAN epics USING INDEX idx_epics_page_updated_at_asc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_epics_page_updated_at_asc |
| `epics/updated_at_desc` | `idx_epics_page_updated_at_desc` | `SCAN epics; USE TEMP B-TREE FOR ORDER BY` → `SCAN epics USING INDEX idx_epics_page_updated_at_desc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_epics_page_updated_at_desc |
| `epics/created_at_asc` | `idx_epics_page_created_at_asc` | `SCAN epics; USE TEMP B-TREE FOR ORDER BY` → `SCAN epics USING INDEX idx_epics_page_created_at_asc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_epics_page_created_at_asc |
| `epics/created_at_desc` | `idx_epics_page_created_at_desc` | `SCAN epics; USE TEMP B-TREE FOR ORDER BY` → `SCAN epics USING INDEX idx_epics_page_created_at_desc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_epics_page_created_at_desc |
| `sprints/name_asc` | `idx_sprints_page_name_asc` | `SCAN sprints; USE TEMP B-TREE FOR ORDER BY` → `SCAN sprints USING INDEX idx_sprints_page_name_asc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_sprints_page_name_asc |
| `sprints/name_desc` | `idx_sprints_page_name_desc` | `SCAN sprints; USE TEMP B-TREE FOR ORDER BY` → `SCAN sprints USING INDEX idx_sprints_page_name_desc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_sprints_page_name_desc |
| `sprints/status_asc` | `idx_sprints_page_status_asc` | `SCAN sprints; USE TEMP B-TREE FOR ORDER BY` → `SCAN sprints USING INDEX idx_sprints_page_status_asc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_sprints_page_status_asc |
| `sprints/status_desc` | `idx_sprints_page_status_desc` | `SCAN sprints; USE TEMP B-TREE FOR ORDER BY` → `SCAN sprints USING INDEX idx_sprints_page_status_desc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_sprints_page_status_desc |
| `sprints/updated_at_asc` | `idx_sprints_page_updated_at_asc` | `SCAN sprints; USE TEMP B-TREE FOR ORDER BY` → `SCAN sprints USING INDEX idx_sprints_page_updated_at_asc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_sprints_page_updated_at_asc |
| `sprints/updated_at_desc` | `idx_sprints_page_updated_at_desc` | `SCAN sprints; USE TEMP B-TREE FOR ORDER BY` → `SCAN sprints USING INDEX idx_sprints_page_updated_at_desc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_sprints_page_updated_at_desc |
| `sprints/created_at_asc` | `idx_sprints_page_created_at_asc` | `SCAN sprints; USE TEMP B-TREE FOR ORDER BY` → `SCAN sprints USING INDEX idx_sprints_page_created_at_asc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_sprints_page_created_at_asc |
| `sprints/created_at_desc` | `idx_sprints_page_created_at_desc` | `SCAN sprints; USE TEMP B-TREE FOR ORDER BY` → `SCAN sprints USING INDEX idx_sprints_page_created_at_desc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_sprints_page_created_at_desc |
| `runs/started_at_asc` | `idx_runs_page_started_at_asc` | `SCAN r; USE TEMP B-TREE FOR ORDER BY` → `SCAN r USING INDEX idx_runs_page_started_at_asc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_runs_page_started_at_asc |
| `runs/started_at_desc` | `idx_runs_page_started_at_desc` | `SCAN r; USE TEMP B-TREE FOR ORDER BY` → `SCAN r USING INDEX idx_runs_page_started_at_desc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_runs_page_started_at_desc |
| `runs/status_asc` | `idx_runs_page_status_asc` | `SCAN r USING INDEX idx_runs_status` → `SCAN r USING INDEX idx_runs_status` | Limit → Incremental Sort → Index Scan idx_runs_status → Limit → Index Scan idx_runs_page_status_asc |
| `runs/status_desc` | `idx_runs_page_status_desc` | `SCAN r USING INDEX idx_runs_status; USE TEMP B-TREE FOR LAST TERM OF ORDER BY` → `SCAN r USING INDEX idx_runs_page_status_desc` | Limit → Incremental Sort → Index Scan idx_runs_status → Limit → Index Scan idx_runs_page_status_desc |
| `runs/duration_asc` | `idx_runs_page_duration_asc` | `SCAN r; USE TEMP B-TREE FOR ORDER BY` → `SCAN r USING INDEX idx_runs_page_duration_asc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_runs_page_duration_asc |
| `runs/duration_desc` | `idx_runs_page_duration_desc` | `SCAN r; USE TEMP B-TREE FOR ORDER BY` → `SCAN r USING INDEX idx_runs_page_duration_desc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_runs_page_duration_desc |
| `runs/cost_asc` | `idx_runs_page_cost_asc` | `SCAN r; USE TEMP B-TREE FOR ORDER BY` → `SCAN r USING INDEX idx_runs_page_cost_asc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_runs_page_cost_asc |
| `runs/cost_desc` | `idx_runs_page_cost_desc` | `SCAN r; USE TEMP B-TREE FOR ORDER BY` → `SCAN r USING INDEX idx_runs_page_cost_desc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_runs_page_cost_desc |
| `tasks/status_priority` | `idx_tasks_page_status_priority` | `SEARCH tasks USING INDEX idx_tasks_status (status=?); USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH tasks USING INDEX idx_tasks_page_status_priority (status=?)` | Limit → Incremental Sort → Index Scan idx_tasks_priority → Limit → Index Scan idx_tasks_page_status_priority |
| `tasks/project_id_priority` | `idx_tasks_page_project_id_priority` | `SEARCH tasks USING INDEX idx_tasks_project (project_id=?); USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH tasks USING INDEX idx_tasks_page_project_id_priority (project_id=?)` | Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_tasks_project → Limit → Index Scan idx_tasks_page_project_id_priority |
| `tasks/epic_id_priority` | `idx_tasks_page_epic_id_priority` | `SEARCH tasks USING INDEX idx_tasks_epic (epic_id=?); USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH tasks USING INDEX idx_tasks_page_epic_id_priority (epic_id=?)` | Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_tasks_epic → Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_tasks_page_epic_id_priority |
| `tasks/sprint_id_priority` | `idx_tasks_page_sprint_id_priority` | `SEARCH tasks USING INDEX idx_tasks_sprint (sprint_id=?); USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH tasks USING INDEX idx_tasks_page_sprint_id_priority (sprint_id=?)` | Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_tasks_sprint → Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_tasks_page_sprint_id_priority |
| `tasks/kind_priority` | `idx_tasks_page_kind_priority` | `SEARCH tasks USING INDEX idx_tasks_kind_status (kind=?); USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH tasks USING INDEX idx_tasks_page_kind_priority (kind=?)` | Limit → Incremental Sort → Index Scan idx_tasks_priority → Limit → Index Scan idx_tasks_page_kind_priority |
| `tasks/parent_id_priority` | `idx_tasks_page_parent_id_priority` | `SEARCH tasks USING INDEX idx_tasks_parent_id (parent_id=?); USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH tasks USING INDEX idx_tasks_page_parent_id_priority (parent_id=?)` | Limit → Sort → Index Scan idx_tasks_parent_id → Limit → Index Scan idx_tasks_page_parent_id_priority |
| `epics/project_updated` | `idx_epics_page_project_updated` | `SCAN epics; USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH epics USING INDEX idx_epics_page_project_updated (project_id=?)` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `sprints/project_updated` | `idx_sprints_page_project_updated` | `SCAN sprints; USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH sprints USING INDEX idx_sprints_page_project_updated (project_id=?)` | Limit → Sort → Seq Scan → Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_sprints_page_project_updated |
| `runs/status_started` | `idx_runs_page_status_started` | `SEARCH r USING INDEX idx_runs_status (status=?); USE TEMP B-TREE FOR ORDER BY` → `SEARCH r USING INDEX idx_runs_page_status_started (status=?)` | Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_runs_status → Limit → Index Scan idx_runs_page_status_started |
| `runs/task_id_started` | `idx_runs_page_task_id_started` | `SEARCH r USING INDEX idx_runs_task (task_id=?); USE TEMP B-TREE FOR ORDER BY` → `SEARCH r USING INDEX idx_runs_page_task_id_started (task_id=?)` | Limit → Sort → Index Scan idx_runs_task → Limit → Index Scan idx_runs_page_task_id_started |
| `comments/created_asc` | `idx_comments_page_created_asc` | `SCAN comments; USE TEMP B-TREE FOR ORDER BY` → `SCAN comments USING INDEX idx_comments_page_created_asc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_comments_page_created_asc |
| `comments/entity_created_asc` | `idx_comments_page_entity_created_asc` | `SEARCH comments USING INDEX idx_comments_entity (entity_type=? AND entity_id=?); USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH comments USING INDEX idx_comments_page_entity_created_asc (entity_type=? AND entity_id=?)` | Limit → Incremental Sort → Index Scan idx_comments_entity → Limit → Index Scan idx_comments_page_entity_created_asc |
| `comments/created_desc` | `idx_comments_page_created_desc` | `SCAN comments; USE TEMP B-TREE FOR ORDER BY` → `SCAN comments USING INDEX idx_comments_page_created_desc` | Limit → Sort → Seq Scan → Limit → Index Scan idx_comments_page_created_desc |
| `comments/entity_created_desc` | `idx_comments_page_entity_created_desc` | `SEARCH comments USING INDEX idx_comments_entity (entity_type=? AND entity_id=?); USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH comments USING INDEX idx_comments_page_entity_created_desc (entity_type=? AND entity_id=?)` | Limit → Incremental Sort → Index Scan idx_comments_entity → Limit → Index Scan idx_comments_page_entity_created_desc |
| `artifacts/task_created` | `idx_artifacts_page_task_created` | `SEARCH artifacts USING INDEX idx_artifacts_task (task_id=?); USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH artifacts USING INDEX idx_artifacts_page_task_created (task_id=?)` | Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_artifacts_task → Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_artifacts_page_task_created |
| `sessions/created` | `idx_sessions_page_created` | `SCAN sessions; USE TEMP B-TREE FOR ORDER BY` → `SCAN sessions USING INDEX idx_sessions_page_created` | Limit → Sort → Seq Scan → Limit → Index Scan idx_sessions_page_created |
| `sessions/state_created` | `idx_sessions_page_state_created` | `SEARCH sessions USING INDEX idx_sessions_state (state=?); USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH sessions USING INDEX idx_sessions_page_state_created (state=?)` | Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_sessions_state → Limit → Index Scan idx_sessions_page_state_created |
| `sessions/project_id_created` | `idx_sessions_page_project_id_created` | `SEARCH sessions USING INDEX idx_sessions_project_id (project_id=?); USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH sessions USING INDEX idx_sessions_page_project_id_created (project_id=?)` | Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_sessions_project_id → Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_sessions_page_project_id_created |
| `sessions/task_id_created` | `idx_sessions_page_task_id_created` | `SEARCH sessions USING INDEX idx_sessions_task_id (task_id=?); USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH sessions USING INDEX idx_sessions_page_task_id_created (task_id=?)` | Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_sessions_task_id → Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_sessions_page_task_id_created |
| `messages/to_urn_created` | `idx_messages_page_to_urn_created` | `SEARCH messages USING INDEX idx_messages_to_urn_created (to_urn=?); USE TEMP B-TREE FOR LAST TERM OF ORDER BY` → `SEARCH messages USING INDEX idx_messages_page_to_urn_created (to_urn=?)` | Limit → Incremental Sort → Index Scan idx_messages_to_urn_created → Limit → Index Scan idx_messages_page_to_urn_created |
| `messages/thread_id_created` | `idx_messages_page_thread_id_created` | `SCAN messages; USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH messages USING INDEX idx_messages_page_thread_id_created (thread_id=?)` | Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_messages_thread → Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_messages_page_thread_id_created |
| `collections/created` | `idx_collections_page_created` | `SCAN collections; USE TEMP B-TREE FOR ORDER BY` → `SCAN collections USING INDEX idx_collections_page_created` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `checkpoints/task_emitted` | `idx_checkpoints_page_task_emitted` | `SEARCH checkpoints USING INDEX idx_checkpoints_task_status (task_id=?); USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH checkpoints USING INDEX idx_checkpoints_page_task_emitted (task_id=?)` | Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_checkpoints_task_status → Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_checkpoints_page_task_emitted |
| `checkpoints/pending_emitted` | `idx_checkpoints_page_pending_emitted` | `SEARCH checkpoints USING INDEX idx_checkpoints_pending (status=?); USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH checkpoints USING INDEX idx_checkpoints_page_pending_emitted (status=?)` | Limit → Sort → Seq Scan → Limit → Index Scan idx_checkpoints_page_pending_emitted |
| `templates/existing_pk` | `idx_templates_page_id_version` | `SCAN task_templates USING INDEX sqlite_autoindex_task_templates_1; USE TEMP B-TREE FOR LAST TERM OF ORDER BY` → `SCAN task_templates USING INDEX idx_templates_page_id_version` | Limit → Incremental Sort → Index Scan task_templates_pkey → Limit → Index Scan idx_templates_page_id_version |
| `collection_tasks/existing_position` | `idx_tasks_page_collection_position` | `SEARCH tasks USING INDEX idx_tasks_collection_position (collection_id=?); USE TEMP B-TREE FOR ORDER BY` → `SEARCH tasks USING INDEX idx_tasks_page_collection_position (collection_id=?)` | Limit → Sort → Index Scan idx_tasks_collection_position → Limit → Index Scan idx_tasks_page_collection_position |
| `tags/name` | `audit only` | `SCAN tags; USE TEMP B-TREE FOR ORDER BY` → `SCAN tags; USE TEMP B-TREE FOR ORDER BY` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `runs/project_cohort` | `audit only` | `SEARCH t USING INDEX idx_tasks_project (project_id=?); SEARCH r USING INDEX idx_runs_task (task_id=?); USE TEMP B-TREE FOR ORDER BY` → `SEARCH t USING COVERING INDEX idx_tasks_page_project_id_priority (project_id=?); SEARCH r USING INDEX idx_runs_task (task_id=?); USE TEMP B-TREE FOR ORDER BY` | Limit → Sort → Hash Join → Seq Scan → Hash → Bitmap Heap Scan → Bitmap Index Scan idx_tasks_project → Limit → Sort → Hash Join → Seq Scan → Hash → Bitmap Heap Scan → Bitmap Index Scan idx_tasks_page_project_id_priority |
| `runs/status_time` | `audit only` | `SEARCH r USING INDEX idx_runs_status (status=?); USE TEMP B-TREE FOR ORDER BY` → `SEARCH r USING INDEX idx_runs_page_started_at_desc (<expr>>?)` | Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_runs_status → Limit → Index Scan idx_runs_page_started_at_desc |
| `comments/author_time` | `audit only` | `SCAN comments; USE TEMP B-TREE FOR ORDER BY` → `SEARCH comments USING INDEX idx_comments_page_created_desc (<expr>>?)` | Limit → Sort → Seq Scan → Limit → Index Scan idx_comments_page_created_desc |
| `session_checkpoints/existing_scope` | `audit only` | `SEARCH session_checkpoints USING INDEX idx_session_checkpoints_session (session_id=?)` → `SEARCH session_checkpoints USING INDEX idx_session_checkpoints_session (session_id=?)` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `project_artifacts/existing_scope` | `audit only` | `SEARCH project_artifacts USING INDEX idx_project_artifacts_project (project_id=?); USE TEMP B-TREE FOR ORDER BY` → `SEARCH project_artifacts USING INDEX idx_project_artifacts_project (project_id=?); USE TEMP B-TREE FOR ORDER BY` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `runs/facets_cost` | `idx_cost_run` | `SCAN r; CORRELATED SCALAR SUBQUERY 1; BLOOM FILTER ON l (run_id=?); SEARCH l USING AUTOMATIC COVERING INDEX (run_id=?)` → `SCAN r; CORRELATED SCALAR SUBQUERY 1; SEARCH l USING INDEX idx_cost_run (run_id=?)` | Aggregate → Seq Scan → Aggregate → Seq Scan → Aggregate → Seq Scan → Aggregate → Index Scan idx_cost_run |

## Cursor-page plans

The keyset predicate is `key > value OR (key = value AND id > last_id)` for
ascending sort, and `<` on the first arm for descending sort. The same cohort
filters apply. Full queries and bound values are in the JSON artifact.

| Query | SQLite before → after | PostgreSQL before → after |
|---|---|---|
| `tasks/priority_asc/cursor` | `SEARCH tasks USING INDEX idx_tasks_priority (priority>?); USE TEMP B-TREE FOR LAST TERM OF ORDER BY` → `SEARCH tasks USING INDEX idx_tasks_page_priority_asc (priority>?)` | Limit → Incremental Sort → Index Scan idx_tasks_priority → Limit → Index Scan idx_tasks_page_priority_asc |
| `tasks/priority_desc/cursor` | `SEARCH tasks USING INDEX idx_tasks_priority (priority<?); USE TEMP B-TREE FOR LAST TERM OF ORDER BY` → `SEARCH tasks USING INDEX idx_tasks_page_priority_desc (priority<?)` | Limit → Incremental Sort → Index Scan idx_tasks_priority → Limit → Index Scan idx_tasks_page_priority_desc |
| `tasks/status_asc/cursor` | `SEARCH tasks USING INDEX idx_tasks_status (status>?); USE TEMP B-TREE FOR LAST TERM OF ORDER BY` → `SEARCH tasks USING INDEX idx_tasks_page_status_asc (status>?)` | Limit → Sort → Bitmap Heap Scan → BitmapOr → Bitmap Index Scan idx_tasks_status → Bitmap Index Scan idx_tasks_status → Limit → Index Scan idx_tasks_page_status_asc |
| `tasks/status_desc/cursor` | `SEARCH tasks USING INDEX idx_tasks_status (status<?); USE TEMP B-TREE FOR LAST TERM OF ORDER BY` → `SEARCH tasks USING INDEX idx_tasks_page_status_desc (status<?)` | Limit → Incremental Sort → Index Scan idx_tasks_status → Limit → Index Scan idx_tasks_page_status_desc |
| `tasks/updated_at_asc/cursor` | `SCAN tasks; USE TEMP B-TREE FOR ORDER BY` → `SEARCH tasks USING INDEX idx_tasks_page_updated_at_asc (<expr>>?)` | Limit → Sort → Seq Scan → Limit → Index Scan idx_tasks_page_updated_at_asc |
| `tasks/updated_at_desc/cursor` | `SCAN tasks; USE TEMP B-TREE FOR ORDER BY` → `SEARCH tasks USING INDEX idx_tasks_page_updated_at_desc (<expr><?)` | Limit → Sort → Seq Scan → Limit → Index Scan idx_tasks_page_updated_at_desc |
| `tasks/created_at_asc/cursor` | `SCAN tasks; USE TEMP B-TREE FOR ORDER BY` → `SEARCH tasks USING INDEX idx_tasks_page_created_at_asc (<expr>>?)` | Limit → Sort → Seq Scan → Limit → Index Scan idx_tasks_page_created_at_asc |
| `tasks/created_at_desc/cursor` | `SCAN tasks; USE TEMP B-TREE FOR ORDER BY` → `SEARCH tasks USING INDEX idx_tasks_page_created_at_desc (<expr><?)` | Limit → Sort → Seq Scan → Limit → Index Scan idx_tasks_page_created_at_desc |
| `projects/name_asc/cursor` | `SCAN projects; USE TEMP B-TREE FOR ORDER BY` → `SEARCH projects USING INDEX idx_projects_page_name_asc (name>?)` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `projects/name_desc/cursor` | `SCAN projects; USE TEMP B-TREE FOR ORDER BY` → `SEARCH projects USING INDEX idx_projects_page_name_desc (name<?)` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `projects/status_asc/cursor` | `SCAN projects; USE TEMP B-TREE FOR ORDER BY` → `SEARCH projects USING INDEX idx_projects_page_status_asc (status>?)` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `projects/status_desc/cursor` | `SCAN projects; USE TEMP B-TREE FOR ORDER BY` → `SEARCH projects USING INDEX idx_projects_page_status_desc (status<?)` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `projects/updated_at_asc/cursor` | `SCAN projects; USE TEMP B-TREE FOR ORDER BY` → `SEARCH projects USING INDEX idx_projects_page_updated_at_asc (<expr>>?)` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `projects/updated_at_desc/cursor` | `SCAN projects; USE TEMP B-TREE FOR ORDER BY` → `SEARCH projects USING INDEX idx_projects_page_updated_at_desc (<expr><?)` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `projects/created_at_asc/cursor` | `SCAN projects; USE TEMP B-TREE FOR ORDER BY` → `SEARCH projects USING INDEX idx_projects_page_created_at_asc (<expr>>?)` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `projects/created_at_desc/cursor` | `SCAN projects; USE TEMP B-TREE FOR ORDER BY` → `SEARCH projects USING INDEX idx_projects_page_created_at_desc (<expr><?)` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `epics/name_asc/cursor` | `SCAN epics; USE TEMP B-TREE FOR ORDER BY` → `SEARCH epics USING INDEX idx_epics_page_name_asc (name>?)` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `epics/name_desc/cursor` | `SCAN epics; USE TEMP B-TREE FOR ORDER BY` → `SEARCH epics USING INDEX idx_epics_page_name_desc (name<?)` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `epics/status_asc/cursor` | `SCAN epics; USE TEMP B-TREE FOR ORDER BY` → `SEARCH epics USING INDEX idx_epics_page_status_asc (status>?)` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `epics/status_desc/cursor` | `SCAN epics; USE TEMP B-TREE FOR ORDER BY` → `SEARCH epics USING INDEX idx_epics_page_status_desc (status<?)` | Limit → Sort → Seq Scan → Limit → Index Scan idx_epics_page_status_desc |
| `epics/updated_at_asc/cursor` | `SCAN epics; USE TEMP B-TREE FOR ORDER BY` → `SEARCH epics USING INDEX idx_epics_page_updated_at_asc (<expr>>?)` | Limit → Sort → Seq Scan → Limit → Index Scan idx_epics_page_updated_at_asc |
| `epics/updated_at_desc/cursor` | `SCAN epics; USE TEMP B-TREE FOR ORDER BY` → `SEARCH epics USING INDEX idx_epics_page_updated_at_desc (<expr><?)` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `epics/created_at_asc/cursor` | `SCAN epics; USE TEMP B-TREE FOR ORDER BY` → `SEARCH epics USING INDEX idx_epics_page_created_at_asc (<expr>>?)` | Limit → Sort → Seq Scan → Limit → Index Scan idx_epics_page_created_at_asc |
| `epics/created_at_desc/cursor` | `SCAN epics; USE TEMP B-TREE FOR ORDER BY` → `SEARCH epics USING INDEX idx_epics_page_created_at_desc (<expr><?)` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `sprints/name_asc/cursor` | `SCAN sprints; USE TEMP B-TREE FOR ORDER BY` → `SEARCH sprints USING INDEX idx_sprints_page_name_asc (name>?)` | Limit → Sort → Seq Scan → Limit → Index Scan idx_sprints_page_name_asc |
| `sprints/name_desc/cursor` | `SCAN sprints; USE TEMP B-TREE FOR ORDER BY` → `SEARCH sprints USING INDEX idx_sprints_page_name_desc (name<?)` | Limit → Sort → Seq Scan → Limit → Index Scan idx_sprints_page_name_desc |
| `sprints/status_asc/cursor` | `SCAN sprints; USE TEMP B-TREE FOR ORDER BY` → `SEARCH sprints USING INDEX idx_sprints_page_status_asc (status>?)` | Limit → Sort → Seq Scan → Limit → Sort → Bitmap Heap Scan → BitmapOr → Bitmap Index Scan idx_sprints_page_status_desc → Bitmap Index Scan idx_sprints_page_status_desc |
| `sprints/status_desc/cursor` | `SCAN sprints; USE TEMP B-TREE FOR ORDER BY` → `SEARCH sprints USING INDEX idx_sprints_page_status_desc (status<?)` | Limit → Sort → Seq Scan → Limit → Index Scan idx_sprints_page_status_desc |
| `sprints/updated_at_asc/cursor` | `SCAN sprints; USE TEMP B-TREE FOR ORDER BY` → `SEARCH sprints USING INDEX idx_sprints_page_updated_at_asc (<expr>>?)` | Limit → Sort → Seq Scan → Limit → Index Scan idx_sprints_page_updated_at_asc |
| `sprints/updated_at_desc/cursor` | `SCAN sprints; USE TEMP B-TREE FOR ORDER BY` → `SEARCH sprints USING INDEX idx_sprints_page_updated_at_desc (<expr><?)` | Limit → Sort → Seq Scan → Limit → Sort → Bitmap Heap Scan → BitmapOr → Bitmap Index Scan idx_sprints_page_updated_at_desc → Bitmap Index Scan idx_sprints_page_updated_at_desc |
| `sprints/created_at_asc/cursor` | `SCAN sprints; USE TEMP B-TREE FOR ORDER BY` → `SEARCH sprints USING INDEX idx_sprints_page_created_at_asc (<expr>>?)` | Limit → Sort → Seq Scan → Limit → Index Scan idx_sprints_page_created_at_asc |
| `sprints/created_at_desc/cursor` | `SCAN sprints; USE TEMP B-TREE FOR ORDER BY` → `SEARCH sprints USING INDEX idx_sprints_page_created_at_desc (<expr><?)` | Limit → Sort → Seq Scan → Limit → Sort → Bitmap Heap Scan → BitmapOr → Bitmap Index Scan idx_sprints_page_created_at_desc → Bitmap Index Scan idx_sprints_page_created_at_desc |
| `runs/started_at_asc/cursor` | `SCAN r; USE TEMP B-TREE FOR ORDER BY` → `SEARCH r USING INDEX idx_runs_page_started_at_asc (<expr>>?)` | Limit → Sort → Seq Scan → Limit → Index Scan idx_runs_page_started_at_asc |
| `runs/started_at_desc/cursor` | `SCAN r; USE TEMP B-TREE FOR ORDER BY` → `SEARCH r USING INDEX idx_runs_page_started_at_desc (<expr><?)` | Limit → Sort → Seq Scan → Limit → Index Scan idx_runs_page_started_at_desc |
| `runs/status_asc/cursor` | `SEARCH r USING INDEX idx_runs_status (status>?)` → `SEARCH r USING INDEX idx_runs_status (status>?)` | Limit → Sort → Bitmap Heap Scan → BitmapOr → Bitmap Index Scan idx_runs_status → Bitmap Index Scan idx_runs_status → Limit → Sort → Bitmap Heap Scan → BitmapOr → Bitmap Index Scan idx_runs_page_status_started → Bitmap Index Scan idx_runs_status |
| `runs/status_desc/cursor` | `SEARCH r USING INDEX idx_runs_status (status<?); USE TEMP B-TREE FOR LAST TERM OF ORDER BY` → `SEARCH r USING INDEX idx_runs_page_status_desc (status<?)` | Limit → Incremental Sort → Index Scan idx_runs_status → Limit → Index Scan idx_runs_page_status_desc |
| `runs/duration_asc/cursor` | `SCAN r; USE TEMP B-TREE FOR ORDER BY` → `SEARCH r USING INDEX idx_runs_page_duration_asc (<expr>>?)` | Limit → Sort → Seq Scan → Limit → Index Scan idx_runs_page_duration_asc |
| `runs/duration_desc/cursor` | `SCAN r; USE TEMP B-TREE FOR ORDER BY` → `SEARCH r USING INDEX idx_runs_page_duration_desc (<expr><?)` | Limit → Sort → Seq Scan → Limit → Index Scan idx_runs_page_duration_desc |
| `runs/cost_asc/cursor` | `SCAN r; USE TEMP B-TREE FOR ORDER BY` → `SEARCH r USING INDEX idx_runs_page_cost_asc (<expr>>?)` | Limit → Sort → Seq Scan → Limit → Index Scan idx_runs_page_cost_asc |
| `runs/cost_desc/cursor` | `SCAN r; USE TEMP B-TREE FOR ORDER BY` → `SEARCH r USING INDEX idx_runs_page_cost_desc (<expr><?)` | Limit → Sort → Seq Scan → Limit → Index Scan idx_runs_page_cost_desc |
| `tasks/status_priority/cursor` | `SEARCH tasks USING INDEX idx_tasks_priority (priority>?); USE TEMP B-TREE FOR LAST TERM OF ORDER BY` → `SEARCH tasks USING INDEX idx_tasks_page_status_priority (status=? AND priority>?)` | Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_tasks_status → Limit → Index Scan idx_tasks_page_status_priority |
| `tasks/project_id_priority/cursor` | `SEARCH tasks USING INDEX idx_tasks_project (project_id=?); USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH tasks USING INDEX idx_tasks_page_project_id_priority (project_id=? AND priority>?)` | Limit → Sort → Bitmap Heap Scan → BitmapAnd → Bitmap Index Scan idx_tasks_project → BitmapOr → Bitmap Index Scan idx_tasks_priority → Bitmap Index Scan idx_tasks_priority → Limit → Sort → Bitmap Heap Scan → BitmapOr → Bitmap Index Scan idx_tasks_page_project_id_priority → Bitmap Index Scan idx_tasks_page_project_id_priority |
| `tasks/epic_id_priority/cursor` | `SEARCH tasks USING INDEX idx_tasks_epic (epic_id=?); USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH tasks USING INDEX idx_tasks_page_epic_id_priority (epic_id=? AND priority>?)` | Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_tasks_epic → Limit → Sort → Bitmap Heap Scan → BitmapOr → Bitmap Index Scan idx_tasks_page_epic_id_priority → Bitmap Index Scan idx_tasks_page_epic_id_priority |
| `tasks/sprint_id_priority/cursor` | `SEARCH tasks USING INDEX idx_tasks_sprint (sprint_id=?); USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH tasks USING INDEX idx_tasks_page_sprint_id_priority (sprint_id=? AND priority>?)` | Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_tasks_sprint → Limit → Sort → Bitmap Heap Scan → BitmapOr → Bitmap Index Scan idx_tasks_page_sprint_id_priority → Bitmap Index Scan idx_tasks_page_sprint_id_priority |
| `tasks/kind_priority/cursor` | `SEARCH tasks USING INDEX idx_tasks_priority (priority>?); USE TEMP B-TREE FOR LAST TERM OF ORDER BY` → `SEARCH tasks USING INDEX idx_tasks_page_kind_priority (kind=? AND priority>?)` | Limit → Incremental Sort → Index Scan idx_tasks_priority → Limit → Index Scan idx_tasks_page_kind_priority |
| `tasks/parent_id_priority/cursor` | `SEARCH tasks USING INDEX idx_tasks_parent_id (parent_id=?); USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH tasks USING INDEX idx_tasks_page_parent_id_priority (parent_id=? AND priority>?)` | Limit → Sort → Index Scan idx_tasks_parent_id → Limit → Index Scan idx_tasks_page_parent_id_priority |
| `epics/project_updated/cursor` | `SCAN epics; USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH epics USING INDEX idx_epics_page_project_updated (project_id=? AND <expr><?)` | Limit → Sort → Seq Scan → Limit → Sort → Seq Scan |
| `sprints/project_updated/cursor` | `SCAN sprints; USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH sprints USING INDEX idx_sprints_page_project_updated (project_id=? AND <expr><?)` | Limit → Sort → Seq Scan → Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_sprints_page_project_updated |
| `runs/status_started/cursor` | `SEARCH r USING INDEX idx_runs_status (status=?); USE TEMP B-TREE FOR ORDER BY` → `SEARCH r USING INDEX idx_runs_page_status_started (status=? AND <expr><?)` | Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_runs_status → Limit → Index Scan idx_runs_page_status_started |
| `runs/task_id_started/cursor` | `SEARCH r USING INDEX idx_runs_task (task_id=?); USE TEMP B-TREE FOR ORDER BY` → `SEARCH r USING INDEX idx_runs_page_task_id_started (task_id=? AND <expr><?)` | Limit → Sort → Index Scan idx_runs_task → Limit → Index Scan idx_runs_page_task_id_started |
| `comments/created_asc/cursor` | `SCAN comments; USE TEMP B-TREE FOR ORDER BY` → `SEARCH comments USING INDEX idx_comments_page_created_asc (<expr>>?)` | Limit → Sort → Seq Scan → Limit → Index Scan idx_comments_page_created_asc |
| `comments/entity_created_asc/cursor` | `SEARCH comments USING INDEX idx_comments_entity (entity_type=? AND entity_id=?); USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH comments USING INDEX idx_comments_page_entity_created_asc (entity_type=? AND entity_id=? AND <expr>>?)` | Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_comments_entity → Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_comments_page_entity_created_desc |
| `comments/created_desc/cursor` | `SCAN comments; USE TEMP B-TREE FOR ORDER BY` → `SEARCH comments USING INDEX idx_comments_page_created_desc (<expr><?)` | Limit → Sort → Seq Scan → Limit → Index Scan idx_comments_page_created_desc |
| `comments/entity_created_desc/cursor` | `SEARCH comments USING INDEX idx_comments_entity (entity_type=? AND entity_id=?); USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY` → `SEARCH comments USING INDEX idx_comments_page_entity_created_desc (entity_type=? AND entity_id=? AND <expr><?)` | Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_comments_entity → Limit → Sort → Bitmap Heap Scan → Bitmap Index Scan idx_comments_page_entity_created_desc |

## COUNT evidence

Warm, server-side execution time from EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON),
median of seven samples after one warm-up, after VACUUM ANALYZE. These exclude
transport, cold-cache behavior, write churn and concurrent application load.
The shared host produces measurable variation between runs; these figures are
observations, not latency promises. For the 10x scenario, task/run/project/
epic/sprint fixtures are expanded independently; other fixture tables stay at
baseline size. Total matches exclude pagination.

| Query | Rows matched at baseline / 10x | Median ms at baseline / 10x |
|---|---|---|
| `tasks/all` | 4,330 / 43,300 | 0.446 / 4.706 |
| `tasks/status` | 1,083 / 10,830 | 0.179 / 1.126 |
| `tasks/project` | 101 / 1,010 | 0.093 / 0.165 |
| `tasks/page` | 51 / 51 | 0.101 / 0.153 |
| `runs/all` | 4,330 / 43,300 | 0.462 / 3.932 |
| `runs/status` | 1,083 / 10,830 | 0.161 / 1.013 |
| `runs/project` | 101 / 1,010 | 0.278 / 1.816 |
| `runs/page` | 51 / 51 | 0.090 / 0.225 |
| `projects/all` | 43 / 430 | 0.068 / 0.109 |
| `epics/all` | 200 / 2,000 | 0.093 / 0.267 |
| `sprints/all` | 400 / 4,000 | 0.107 / 0.460 |

The [API contract](api-pagination.md#count-cost-evidence) retains opt-in totals.
