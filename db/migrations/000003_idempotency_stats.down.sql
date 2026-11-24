DROP INDEX IF EXISTS idx_idempotency_keys_created_at;

ALTER TABLE idempotency_keys
    DROP COLUMN IF EXISTS endpoint,
    DROP COLUMN IF EXISTS replay_count,
    DROP COLUMN IF EXISTS last_replayed_at,
    DROP COLUMN IF EXISTS conflict_count;

