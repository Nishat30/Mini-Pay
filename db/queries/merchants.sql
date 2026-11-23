-- name: CreateMerchant :one
INSERT INTO merchants (id, name, api_key_hash, webhook_url, webhook_secret, created_at)
VALUES ($1, $2, $3, $4, $5, now())
RETURNING *;

-- name: GetMerchantByAPIKeyHash :one
SELECT * FROM merchants
WHERE api_key_hash = $1;

-- name: GetMerchantByID :one
SELECT * FROM merchants
WHERE id = $1;

-- name: UpdateMerchantWebhook :one
UPDATE merchants
SET webhook_url = $2
WHERE id = $1
RETURNING *;
