-- 047 (Postgres override; same shape) project execution defaults (CW-20261030-0208): JSON object of task defaults
-- (executor, launch_profile, agent_profile, on_done, max_retries, quality_gates)
-- applied to tasks created in the project when neither the task nor its template sets them.
ALTER TABLE projects ADD COLUMN execution_defaults TEXT;
