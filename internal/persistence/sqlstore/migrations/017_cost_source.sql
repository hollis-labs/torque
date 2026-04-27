-- 017 cost_source — track whether a cost_ledger entry's `cost` came from
-- the executor stream (`cost_usd` field on the result envelope) or was
-- computed from token counts via models.dev pricing. Lets the UI show
-- "measured" vs "estimated" cost and gives ops a foothold for auditing
-- accuracy. Existing rows are 'unknown' since we can't recover the
-- original source post-hoc.
ALTER TABLE cost_ledger
    ADD COLUMN cost_source TEXT NOT NULL DEFAULT 'unknown';
