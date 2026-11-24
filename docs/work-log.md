# Work Log

Newest entries first. Each entry: what was done, why, how verified, what's left.

---

## 2026-11-25: Idempotency Visibility & Retry Storm Simulator

### What
- Added database migration `000003_idempotency_stats.up.sql` adding `endpoint`, `replay_count`, `last_replayed_at`, `conflict_count` columns to `idempotency_keys` with an index on `created_at`.
- Instrumented `internal/idempotency/store.go` to atomically increment `replay_count` and `last_replayed_at` on cache replays, increment `conflict_count` on payload mismatches, and re-check completed state during concurrent race completions.
- Exposed Prometheus counters `idempotency_replays_total` and `idempotency_conflicts_total` on `GET /metrics`.
- Added sqlc queries for `ListRecentIdempotencyKeys`, `GetIdempotencyStats`, `CheckDuplicatePayments`, `ListRecentPaymentsAdmin`, and `ListRecentWebhookDeliveries`.
- Completely revamped admin dashboard (`minipay admin dashboard -port 8081`):
  - Summary cards: Total keys, Total replays served, Conflicts blocked, In-flight keys, Ledger integrity status.
  - Live data guarantee card: "N retries -> 0 duplicate payments" computed live from payment records.
  - Recent Idempotency Keys table with key truncation, endpoint, state badge, replay count, conflict count, and timestamps.
  - Interactive "Simulate 20x Concurrent Retry Storm" and "Simulate Payload Conflict" simulator refusing live Stripe keys and proving exactly 1 payment + 19 replays.
  - Webhook deliveries and recent payments tables.
- Added comprehensive unit and integration tests in `test/idempotency_admin_integration_test.go` and `internal/idempotency/store_test.go`.

### Verification
- `TestIdempotencyRetryStormAndDashboard`: 20 concurrent requests with identical idempotency key produced exactly 1 payment created, exactly 19 replays served, all with identical payment IDs, exactly 0 duplicate payments, and 0 ledger invariant violations.
- `golangci-lint run --timeout=5m ./...`: 0 lint issues.
- `go test -v ./...`: 100% test suite passed.

---

## 2026-11-25: Phase 4 — Production Habits & Polish

### What
- Implemented atomic token-bucket rate limiter in `internal/ratelimit/limiter.go` using Redis Lua script with automatic in-memory fallback, setting `X-RateLimit-*` headers and returning 429 Too Many Requests upon throttle.
- Implemented multi-tier risk rules engine in `internal/risk/rules.go` (`amount_cap`, `velocity`, `risk_decisions` audit trail recording).
- Wired risk engine into `internal/api/payments.go` (`POST /v1/payments` flags review or blocks fraud; `GET /v1/payments/{id}/risk` exposes decision audit log).
- Built zero-dependency Prometheus exposition collector in `internal/metrics/metrics.go` exposing `/metrics` with latency histograms, request counters, payment counts, and ledger violation gauge.
- Built administrative CLI operations in `cmd/minipay/admin.go` (`list-reviews`, `review-payment` approval flow, `inspect-risk`, and authenticated web `dashboard`).
- Wrote integration test suite `test/phase4_integration_test.go` verifying rate limiting throttling, risk amount cap / review / block flow, admin review processing, and Prometheus metric scrapes.
- Recorded ADR 0005 for Rate Limiting, Risk Rules Engine, and Observability.

### Why
Phase 4 hardens MiniPay for real-world production multi-tenancy: guarding infrastructure against denial-of-service, intercepting fraudulent / excessive charges before PSP processing, exposing operational metrics, and giving operators tools to manage exceptions.

### Verification
- **Unit Tests**:
  - `internal/ratelimit`: verified token burst capacity, elapsed refill calculation, HTTP headers, and 429 response.
  - `internal/risk`: verified allow, review, and block outcomes for amount thresholds, velocity window counting, and aggregate decision composition.
  - `internal/metrics`: verified counter, gauge, and histogram exposition syntax matching Prometheus 0.0.4 text format.
- **Integration Tests** (`test/phase4_integration_test.go`):
  - `TestRateLimitingHeadersAndThrottling`: verified rate limit headers and 429 throttling under burst traffic.
  - `TestRiskEngineAmountCapAndReviews`: verified full review lifecycle (charge creation in `requires_review`, rejection of automated worker charging, admin approval transition to `processing`, and final worker settlement to `succeeded`).
  - `TestPrometheusMetricsScrape`: verified `GET /metrics` HTTP 200, Content-Type, and presence of all core metrics.

### What's Left
- All core phases (0, 1, 2, 3, 4) complete and verified!

---

## 2026-11-24: Phase 3 — Refunds & Stripe Adapter

