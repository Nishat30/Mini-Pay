# Work Log

Newest entries first. Each entry: what was done, why, how verified, what's left.

---

## 2026-11-23: Phase 1 — Foundation

### What
- Designed and applied migration `000002_core_schema.up.sql` covering 11 core tables (`merchants`, `payments`, `payment_events`, `refunds`, `idempotency_keys`, `ledger_accounts`, `ledger_transactions`, `ledger_entries`, `outbox_events`, `webhook_deliveries`, `risk_decisions`).
- Created PostgreSQL deferred constraint trigger `assert_ledger_balanced` and append-only trigger `forbid_mutation` protecting ledger entries, transactions, and payment events.
- Generated typed database access code using sqlc in Docker (`internal/platform/db`).
- Implemented prefixed ULID generation (`internal/platform/ids.go`) and transaction runner `WithTx`.
- Implemented pure payment lifecycle state machine with validation (`internal/payments/state.go`).
- Implemented double-entry ledger domain service and financial invariant checker (`internal/ledger/ledger.go`).
- Implemented single-transaction idempotency key locking and response caching (`internal/idempotency/store.go`).
- Implemented PSP simulator supporting deterministic outcomes (`internal/processor/simulator.go`).
- Implemented background outbox worker (`internal/outbox/worker.go`) with `FOR UPDATE SKIP LOCKED` batching.
- Implemented HTTP API handlers for `POST /v1/payments`, `GET /v1/payments/{id}`, and `GET /v1/payments` with Bearer auth and payload validation.
- Implemented admin CLI subcommands: `minipay admin create-merchant` and `minipay admin check-ledger`.
- Wrote ADR 0002 on Single-Transaction Idempotency.
- Created end-to-end integration tests in `test/phase1_integration_test.go`.

### Why
Phase 1 delivers the core transactional engine of MiniPay: guaranteeing that payments cannot be duplicated under retries, double-entry ledger debits and credits strictly balance, and mutations are atomic across the payment, outbox, and ledger tables.

### Verification
- **Migration**: `go run ./cmd/minipay migrate up` successfully applied migration 2 (`applied: 2`).
- **Unit Tests**:
  - `internal/payments`: Table-driven tests verifying all valid and invalid transitions and terminal states.
  - `internal/ledger`: Tests verifying fee formulas ($10.00 -> $0.59), balanced capture entries, and refund entries.
  - `internal/idempotency`: Tests verifying SHA-256 request hashing and key validation.
  - `internal/platform`: Tests verifying prefixed ULIDs and config safety.
- **Admin CLI**:
  - `go run ./cmd/minipay admin create-merchant --name "Acme Corp"` created merchant with ID `mer_...` and API key `mp_test_...`.
  - `go run ./cmd/minipay admin check-ledger` executed query checks and reported `Ledger integrity check: OK (all invariants hold, 0 violations)`.
- **Integration Tests** (`test/phase1_integration_test.go`):
  - `TestIdempotentReplay`: Initial request returned 201 Created; duplicate request returned 201 Created with `Idempotent-Replayed: true` and identical body.
  - `TestIdempotencyKeyReuseDifferentPayload`: Attempting to reuse an idempotency key with an altered payload rejected with 422 Unprocessable Entity.
  - `TestDuplicateStorm`: 10 concurrent requests with identical keys produced exactly 1 payment record in the database.
  - `TestParallelDistinctKeys`: 10 concurrent requests with distinct keys created 10 distinct payment records.
  - `TestLedgerTriggers`:
    - Direct insert of unbalanced entry rejected by `assert_ledger_balanced` trigger (`ERROR: ledger transaction ... is unbalanced`).
    - Direct update and delete rejected by `forbid_mutation` trigger (`ERROR: table ... rows are append-only and cannot be updated or deleted`).
  - `TestWorkerEndToEnd`: Payment created in `processing`, outbox worker transitioned it to `succeeded`, populated `processor_ref`, created balanced ledger transaction `capture:<id>`, and invariant checker verified 0 violations.

### Issues Resolved
- Wazero JIT/memory crash under Go 1.25 on Windows: solved by invoking sqlc code generation through Docker container (`sqlc/sqlc:latest`).
- Missing metadata constraint in test: populated default empty JSON object.
- Worker event filtering: constrained outbox claim query to `type = 'payment.process'` so payments worker does not consume webhook events.

### What's Left
- Phase 2: Webhooks (dispatcher, retry schedule, exponential backoff, HMAC-SHA256 signature, SSRF protection).

---

## 2026-11-03: Phase 0 — Bootstrap
(Phase 0 entries preserved below)
