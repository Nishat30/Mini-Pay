-- name: CreateWebhookDelivery :one
INSERT INTO webhook_deliveries (id, merchant_id, event_id, state, attempts, next_attempt_at, created_at)
VALUES ($1, $2, $3, 'pending', 0, now(), now())
ON CONFLICT (merchant_id, event_id) DO UPDATE
SET merchant_id = EXCLUDED.merchant_id
RETURNING *;

-- name: GetWebhookDeliveryByEvent :one
SELECT * FROM webhook_deliveries
WHERE merchant_id = $1 AND event_id = $2;

-- name: GetWebhookDelivery :one
SELECT * FROM webhook_deliveries
WHERE id = $1;

-- name: ClaimPendingDeliveries :many
UPDATE webhook_deliveries
SET next_attempt_at = now() + interval '60 seconds', attempts = attempts + 1
WHERE id IN (
  SELECT sub.id FROM webhook_deliveries sub
  WHERE sub.state = 'pending' AND sub.next_attempt_at <= now()
  ORDER BY sub.next_attempt_at ASC, sub.id ASC LIMIT $1
  FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: UpdateDeliverySuccess :exec
UPDATE webhook_deliveries
SET state = 'succeeded',
    last_status = $2,
    last_error = NULL
WHERE id = $1;

-- name: UpdateDeliveryFailure :exec
UPDATE webhook_deliveries
SET state = $2,
    next_attempt_at = $3,
    last_status = $4,
    last_error = $5
WHERE id = $1;

-- name: ListRecentWebhookDeliveries :many
SELECT * FROM webhook_deliveries
ORDER BY created_at DESC
LIMIT $1;

