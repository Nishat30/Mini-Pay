# Architecture

This document describes MiniPay's architecture. It is updated whenever the architecture changes.

## Component Diagram

```mermaid
flowchart LR
  M[Merchant server] -->|"POST /v1/payments with Idempotency-Key"| A1
  subgraph API["API process: minipay api"]
    A1[Auth and rate limit] --> A2[Idempotency layer] --> A3[Payment handlers]
  end
  subgraph WK["Worker: minipay worker"]
    W1[Outbox relay]
    W2[Webhook dispatcher]
    W3[Processor adapter]
  end
  A1 -->|token bucket| R[(Redis)]
  A3 -->|one transaction| PG[(PostgreSQL)]
  W1 -->|claim with lease| PG
  W2 -->|"signed POST, retried"| H[Merchant webhook endpoint]
  W3 --> S[Simulator or Stripe test mode]
  S -.->|webhook callback| A1
```

## Payment State Machine

```mermaid
stateDiagram-v2
  [*] --> processing : risk allows
  [*] --> requires_review : risk says review
  [*] --> failed : risk says block
  processing --> succeeded : processor succeeded
  processing --> failed : processor declined / retries exhausted
  succeeded --> refunded : refunds sum to full amount
```

## Sequence Diagram: Create Payment to Webhook

(To be added in Phase 1 when the full flow is implemented.)

## Ledger Posting Example

(To be added in Phase 1 when the ledger is implemented.)

