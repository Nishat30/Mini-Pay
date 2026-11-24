ALTER TABLE idempotency_keys
    ADD COLUMN endpoint TEXT NOT NULL DEFAULT '/v1/payments',
    ADD COLUMN replay_count INT NOT NULL DEFAULT 0,
    ADD COLUMN last_replayed_at TIMESTAMPTZ NULL,
    ADD COLUMN conflict_count INT NOT NULL DEFAULT 0;

CREATE INDEX idx_idempotency_keys_created_at ON idempotency_keys (created_at DESC);

