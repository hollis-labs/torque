-- 045 task_id_counters: task ids are never handed out twice (CW-20261016-0251).
--
-- NextTaskID used to derive the next id from the current MAX(id) of the day, so
-- deleting the newest task and creating another handed out the same
-- CW-YYYYMMDD-NNNN. That was harmless until event_outbox started keeping
-- durable history keyed by task_id (180-day retention): a reused id merges two
-- unrelated tasks' histories for any consumer replaying by task.
--
-- One row per UTC day holds the last sequence number issued. The allocator uses
-- max(counter, live MAX) + 1 and writes the result back in the same
-- transaction, so the counter never goes backwards. The seed below covers every
-- task id that exists now and every task id the outbox has ever recorded, so an
-- id that was handed out before this migration is not reissued either.
--
-- Additive: no down migration. Rollback is a code revert; the unused table is
-- inert.

CREATE TABLE IF NOT EXISTS task_id_counters (
    day      TEXT PRIMARY KEY,
    last_seq INTEGER NOT NULL
);

INSERT INTO task_id_counters (day, last_seq)
SELECT day, MAX(n) FROM (
    SELECT substr(id, 4, 8) AS day, CAST(substr(id, 13) AS INTEGER) AS n
      FROM tasks
     WHERE id GLOB 'CW-[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-[0-9]*'
    UNION ALL
    SELECT substr(task_id, 4, 8), CAST(substr(task_id, 13) AS INTEGER)
      FROM event_outbox
     WHERE task_id GLOB 'CW-[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-[0-9]*'
) GROUP BY day;
