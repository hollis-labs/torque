-- The portfolio planner POC has moved to Tachyon. Remove its data and schema.
-- Execution plans, tasks, and task judgments remain owned by Torque.
DROP TABLE IF EXISTS planner_comments;
DROP TABLE IF EXISTS planner_links;
DROP TABLE IF EXISTS planner_priorities;
DROP TABLE IF EXISTS planner_workstreams;
DROP TABLE IF EXISTS planner_roadmap;
DROP TABLE IF EXISTS planner_ideas;
DROP TABLE IF EXISTS planner_decisions;
DROP TABLE IF EXISTS planner_risks;
DROP TABLE IF EXISTS planner_inbox;
DELETE FROM settings WHERE key = 'features.planner';
DELETE FROM judgments WHERE subject_type LIKE 'planner:%';
