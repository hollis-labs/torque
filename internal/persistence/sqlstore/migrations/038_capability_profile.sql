-- 038 add role, tier, capability_profile to tasks; profile_snapshot to runs.
--
-- Torque classifies work independently of the low-level agent_profile.
-- capability_profile is a JSON block holding task_class, difficulty, ambiguity, modality, autonomy, budget_class.
-- profile_snapshot stores the resolved launch profile at the moment a run starts.

ALTER TABLE tasks ADD COLUMN role TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN tier TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN capability_profile TEXT;

ALTER TABLE task_templates ADD COLUMN role TEXT;
ALTER TABLE task_templates ADD COLUMN tier TEXT;
ALTER TABLE task_templates ADD COLUMN capability_profile TEXT;

ALTER TABLE sessions ADD COLUMN role TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN tier TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN capability_profile TEXT;

ALTER TABLE runs ADD COLUMN profile_snapshot TEXT;
