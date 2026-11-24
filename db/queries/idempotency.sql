-- name: InsertIdempotencyKey :one
INSERT INTO idempotency_keys (merchant_id, key, request_hash, endpoint, state, response_status, response_body, locked_at, created_at, expires_at)
VALUES ($1, $2, $3, COALESCE(sqlc.narg('endpoint')::text, '/v1/payments'), 'started', 0, '{}'::jsonb, now(), now(), now() + interval '24 hours')
ON CONFLICT (merchant_id, key) DO UPDATE
SET request_hash = EXCLUDED.request_hash,
    endpoint = EXCLUDED.endpoint,
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

-- name: IncrementIdempotencyReplay :exec
UPDATE idempotency_keys
SET replay_count = replay_count + 1,
    last_replayed_at = now()
WHERE merchant_id = $1 AND key = $2;

-- name: IncrementIdempotencyConflict :exec
UPDATE idempotency_keys
SET conflict_count = conflict_count + 1
WHERE merchant_id = $1 AND key = $2;

-- name: ListRecentIdempotencyKeys :many
SELECT merchant_id, key, endpoint, state, replay_count, conflict_count, last_replayed_at, created_at, expires_at
FROM idempotency_keys
ORDER BY created_at DESC
LIMIT $1;

-- name: GetIdempotencyStats :one
SELECT
    COUNT(*)::bigint AS total_keys,
    COALESCE(SUM(replay_count), 0)::bigint AS total_replays,
    COALESCE(SUM(conflict_count), 0)::bigint AS total_conflicts,
    COUNT(*) FILTER (WHERE state = 'started' AND locked_at IS NOT NULL AND locked_at > now() - interval '60 seconds')::bigint AS in_flight_count
FROM idempotency_keys;

-- name: CheckDuplicatePayments :one
SELECT
    COALESCE(SUM(replay_count), 0)::bigint AS total_retries,
    (
        SELECT COUNT(*)
        FROM (
            SELECT response_body->>'id' AS pay_id
            FROM idempotency_keys
            WHERE state = 'completed' AND response_body ? 'id'
            GROUP BY response_body->>'id'
            HAVING count(*) > 1
        ) d
    )::bigint AS duplicate_payments
FROM idempotency_keys;

