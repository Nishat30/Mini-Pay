# ADR 0004: Refund Accounting, Concurrency Control, and Processor Abstraction

## Status

Accepted (2026-11-24)

## Context

Payments platforms must support both partial and full refunds while maintaining exact accounting consistency and preventing race conditions.

When multiple refund requests arrive concurrently for the same payment (e.g. parallel API client retries or multiple dashboard operators acting simultaneously), a classic race condition arises: two parallel transactions might both check that $\text{amount\_refunded} + \text{refund\_amount} \le \text{payment\_amount}$, both pass, and both commit, resulting in a disastrous **over-refund** where the merchant returns more money than was initially collected.

Additionally, reversing funds requires strict double-entry balancing without modifying immutable historical ledger entries.

## Decision

We implemented a multi-layered concurrency and accounting defense:

### 1. Row-Level Serialization (`FOR UPDATE`) & Database Constraints

1. **Transaction Row Lock**: Every refund request acquires an exclusive row lock on the target payment row via:
   ```sql
   SELECT * FROM payments WHERE id = $1 AND merchant_id = $2 FOR UPDATE;
   ```
   Concurrent refund requests for the same payment are forced to serialize at the database engine level.
2. **Deterministic State Check**: The transaction verifies that `payment.status` is either `succeeded` or `partially_refunded`, and checks that:
   $$\text{payment.amount\_refunded} + \text{req.amount} \le \text{payment.amount}$$
   If exceeded, the transaction fails fast with `400 Bad Request` (`refund_exceeds_balance`).
3. **Database-Level Check Constraint**: As defense-in-depth against application bugs, the `payments` table enforces:
   ```sql
   CONSTRAINT payments_amount_refunded_check CHECK (amount_refunded BETWEEN 0 AND amount)
   ```
   PostgreSQL will abort and roll back any transaction attempting to increment `amount_refunded` beyond `amount`.

### 2. Double-Entry Ledger Reversal

MiniPay preserves ledger immutability (`forbid_mutation` trigger) by never editing or deleting historical capture entries. Instead, every refund creates a new `ledger_transactions` row with `kind = 'refund'` and unique reference `refund:<refund_id>`:
- **Debit**: `merchant_payable` ($\text{refund\_amount}$)
- **Credit**: `processor_clearing` ($\text{refund\_amount}$)

Debits and credits balance exactly, satisfying the deferred trigger `assert_ledger_balanced`.

### 3. Asynchronous Webhook Notification

Upon refund commit, an outbox event of type `refund.succeeded` is enqueued atomically within the same transaction. The webhook dispatcher delivers this event to the merchant's endpoint with HMAC-SHA256 signature verification.

### 4. Processor Interface & Stripe Adapter

Processors implement the pure `Processor` interface:
```go
type Processor interface {
    Charge(ctx context.Context, req ChargeRequest) (*ChargeResult, error)
}
```
MiniPay provides both:
- `Simulator`: Deterministic in-memory PSP simulator for fast, isolated tests.
- `StripeAdapter`: Direct HTTP adapter for Stripe's test-mode charges API (`sk_test_`), strictly rejecting live keys (`sk_live_`).

## Consequences

### Positive
- **Guaranteed No Over-Refunding**: Verified via integration race test (10 concurrent requests on a \$10 balance result in exactly 5 successes and 5 rejections).
- **Auditability**: Every partial and full refund has an immutable ULID (`ref_...`), corresponding ledger transaction (`refund:<id>`), and webhook delivery audit log.
- **Provider Agnostic**: Easily swappable between Stripe, Adyen, and simulated processors.
