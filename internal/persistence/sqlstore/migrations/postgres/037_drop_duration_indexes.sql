-- Match SQLite's removal of the duration indexes from 035. PostgreSQL enforces
-- immutable index expressions, but we remove this index family on both stores
-- for symmetry. The runner requires a PostgreSQL override for each version.
-- SQLite rule: no date/time or floating-point functions in expression indexes;
-- results can differ between SQLite builds. Duration queries stay unchanged.
DROP INDEX IF EXISTS idx_runs_page_duration_asc;
DROP INDEX IF EXISTS idx_runs_page_duration_desc;
