-- name: InsertRiskDecision :one
INSERT INTO risk_decisions (payment_id, rule, outcome, detail, created_at)
VALUES ($1, $2, $3, $4, now())
RETURNING *;

-- name: ListRiskDecisionsForPayment :many
SELECT * FROM risk_decisions
WHERE payment_id = $1
ORDER BY created_at ASC;

