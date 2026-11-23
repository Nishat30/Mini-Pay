-- name: InsertIdempotencyKey :one
INSERT INTO idempotency_keys (merchant_id, key, request_hash, state, response_status, response_body, locked_at, created_at, expires_at)
VALUES ($1, $2, $3, 'started', 0, '{}'::jsonb, now(), now(), now() + interval '24 hours')
ON CONFLICT (merchant_id, key) DO UPDATE
SET request_hash = EXCLUDED.request_hash,
    state = 'started',
    locked_at = now(),
    expires_at = now() + interval '24 hours'
WHERE idempotency_keys.expires_at < now()
RETURNING *;

-- name: GetIdempotencyKey :one
SELECT * FROM idempotency_keys
WHERE merchant_id = $1 AND key = $2;

-- name: UpdateIdempotencyKeyCompleted :exec
UPDATE idempotency_keys
SET state = 'completed',
    response_status = $3,
    response_body = $4,
    locked_at = NULL
WHERE merchant_id = $1 AND key = $2;
