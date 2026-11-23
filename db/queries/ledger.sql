-- name: CreateAccountLazy :one
INSERT INTO ledger_accounts (id, merchant_id, kind, currency)
VALUES ($1, $2, $3, $4)
ON CONFLICT (merchant_id, kind, currency) DO NOTHING
RETURNING *;

-- name: GetAccount :one
SELECT * FROM ledger_accounts
WHERE (merchant_id = $1 OR (merchant_id IS NULL AND $1::varchar IS NULL))
  AND kind = $2
  AND currency = $3;

-- name: CreateLedgerTransaction :one
INSERT INTO ledger_transactions (id, payment_id, kind, reference, created_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (reference) DO NOTHING
RETURNING *;

-- name: GetLedgerTransactionByRef :one
SELECT * FROM ledger_transactions
WHERE reference = $1;

-- name: CreateLedgerEntry :one
INSERT INTO ledger_entries (transaction_id, account_id, direction, amount, currency)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetAccountBalance :one
SELECT COALESCE(SUM(CASE direction WHEN 'credit' THEN amount ELSE -amount END), 0)::bigint AS balance
FROM ledger_entries
WHERE account_id = $1;

-- name: CheckUnbalancedTransactions :many
SELECT transaction_id,
       COALESCE(SUM(CASE direction WHEN 'debit' THEN amount ELSE -amount END), 0)::bigint AS diff
FROM ledger_entries
GROUP BY transaction_id
HAVING COALESCE(SUM(CASE direction WHEN 'debit' THEN amount ELSE -amount END), 0) <> 0;

-- name: CheckCurrencyMismatchEntries :many
SELECT e.id, e.transaction_id, e.currency AS entry_currency, a.currency AS account_currency
FROM ledger_entries e
JOIN ledger_accounts a ON e.account_id = a.id
WHERE e.currency <> a.currency;
