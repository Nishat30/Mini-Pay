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

-- name: CreateRefund :one
INSERT INTO refunds (id, payment_id, amount, created_at)
VALUES ($1, $2, $3, now())
RETURNING *;

-- name: UpdatePaymentRefund :one
UPDATE payments
SET amount_refunded = amount_refunded + @refund_amount,
    status = CASE WHEN amount_refunded + @refund_amount = amount THEN 'refunded' ELSE status END,
    version = version + 1,
    updated_at = now()
WHERE id = @id AND merchant_id = @merchant_id
RETURNING *;

-- name: ListRefundsForPayment :many
SELECT * FROM refunds
WHERE payment_id = $1
ORDER BY created_at ASC;

-- name: GetRefundByID :one
SELECT * FROM refunds
WHERE id = $1;

-- name: ListPaymentsByStatus :many
SELECT * FROM payments
WHERE status = $1
ORDER BY created_at ASC;

-- name: GetPaymentByIDAdmin :one
SELECT * FROM payments
WHERE id = $1;

