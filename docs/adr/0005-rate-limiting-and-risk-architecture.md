# ADR 0005: Rate Limiting, Risk Rules Engine, and Operational Observability

## Status

Accepted (2026-11-25)

## Context

Payments APIs require robust multi-tenant rate limiting and fraud/risk detection to protect infrastructure and financial assets:
1. **API Flooding & DoS**: Malicious actors or misconfigured retry loops can overwhelm backend services, exhaust connection pools, or trigger duplicate processing storms.
2. **Fraud & High-Value Risks**: Unchecked high-value charges or high-velocity card testing can result in catastrophic chargeback liability and platform merchant bans.
3. **Observability & Operational Auditing**: Production operators require real-time metrics (latency histograms, throughput, error rates, ledger integrity) and administrative tooling to review flagged payments without stopping production services.

## Decision

We implemented a defense-in-depth architecture covering rate limiting, risk decisioning, metrics exposition, and administrative oversight:

### 1. Token-Bucket Rate Limiting with Fail-Open Resilience

- **Redis-Backed Atomic Token Bucket**:
  Rate limits are evaluated atomically in Redis via a Lua script (`internal/ratelimit/limiter.go`):
  - Tokens are refilled continuously based on elapsed wall-clock seconds up to burst capacity.
  - Rejections emit standard headers:
    - `X-RateLimit-Limit`: configured bucket capacity.
    - `X-RateLimit-Remaining`: tokens currently available.
    - `X-RateLimit-Reset`: seconds until full refill.
  - Throttled requests immediately return `429 Too Many Requests` with a structured JSON error body (`rate_limit_exceeded`).
- **Fail-Open In-Memory Fallback**:
  If Redis is partitioned or unavailable, the limiter automatically falls back to an in-memory token bucket, preventing cascading payment outages while preserving protection.

### 2. Multi-Tier Risk Rules Engine

Every payment creation request passes through the pluggable `risk.Engine` (`internal/risk/rules.go`):
- **Amount Cap Rule**:
  - Normal amounts ($<\$500$) are allowed (`OutcomeAllow`).
  - Moderate high-value amounts ($\ge \$500$ and $<\$5000$) transition to `requires_review` (`OutcomeReview`), creating the payment record without enqueuing processing outbox events.
  - Excessive amounts ($\ge \$5000$) are blocked immediately (`OutcomeBlock`), creating an immutable `failed` payment record with `failure_code: 'risk_blocked'`.
- **Velocity Rule**:
  Tracks request frequency per merchant across rolling time windows, preventing automated card-testing scripts.
- **Auditability**:
  All evaluations are recorded to the `risk_decisions` table linked to the payment ID (`rule`, `outcome`, `detail` JSON, `created_at`). Merchants can inspect decisions via `GET /v1/payments/{id}/risk`.

### 3. Operational Administration & Review Workflow

In `cmd/minipay/admin.go`:
- `list-reviews`: Lists payments awaiting manual review.
- `review-payment -id <pay_id> -action <approve|decline>`:
  - **Approve**: Atomically transitions `requires_review` $\to$ `processing` and enqueues `payment.process` into the outbox for background worker processing.
  - **Decline**: Transitions to `failed` with reason `admin_declined`.
- `inspect-risk -id <pay_id>`: Displays rule-by-rule evaluation logs.
- `dashboard -port 8081`: Serves a secure, authenticated web dashboard showing ledger health and review queues.

### 4. Zero-Dependency Prometheus Metrics

Implemented in `internal/metrics/metrics.go`:
- Exposes standard Prometheus text exposition format (`text/plain; version=0.0.4`) on `GET /metrics`.
- Tracks:
  - `http_requests_total{method, path, status}`
  - `http_request_duration_seconds{method, path}` (histograms with latency buckets)
  - `payments_total{status, currency}`
  - `ledger_invariants_violations` (gauge)

## Consequences

### Positive
- **Guaranteed Throughput Protection**: Tested under burst traffic; excess requests receive standard 429 responses.
- **Fraud & High-Value Isolation**: Flagged payments cannot trigger charges until explicit admin approval.
- **Zero External Dependencies for Metrics**: Self-contained Prometheus exposition eliminates brittle network library pulls while providing standard metric scraping compatibility.
- **Complete Audit Trail**: Every decision is permanently stored in PostgreSQL and retrievable via CLI or API.
