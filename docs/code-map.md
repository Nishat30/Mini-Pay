# Code Map

One entry per file or directory: path, purpose, and what depends on it.
Updated in the same commit that adds, moves, or removes a file.

## Root Files

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `go.mod` | Go module definition, declares dependencies | All Go code |
| `Makefile` | Developer workflow targets (up, down, test, lint, demo, etc.) | Developers |
| `.env.example` | Template for environment variables | Developers, Docker Compose |
| `.gitignore` | Files excluded from version control | Git |
| `sqlc.yaml` | sqlc code generation configuration for pgx/v5 | Developers, Docker |

## cmd/minipay/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `main.go` | Entry point, subcommand routing (api, worker, migrate, admin) | Binary build |
| `api.go` | Wires up and starts the HTTP API server with graceful shutdown | `main.go` |
| `worker.go` | Wires up and starts the background payment worker and webhook dispatcher | `main.go` |
| `migrate.go` | Runs database migrations via golang-migrate | `main.go` |
| `admin.go` | Routes admin CLI subcommands (`create-merchant`, `check-ledger`) | `main.go` |

## internal/platform/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `doc.go` | Package documentation | — |
| `config.go` | Loads and validates config from env vars; rejects sk_live_ keys | All packages |
| `config_test.go` | Tests config loading, sk_live_ rejection, DSN redaction | CI |
| `logging.go` | Creates structured JSON logger via slog | All packages |
| `database.go` | PostgreSQL connection pool via pgx/v5 | api, worker, admin |
| `redis.go` | Redis client creation and health check | api (rate limiting) |
| `migrate.go` | Migration runner using golang-migrate | cmd/minipay/migrate.go |
| `ids.go` | Prefixed ULID generator (`mer_`, `pay_`, `ref_`, `acc_`, `ltx_`, `whd_`, `evt_`) | Domain packages |
| `ids_test.go` | Unit tests for prefixed ULID generation and format validation | CI |
| `clock.go` | Clock interface with RealClock and FakeClock for test determinism | Worker, services |
| `tx.go` | WithTx transaction runner with automatic commit/rollback | API, worker, ledger |
| `db/` | sqlc generated models, querier interface, and pgx/v5 queries | API, worker, ledger, admin |

## internal/payments/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `state.go` | Pure payment lifecycle state machine and event transitions | API, worker |
| `state_test.go` | Table-driven unit tests verifying legal and illegal transitions | CI |

## internal/ledger/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `ledger.go` | Double-entry posting rules, fee calculations, invariant checker | Worker, admin |
| `ledger_test.go` | Unit tests for fee calculation, balanced capture and refund entries | CI |

## internal/idempotency/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `store.go` | Single-transaction idempotency key locking, hash checking, response caching | API |
| `store_test.go` | Unit tests for SHA-256 request hashing and key validation | CI |

## internal/processor/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `simulator.go` | PSP interface and deterministic simulator (success, decline, network_error) | Worker, tests |

## internal/outbox/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `worker.go` | Transactional outbox polling, processor invocation, ledger posting | cmd/minipay/worker.go |

## internal/webhooks/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `ssrf.go` | SSRF defense, blocked CIDRs, and safe HTTP dialer with socket controls | Dispatcher, API |
| `ssrf_test.go` | Unit tests for private IP detection and static URL validation | CI |
| `signer.go` | HMAC-SHA256 signature generator and timestamp replay verification | Dispatcher, tests |
| `signer_test.go` | Unit tests for payload signing, tampering detection, and replay expiry | CI |
| `dispatcher.go` | Webhook event poller, delivery sender, exponential backoff, dead-lettering | cmd/minipay/worker.go |

## internal/api/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `doc.go` | Package documentation | — |
| `router.go` | HTTP router setup and middleware chain | cmd/minipay/api.go |
| `health.go` | /healthz and /readyz endpoints | router.go |
| `middleware.go` | Panic recovery, request ID generation | router.go |
| `auth.go` | Bearer token authentication middleware and context extraction | router.go |
| `payments.go` | POST /v1/payments, GET /v1/payments/{id}, GET /v1/payments | router.go |
| `merchants.go` | PUT /v1/merchants/webhook, GET /v1/merchants/webhook | router.go |

## db/migrations/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `000001_init.up.sql` | Baseline schema: enables pgcrypto | migrate command |
| `000001_init.down.sql` | Rolls back baseline schema | migrate command |
| `000002_core_schema.up.sql` | 11 core tables, ledger balance & immutability triggers | migrate command |
| `000002_core_schema.down.sql` | Drops triggers and 11 core tables | migrate command |

## db/queries/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `merchants.sql` | Merchant creation and lookup queries | sqlc |
| `idempotency.sql` | Idempotency key lock, check, and completion queries | sqlc |
| `payments.sql` | Payment creation, lookup, status update, event logging | sqlc |
| `ledger.sql` | Account lazy creation, transaction, entry, invariant queries | sqlc |
| `outbox.sql` | Outbox event creation, claim with skip locked, retry, mark processed | sqlc |
| `webhooks.sql` | Webhook delivery creation, claim pending, success, failure update | sqlc |

## deploy/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `docker-compose.yml` | Local Postgres 16 and Redis 7 containers | `make up` |

## api/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `openapi.yaml` | API specification (OpenAPI 3.1) including payments and webhook config | CI validation, documentation |

## docs/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `architecture.md` | Mermaid diagrams: components, state machine, sequences | README |
| `code-map.md` | This file — maps every file to its purpose | Developers |
| `work-log.md` | Chronological log of work done and why | Developers, interviews |
| `ai-usage-log.md` | Log of AI-generated content and how it was verified | Interviews |
| `adr/0001-stack-and-why.md` | Technology stack selection rationale | Architecture decisions |
| `adr/0002-single-transaction-idempotency.md` | Single-transaction idempotency and response caching | Architecture decisions |
| `adr/0003-webhook-reliability.md` | Webhook reliability, HMAC signatures, and SSRF prevention | Architecture decisions |

## test/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `phase1_integration_test.go` | Phase 1 integration tests: idempotent replay, storm, ledger triggers, worker | CI |
| `phase2_integration_test.go` | Phase 2 integration tests: delivery success, retry backoff, dead letter, SSRF | CI |
