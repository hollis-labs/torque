-- 026 add launch_profile column to tasks/task_templates/sessions.
--
-- launch_profile is Torque's first-class launch-family selector
-- (internal/launchprofile). It replaces agent_profile as the user-facing
-- entry point — tasks/templates/session-launch requests can now carry
-- launch_profile, and the boot path resolves the underlying agent_profile
-- registry key from it. agent_profile stays on these tables as the legacy
-- compatibility surface (still read, still writeable) — when launch_profile
-- is empty the resolver maps the legacy value to a builtin family or passes
-- it through verbatim.
--
-- All three columns default to empty (no backfill needed). Existing rows
-- continue to boot through the legacy-compat path until they are explicitly
-- migrated to launch_profile.

ALTER TABLE tasks
    ADD COLUMN launch_profile TEXT NOT NULL DEFAULT '';

ALTER TABLE task_templates
    ADD COLUMN launch_profile TEXT;

ALTER TABLE sessions
    ADD COLUMN launch_profile TEXT NOT NULL DEFAULT '';
