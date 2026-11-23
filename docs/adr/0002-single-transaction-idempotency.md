# ADR 0002: Single-Transaction Idempotency with Response Caching

## Status

Accepted (2026-11-23)

## Context

Payments APIs must guarantee exactly-once processing semantics under network timeouts, client retries, and concurrent duplicate storms. If a network blip occurs after a payment is committed but before the HTTP 201 response reaches the client, the client or SDK will automatically retry the request with the same `Idempotency-Key`.

Two common architectural patterns exist to solve this:

1. **Two-Tier (Redis Mutex + Database Cache)**:
   - Acquire a distributed lock in Redis with a short TTL (e.g. `SET key lock NX EX 10`).
   - Execute database transaction.
   - Store response in Redis or Postgres.
   - Release lock.
   - *Failure modes*: Network partitions between Redis and Postgres can cause orphaned locks, two-phase failures, or split-brain states where Redis considers the operation finished but Postgres rolled back.

2. **Single-Transaction PostgreSQL Lock & Cache**:
   - Store idempotency state directly in PostgreSQL (`idempotency_keys` table).
   - Execute key reservation, payment creation, outbox event generation, and response caching inside a single atomic ACID transaction.
   - Use row-level locking (`FOR UPDATE` / unique constraints) and explicit request payload hashing (`SHA-256(method + "\n" + path + "\n" + body)`).

## Decision

We chose **Option 2: Single-Transaction PostgreSQL Idempotency**.

### Implementation Details

1. **Request Payload Hash**:
   Every incoming request computes:
   $$\text{Hash} = \text{SHA-256}(\text{method} \parallel \text{"\n"} \parallel \text{path} \parallel \text{"\n"} \parallel \text{body})$$
   This guarantees that if a merchant reuses an idempotency key with different parameters (e.g. altering the amount from \$10.00 to \$20.00), MiniPay detects the conflict and rejects it with `422 Unprocessable Entity` (`idempotency_key_reused`).

2. **Transaction Isolation & State Lifecycle**:
   - `state = 'started'`: When the transaction begins, the key is inserted or refreshed.
   - Concurrent requests for the same key encounter a row-level lock or detect `state = 'started'`, immediately failing fast with `409 Conflict` (`concurrent_request`).
   - `state = 'completed'`: When the payment insertion and outbox event commit, the response HTTP status and JSON body are stored in the same row.
   - If payment creation fails due to validation or database constraints, the entire transaction rolls back, releasing locks and leaving zero orphaned state.

3. **Replay Semantics**:
   When a completed key is presented with the matching request hash:
   - MiniPay skips payment processing entirely.
   - It serves the cached response body and status code.
   - It appends the HTTP header `Idempotent-Replayed: true` to inform the caller that no new state mutation occurred.

## Consequences

### Positive
- **Guaranteed Atomicity**: It is impossible for a payment to exist without an idempotency record, or for an idempotency record to indicate success when the payment failed.
- **No Distributed Lock Hazards**: No split-brain states, no Redis network timeout edge cases, and no leaked locks during unhandled worker crashes.
- **Auditable**: Key reuse attempts and timings are queryable directly in PostgreSQL.

### Negative / Trade-offs
- Higher write load on PostgreSQL compared to in-memory Redis locks. For MiniPay's operational scale and consistency requirements, transactional correctness is far more important than raw in-memory throughput.
- Keys are retained with a 24-hour expiration window (`expires_at`), requiring background cleanup or periodic partition maintenance.
