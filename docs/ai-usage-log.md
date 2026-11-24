# AI Usage Log

This log records every non-trivial piece of AI-generated content, how it was verified, and what changed after review. Honesty is the point — this file is evidence of judgment.

---

## 2026-11-25: Phase 4 — Production Habits & Polish

### Generated
- Token-bucket rate limiter with Redis Lua script and in-memory fallback in `internal/ratelimit/limiter.go`.
- Risk rules engine (`amount_cap`, `velocity`) and decision persistence in `internal/risk/rules.go`.
- Zero-dependency Prometheus exposition format collector in `internal/metrics/metrics.go`.
- Integration of risk evaluation and rate limiting middleware into `internal/api/payments.go` and `internal/api/router.go`.
- Admin CLI subcommands (`list-reviews`, `review-payment`, `inspect-risk`, `dashboard`) in `cmd/minipay/admin.go`.
- Phase 4 integration tests in `test/phase4_integration_test.go`.
- ADR 0005: Rate Limiting, Risk Rules Engine, and Operational Observability.

### Verification
- **Unit Tests**:
  - `internal/ratelimit`: verified burst capacity, refill timing, rate limit headers, and 429 response.
  - `internal/risk`: verified rule evaluations for normal, review, and blocked thresholds, plus velocity counter windows.
  - `internal/metrics`: verified Prometheus 0.0.4 text output for counters, gauges, and latency histograms.
- **Integration Tests** (`test/phase4_integration_test.go`):
  - Verified burst rate limit enforcement and 429 status.
  - Verified end-to-end review lifecycle: payment created in `requires_review`, held from automated worker processing, approved via admin action, and settled to `succeeded`.
  - Verified scraping `/metrics` returns 200 with standard metric lines.

### Changes After Review
- Added fail-open fallback in `RedisLimiter` so Redis disconnections degrade to in-memory limiting rather than failing payment creation.
- Initialized `ledger_invariants_violations` metric to 0 at router startup so scrapers immediately observe baseline ledger integrity.

---

## 2026-11-24: Phase 3 — Refunds & Stripe Adapter

### Generated
- Refund queries in `db/queries/payments.sql` (`CreateRefund`, `UpdatePaymentRefund`, `ListRefundsForPayment`, `GetRefundByID`).
- Reversal double-entry ledger posting (`PostRefund`) in `internal/ledger/ledger.go`.
- Refund API endpoints (`POST /v1/payments/{id}/refunds`, `GET /v1/payments/{id}/refunds`) in `internal/api/payments.go`.
- Processor interface and `StripeAdapter` with live-key safety rejection in `internal/processor/stripe.go`.
- Concurrency race tests and partial/full refund integration tests in `test/phase3_integration_test.go`.
- ADR 0004: Refund Accounting, Concurrency Control, and Processor Abstraction.

### Verification
- **Unit Tests**: `TestNewStripeAdapter_RejectsLiveKey` and `TestStripeAdapter_ChargeSuccess`.
- **Integration Tests**:
  - `TestPartialAndFullRefund`: Verified partial and full refund state transitions and ledger balance.
  - `TestConcurrentRefundRaceCondition`: 10 parallel refund requests on $10 balance resulted in exactly 5 successes and 5 rejections with 0 over-refund.

### Changes After Review
- Enforced PostgreSQL `FOR UPDATE` row lock on payments in the refund transaction to serialize concurrent refund requests at the database engine level.

---

## 2026-11-23: Phase 2 — Webhooks

### Generated
- SSRF protection layer: CIDR classification and custom HTTP transport dialer with socket control in `internal/webhooks/ssrf.go`.
- HMAC-SHA256 signature scheme in `internal/webhooks/signer.go`.
- SQL queries and sqlc generated code for `webhook_deliveries` in `db/queries/webhooks.sql` and `internal/platform/db/webhooks.sql.go`.
- Webhook dispatcher with exponential backoff and dead-lettering in `internal/webhooks/dispatcher.go`.
- Merchant webhook configuration endpoints (`PUT /v1/merchants/webhook`, `GET /v1/merchants/webhook`) in `internal/api/merchants.go`.
- ADR 0003: Webhook Reliability, HMAC Signatures, and SSRF Prevention.
- Integration tests in `test/phase2_integration_test.go`.

### Verification
- **SSRF Defense**: Verified via unit tests (`TestIsPrivateIP`, `TestValidateURL`) and integration test `TestMerchantWebhookSSRFRejection` against AWS metadata `169.254.169.254`, loopback, and private CIDRs.
- **HMAC Signatures**: Verified with `TestSignAndVerifyPayload` that tampered payloads and expired timestamps are rejected.
- **End-to-End Delivery**: Verified with `TestWebhookDeliverySuccess` that delivered events match HMAC signatures, headers are populated, and delivery status updates to 200.
- **Retry & Dead-Letter**: Verified with `TestWebhookRetryOnFailure` and `TestWebhookDeadLetterAfterMaxAttempts` that backoff is scheduled and deliveries transition to `dead` upon exhaustion.

### Changes After Review
- Added socket-level IP check inside `net.Dialer.Control` callback to guard against DNS rebinding attacks that could bypass hostname validation.
- Made `SecurityPolicy` configurable via environment variables (`WEBHOOK_ALLOW_PRIVATE`, `WEBHOOK_ALLOW_INSECURE`) so local development and integration tests can communicate with localhost servers while production remains strictly secure.
- Added `X-Delivery-Id` header to every dispatched webhook to provide merchants with end-to-end trace correlation with MiniPay support logs.

---

## 2026-11-23: Phase 1 — Foundation
(Phase 1 entries preserved below)
