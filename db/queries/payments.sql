-- name: CreatePayment :one
INSERT INTO payments (id, merchant_id, amount, amount_refunded, currency, status, processor, processor_ref, failure_code, metadata, version, created_at, updated_at)
VALUES ($1, $2, $3, 0, $4, $5, $6, $7, $8, $9, 1, now(), now())
RETURNING *;

-- name: GetPaymentByID :one
SELECT * FROM payments
WHERE id = $1 AND merchant_id = $2;

-- name: GetPaymentByIDForUpdate :one
SELECT * FROM payments
WHERE id = $1 AND merchant_id = $2
FOR UPDATE;

-- name: ListPayments :many
SELECT * FROM payments
WHERE merchant_id = $1
  AND (sqlc.narg('starting_after_id')::text IS NULL OR id < sqlc.narg('starting_after_id'))
ORDER BY created_at DESC, id DESC
LIMIT $2;

-- name: UpdatePaymentStatus :one
UPDATE payments
SET status = @to_status,
    processor_ref = COALESCE(@processor_ref, processor_ref),
    failure_code = COALESCE(@failure_code, failure_code),
    version = version + 1,
    updated_at = now()
WHERE id = @id AND status = @from_status
RETURNING *;

-- name: InsertPaymentEvent :one
INSERT INTO payment_events (payment_id, from_status, to_status, reason, created_at)
VALUES ($1, $2, $3, $4, now())
RETURNING *;
