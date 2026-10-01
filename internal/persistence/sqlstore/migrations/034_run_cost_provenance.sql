-- 034 run cost provenance — cache tokens and where a run's cost came from.
--
-- A run's cost is now resolved once, at completion, and written to both
-- runs.cost and its cost_ledger row in the same transaction
-- (CW-20260912-0003). runs gains the run's cache-token totals and the
-- cost's provenance: 'provider' (the CLI reported it), 'estimate' (priced
-- from the model catalog, cache-aware), 'mixed' (some of each) or 'none'.
-- cost_ledger gains the same cache totals and the provider/estimate split
-- of `cost`. Rows from before this migration keep their values: runs get
-- cost_source '' (not recorded), and ledger rows keep 'executor',
-- 'models_dev' or 'unknown' with a zero split.

ALTER TABLE runs ADD COLUMN cache_read_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN cache_write_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN cost_source TEXT NOT NULL DEFAULT '';

ALTER TABLE cost_ledger ADD COLUMN cache_read_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE cost_ledger ADD COLUMN cache_write_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE cost_ledger ADD COLUMN provider_cost REAL NOT NULL DEFAULT 0;
ALTER TABLE cost_ledger ADD COLUMN estimated_cost REAL NOT NULL DEFAULT 0;
