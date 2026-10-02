-- Duration expression indexes from 035 are not SQLite-version-portable:
-- julianday/ROUND floating-point results can differ between SQLite builds,
-- causing integrity_check failures when another build reads the same database.
-- Rule: no date/time or floating-point functions in SQLite expression indexes.
-- Keep duration ORDER BY/cursor expressions unchanged; compute them unindexed.
DROP INDEX IF EXISTS idx_runs_page_duration_asc;
DROP INDEX IF EXISTS idx_runs_page_duration_desc;
