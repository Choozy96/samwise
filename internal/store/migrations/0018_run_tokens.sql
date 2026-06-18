-- Track token usage per run by type instead of relying on a derived dollar cost
-- (cost-per-token varies by model and changes over time; raw tokens are portable).
-- cost_usd is kept (it's reported for free by the runtime) but no longer the
-- headline metric.
ALTER TABLE runs ADD COLUMN input_tokens          INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN output_tokens         INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN cache_creation_tokens INTEGER NOT NULL DEFAULT 0;  -- cache write
ALTER TABLE runs ADD COLUMN cache_read_tokens     INTEGER NOT NULL DEFAULT 0;  -- cache read
