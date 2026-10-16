-- 045 task_id_counters (Postgres override; same shape): see the SQLite file.

CREATE TABLE IF NOT EXISTS task_id_counters (
    day      TEXT PRIMARY KEY,
    last_seq BIGINT NOT NULL
);

INSERT INTO task_id_counters (day, last_seq)
SELECT day, MAX(n) FROM (
    SELECT substr(id, 4, 8) AS day, CAST(substr(id, 13) AS BIGINT) AS n
      FROM tasks
     WHERE id ~ '^CW-[0-9]{8}-[0-9]+$'
    UNION ALL
    SELECT substr(task_id, 4, 8), CAST(substr(task_id, 13) AS BIGINT)
      FROM event_outbox
     WHERE task_id ~ '^CW-[0-9]{8}-[0-9]+$'
) seeded GROUP BY day;
