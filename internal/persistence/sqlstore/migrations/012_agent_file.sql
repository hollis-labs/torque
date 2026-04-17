-- 012 agent_file — per-task agent spec pointer (CW-20260417-0082).
-- Stores an absolute or working_dir-relative path to a YAML file that
-- defines the agent persona (system_prompt, optional model override,
-- environment, tools). The scheduler parses the file at dispatch time
-- and combines its system_prompt with profile and task prompts via
-- stacked --append-system-prompt args. File existence is validated
-- at create/update; file contents are read only at dispatch.

ALTER TABLE tasks          ADD COLUMN agent_file TEXT NOT NULL DEFAULT '';
ALTER TABLE task_templates ADD COLUMN agent_file TEXT NOT NULL DEFAULT '';
