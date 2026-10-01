-- 035: page order and common cohort indexes (CW-20261001-0570).
-- EXPLAIN evidence: docs/list-index-plans.md. No rows are rewritten.
-- Descending sorts still tie-break on id ASC, so reversing an ASC index
-- cannot serve both directions. Keep expression keys identical to the store.
-- PostgreSQL uses typed timestamps; duration is a fixed completed-run value.

CREATE INDEX IF NOT EXISTS idx_tasks_page_priority_asc ON tasks (priority ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_tasks_page_priority_desc ON tasks (priority DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_tasks_page_status_asc ON tasks (status ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_tasks_page_status_desc ON tasks (status DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_tasks_page_updated_at_asc ON tasks (updated_at ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_tasks_page_updated_at_desc ON tasks (updated_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_tasks_page_created_at_asc ON tasks (created_at ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_tasks_page_created_at_desc ON tasks (created_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_projects_page_name_asc ON projects (name ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_projects_page_name_desc ON projects (name DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_projects_page_status_asc ON projects (status ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_projects_page_status_desc ON projects (status DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_projects_page_updated_at_asc ON projects (updated_at ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_projects_page_updated_at_desc ON projects (updated_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_projects_page_created_at_asc ON projects (created_at ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_projects_page_created_at_desc ON projects (created_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_epics_page_name_asc ON epics (name ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_epics_page_name_desc ON epics (name DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_epics_page_status_asc ON epics (status ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_epics_page_status_desc ON epics (status DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_epics_page_updated_at_asc ON epics (updated_at ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_epics_page_updated_at_desc ON epics (updated_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_epics_page_created_at_asc ON epics (created_at ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_epics_page_created_at_desc ON epics (created_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_sprints_page_name_asc ON sprints (name ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_sprints_page_name_desc ON sprints (name DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_sprints_page_status_asc ON sprints (status ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_sprints_page_status_desc ON sprints (status DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_sprints_page_updated_at_asc ON sprints (updated_at ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_sprints_page_updated_at_desc ON sprints (updated_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_sprints_page_created_at_asc ON sprints (created_at ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_sprints_page_created_at_desc ON sprints (created_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_runs_page_started_at_asc ON runs (started_at ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_runs_page_started_at_desc ON runs (started_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_runs_page_status_asc ON runs (status ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_runs_page_status_desc ON runs (status DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_runs_page_duration_asc ON runs (COALESCE(CAST(ROUND(EXTRACT(EPOCH FROM (ended_at-started_at))*1000) AS BIGINT), -1) ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_runs_page_duration_desc ON runs (COALESCE(CAST(ROUND(EXTRACT(EPOCH FROM (ended_at-started_at))*1000) AS BIGINT), -1) DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_runs_page_cost_asc ON runs (COALESCE(cost, 0) ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_runs_page_cost_desc ON runs (COALESCE(cost, 0) DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_tasks_page_status_priority ON tasks (status, priority ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_tasks_page_project_id_priority ON tasks (project_id, priority ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_tasks_page_epic_id_priority ON tasks (epic_id, priority ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_tasks_page_sprint_id_priority ON tasks (sprint_id, priority ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_tasks_page_kind_priority ON tasks (kind, priority ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_tasks_page_parent_id_priority ON tasks (parent_id, priority ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_epics_page_project_updated ON epics (project_id, updated_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_sprints_page_project_updated ON sprints (project_id, updated_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_runs_page_status_started ON runs (status, started_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_runs_page_task_id_started ON runs (task_id, started_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_comments_page_created_asc ON comments (created_at ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_comments_page_entity_created_asc ON comments (entity_type, entity_id, created_at ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_comments_page_created_desc ON comments (created_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_comments_page_entity_created_desc ON comments (entity_type, entity_id, created_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_artifacts_page_task_created ON artifacts (task_id, created_at ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_sessions_page_created ON sessions (created_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_sessions_page_state_created ON sessions (state, created_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_sessions_page_project_id_created ON sessions (project_id, created_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_sessions_page_task_id_created ON sessions (task_id, created_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_messages_page_to_urn_created ON messages (to_urn, created_at ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_messages_page_thread_id_created ON messages (thread_id, created_at ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_collections_page_created ON collections (created_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_checkpoints_page_task_emitted ON checkpoints (task_id, emitted_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_checkpoints_page_pending_emitted ON checkpoints (status, emitted_at ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_templates_page_id_version ON task_templates (id ASC, version DESC) WHERE is_archived = 0;
CREATE INDEX IF NOT EXISTS idx_tasks_page_collection_position ON tasks (collection_id, (collection_position IS NULL), collection_position ASC, created_at ASC);

-- Canonical run-cost aggregation looks up all ledger rows for each run.
CREATE INDEX IF NOT EXISTS idx_cost_run ON cost_ledger (run_id);
