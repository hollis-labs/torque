-- 011 subtodos — structural acceptance gating (CW-20260417-0029).
-- Stores a JSON array of checklist items on tasks and task_templates:
--   [{"id":"item-1","text":"...","required":true,"done":false,"evidence":""}, ...]
-- The scheduler deliverable-gate blocks done→review when any required item
-- is unchecked. Auto-populated from "- [ ]" markdown in task.description at
-- create time; also writable via MCP tools.

ALTER TABLE tasks          ADD COLUMN subtodos TEXT;
ALTER TABLE task_templates ADD COLUMN subtodos TEXT;