### What
- Implemented refund state machine and endpoints (`POST /v1/payments/{id}/refunds` and `GET /v1/payments/{id}/refunds`) in `internal/api/payments.go`.
- Added PostgreSQL `FOR UPDATE` row lock and database check constraint `CHECK (amount_refunded BETWEEN 0 AND amount)` preventing over-refunding.
- Implemented double-entry ledger refund accounting in `internal/ledger/ledger.go` (`PostRefund`: Debit Merchant Payable, Credit Processor Clearing) satisfying `assert_ledger_balanced` and `forbid_mutation` triggers.
- Implemented `processor.Processor` abstraction and `processor.StripeAdapter` with live-key safety rejection and testmode REST client.
- Wrote ADR 0004 for Refund Accounting and Concurrency Control.
- Created integration race test in `test/phase3_integration_test.go`.

### Why
Phase 3 enables partial and full reversals while guaranteeing ledger balancing, auditability, and zero possibility of concurrent race over-refunds.

### Verification
- `TestPartialAndFullRefund`: Verified partial refund ($40) keeps payment succeeded, subsequent refund ($60) transitions to `refunded`, and ledger remains balanced with 0 violations.
- `TestConcurrentRefundRaceCondition`: 10 parallel $2 refund requests against a $10 payment resulted in exactly 5 successes ($10 total refunded) and 5 rejections with 0 over-refund.

---

## 2026-11-23: Phase 2 — Webhooks

### What
- Implemented comprehensive SSRF defense in `internal/webhooks/ssrf.go` with 15+ prohibited CIDR blocks (RFC 1918, loopback, CGNAT, link-local, AWS/cloud metadata `169.254.169.254`, IPv6 ULA) and socket control callbacks preventing DNS rebinding.
- Implemented HMAC-SHA256 signature generator and verification in `internal/webhooks/signer.go` with `MiniPay-Signature: t=<timestamp>,v1=<signature>` and 5-minute replay tolerance window.
- Created `db/queries/webhooks.sql` and generated typed queries via sqlc for `webhook_deliveries` (`CreateWebhookDelivery`, `ClaimPendingDeliveries`, `UpdateDeliverySuccess`, `UpdateDeliveryFailure`, `GetWebhookDelivery`).
- Implemented asynchronous webhook dispatcher in `internal/webhooks/dispatcher.go` with exponential retry backoff ($10 \times 2^{\text{attempts}-1}$ seconds) and dead-letter archiving after 5 failed attempts.
- Implemented merchant webhook management endpoints `PUT /v1/merchants/webhook` and `GET /v1/merchants/webhook` in `internal/api/merchants.go` protected by SSRF validation.
- Wired both payment outbox worker and webhook dispatcher into `cmd/minipay/worker.go` with coordinated graceful shutdown.
- Wrote ADR 0003 documenting Webhook Reliability, HMAC Signatures, and SSRF Prevention.
- Created unit tests and end-to-end integration tests in `internal/webhooks/` and `test/phase2_integration_test.go`.

### Why
Phase 2 ensures merchants receive guaranteed, tamper-proof, asynchronous notifications when payments succeed or fail, while protecting MiniPay's internal infrastructure from SSRF exploitation.

### Verification
- **Unit Tests**:
  - `TestIsPrivateIP`: Verified accurate classification of private, loopback, metadata, and public IPs across IPv4 and IPv6.
  - `TestValidateURL`: Verified strict HTTPS enforcement, user credential stripping, and direct IP validation.
  - `TestSignAndVerifyPayload`: Verified HMAC-SHA256 signature calculation, tampering detection, wrong secret rejection, and replay tolerance expiry.
- **Integration Tests** (`test/phase2_integration_test.go`):
  - `TestWebhookDeliverySuccess`: End-to-end test where a payment was created, processed by the payment worker, claimed by the webhook dispatcher, delivered to an HTTP receiver with valid HMAC signature and delivery ID headers, and marked succeeded in `webhook_deliveries`.
  - `TestWebhookRetryOnFailure`: Verified that 500 status from the receiver keeps delivery in `pending`, records status 500, and schedules next attempt with exponential backoff.
  - `TestWebhookDeadLetterAfterMaxAttempts`: Verified that after reaching max attempts, delivery state transitions to `dead` and outbox event is archived.
  - `TestMerchantWebhookSSRFRejection`: Verified that attempts to configure internal IP targets (`169.254.169.254`, `127.0.0.1`, `192.168.1.10`, insecure HTTP) are rejected with `400 Bad Request`.

### Issues Resolved
- Filtered outbox query to `ClaimOutboxEventsForWebhooks` for events where `type != 'payment.process'`, preventing race conditions with payment processing.
- Configured dev security policy (`AllowPrivate`, `AllowInsecure`) for local integration test servers while keeping strict HTTPS and public IP enforcement default for production.

### What's Left
- Phase 3: Refunds and Stripe Adapter (partial/full refunds, reverse ledger entries, processor interface with Stripe testmode API).

---

## 2026-11-23: Phase 1 — Foundation
(Phase 1 entries preserved below)
