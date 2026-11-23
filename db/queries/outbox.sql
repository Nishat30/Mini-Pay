-- name: CreateOutboxEvent :one
INSERT INTO outbox_events (merchant_id, aggregate_id, type, payload, created_at, available_at)
VALUES ($1, $2, $3, $4, now(), now())
RETURNING *;

-- name: ClaimOutboxEvents :many
UPDATE outbox_events
SET available_at = now() + interval '60 seconds', attempts = attempts + 1
WHERE id IN (
  SELECT sub.id FROM outbox_events sub
  WHERE sub.processed_at IS NULL AND sub.available_at <= now() AND sub.type = $2
  ORDER BY sub.id LIMIT $1
  FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: MarkOutboxProcessed :exec
UPDATE outbox_events
SET processed_at = now()
WHERE id = $1;

-- name: ScheduleOutboxRetry :exec
UPDATE outbox_events
SET available_at = $2
WHERE id = $1;
