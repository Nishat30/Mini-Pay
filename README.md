# MiniPay

A payments backend in Go demonstrating correctness under retries and races, double-entry accounting, reliable webhook delivery, and production engineering habits.

## Architecture

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

## Quickstart

```bash
# 1. Clone and enter the project
git clone https://github.com/Nishat30/Mini-Pay.git
cd Mini-Pay

# 2. Copy environment config
cp .env.example .env

# 3. Start dependencies (Postgres 16, Redis 7)
make up

# 4. Run migrations
make migrate

# 5. Start the API server
go run ./cmd/minipay api
```

## Stack

| Component | Choice | Justification |
|-----------|--------|---------------|
| Language | Go 1.25 | Concurrency, single binary, strong stdlib |
| Database | PostgreSQL 16 | ACID, deferred triggers, FOR UPDATE SKIP LOCKED |
| Cache | Redis 7 | Rate limiting only, behind interface |
| SQL | pgx v5 + sqlc + golang-migrate | Type-safe generated code from SQL |
| HTTP | net/http (Go 1.22+ routing) | No extra dependency needed |
| Logging | log/slog | Standard library structured logging |
| Testing | testcontainers-go, k6, toxiproxy | Real dependencies, load testing, fault injection |

See [ADR 0001](docs/adr/0001-stack-and-why.md) for the full rationale.

## Development

```bash
make up               # Start Postgres and Redis
make down             # Stop services
make test             # Unit tests with -race
make test-integration # Integration tests (needs running services)
make lint             # golangci-lint + go vet
make migrate          # Apply migrations
make build            # Build binary to bin/
```

## Project Status

- [x] Phase 0: Bootstrap — project scaffold, health checks, CI
- [ ] Phase 1: Foundation — payments, idempotency, ledger, worker
- [ ] Phase 2: Reliability — refunds, webhooks, rate limiting, risk
- [ ] Phase 3: Hardening — load tests, fault injection, migration drill
- [ ] Phase 4: Stripe and Polish — Stripe adapter, admin page, dashboard

## Documentation

- [Architecture](docs/architecture.md) — Component diagrams, state machine, sequences
- [Code Map](docs/code-map.md) — Every file and its purpose
- [Work Log](docs/work-log.md) — What was built and why
- [Design Decisions](docs/adr/) — ADRs for every non-obvious choice
- [AI Usage Log](docs/ai-usage-log.md) — What was AI-generated and how it was verified

## Security

- API keys stored as SHA-256 hashes only
- Stripe live keys (`sk_live_`) rejected at startup
- No secrets in git — see `.env.example`
- All secrets from `crypto/rand`, compared in constant time

