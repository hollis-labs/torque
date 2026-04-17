-- 010 task_templates.working_dir — captures the decision from
-- CW-20260416-0003 (pick A: add typed working_dir column to templates
-- rather than keeping the field metadata-only).
--
-- Nullable; existing rows keep NULL. Templates that care about
-- working_dir set it explicitly; Instantiate copies the value (with
-- {{var}} resolution) onto the resulting task's typed WorkingDir
-- column rather than routing through metadata_template.

ALTER TABLE task_templates ADD COLUMN working_dir TEXT;
