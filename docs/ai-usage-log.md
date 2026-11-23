# AI Usage Log

This log records every non-trivial piece of AI-generated content, how it was verified, and what changed after review. Honesty is the point — this file is evidence of judgment.

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
