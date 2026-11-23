# Work Log

Newest entries first. Each entry: what was done, why, how verified, what's left.

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
