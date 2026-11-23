# AI Usage Log

This log records every non-trivial piece of AI-generated content, how it was verified, and what changed after review. Honesty is the point — this file is evidence of judgment.

---

## 2026-11-23: Phase 1 — Foundation

### Generated
- Database migrations: `000002_core_schema.up.sql` and `down.sql` with deferred constraint trigger `assert_ledger_balanced` and immutability trigger `forbid_mutation`.
- sqlc queries for merchants, payments, idempotency, ledger, and outbox.
- Idempotency layer: single-transaction request hashing, locking, and replay mechanism.
- State machine: pure transition function and validation.
- Double-entry ledger: fee calculations, capture/refund entry generators, and invariant checking.
- PSP simulator and outbox worker.
- HTTP API handlers: POST /v1/payments, GET /v1/payments/{id}, GET /v1/payments.
- Admin commands: create-merchant, check-ledger.
- ADR 0002 on Single-Transaction Idempotency.
- End-to-end integration tests in `test/phase1_integration_test.go`.

### Verification
- **PostgreSQL Triggers**: Verified with live tests in `TestLedgerTriggers` that an unbalanced debit/credit insert triggers a database constraint exception (`P0001`), and mutating append-only tables fails with `forbid_mutation`.
- **Idempotency & Replay**: Verified with `TestIdempotentReplay` (response headers and body match byte-for-byte), `TestIdempotencyKeyReuseDifferentPayload` (422 response), and `TestDuplicateStorm` (10 concurrent requests yield exactly 1 payment record).
- **Ledger Invariants**: Verified with `TestWorkerEndToEnd` and `minipay admin check-ledger` that all debits equal credits, currencies match, and zero violations occur.
- **Unit Tests**: Full suite passed with `-v` across all domain packages.

### Changes After Review
- Added `type = $2` filtering to `ClaimOutboxEvents` in `db/queries/outbox.sql` so payment workers only claim `payment.process` events and don't mistakenly consume webhook notification events.
- Fixed `GetAccount` query in `db/queries/ledger.sql` to properly handle nullable `merchant_id` for system-level accounts (`processor_clearing` and `platform_fees`).
- Adjusted `CreatePaymentParams` in integration test to ensure non-null `metadata` JSON default (`{}`).
- Fixed PostgreSQL connection credentials in integration test setup from `minipay_dev_pass` to the development standard `minipay`.

---

## 2026-11-03: Phase 0 Scaffold

### Generated
- Project structure and all bootstrap files (config, logging, router, health checks, middleware, migrations, Makefile, CI, ADR, docs).

### Verification
- **Config**: Unit tests verify sk_live_ rejection, default values, and DSN redaction.
- **Health endpoints**: Manual curl test against running server.
- **Makefile**: Verified each target runs without error.
- **CI YAML**: Validated syntax matches GitHub Actions schema.
- **ADR**: Reviewed against current Go, pgx, sqlc, and Redis documentation.

### Changes After Review
- Anchored root binary ignore pattern in `.gitignore` (`/minipay`) after discovering that bare `minipay` caused Git to ignore `cmd/minipay/`.
- Adjusted `.github/workflows/ci.yml` to compile `golangci-lint` with the runner's Go compiler and run `go mod download` prior to `go build` to ensure clean builds in CI.
- Handled unhandled errors in `internal/api/health.go` (`_ = json.NewEncoder...`) to satisfy `errcheck`.
