-- 033 checkpoint escalated_at — when a pending checkpoint was escalated.
--
-- A pending HITL checkpoint that nobody answers is escalated once, after a
-- TTL from its type or its payload (CW-20260520-0007). The sweep that
-- escalates it sets escalated_at with a conditional UPDATE, so exactly one
-- sweep notifies, however many run. NULL = not escalated. The later
-- timeout (timeout_at → timed_out, task parked → blocked) is unchanged.

ALTER TABLE checkpoints ADD COLUMN escalated_at DATETIME; -- NULL = not escalated
