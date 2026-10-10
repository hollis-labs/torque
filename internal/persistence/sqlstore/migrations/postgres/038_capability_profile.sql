-- 038 add role, tier, capability_profile to tasks; profile_snapshot to runs.
-- Postgres override: explicit IF NOT EXISTS to match 036 patterns.

ALTER TABLE tasks ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS tier TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS capability_profile TEXT;

ALTER TABLE task_templates ADD COLUMN IF NOT EXISTS role TEXT;
ALTER TABLE task_templates ADD COLUMN IF NOT EXISTS tier TEXT;
ALTER TABLE task_templates ADD COLUMN IF NOT EXISTS capability_profile TEXT;

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS tier TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS capability_profile TEXT;

ALTER TABLE runs ADD COLUMN IF NOT EXISTS profile_snapshot TEXT;
