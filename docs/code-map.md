# Code Map

One entry per file or directory: path, purpose, and what depends on it.
Updated in the same commit that adds, moves, or removes a file.

## Root Files

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `go.mod` | Go module definition, declares dependencies | All Go code |
| `Makefile` | Developer workflow targets (up, down, test, lint, etc.) | Developers |
| `.env.example` | Template for environment variables | Developers, Docker Compose |
| `.gitignore` | Files excluded from version control | Git |

## cmd/minipay/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `main.go` | Entry point, subcommand routing (api, worker, migrate, admin) | Binary build |
| `api.go` | Wires up and starts the HTTP API server with graceful shutdown | `main.go` |
| `worker.go` | Wires up and starts the background worker (stub) | `main.go` |
| `migrate.go` | Runs database migrations via golang-migrate | `main.go` |
| `admin.go` | Routes admin CLI subcommands (stub) | `main.go` |

## internal/platform/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `doc.go` | Package documentation | — |
| `config.go` | Loads and validates config from env vars; rejects sk_live_ keys | All packages |
| `config_test.go` | Tests config loading, sk_live_ rejection, DSN redaction | CI |
| `logging.go` | Creates structured JSON logger via slog | All packages |
| `database.go` | PostgreSQL connection pool via pgx/v5 | api, worker |
| `redis.go` | Redis client creation and health check | api (rate limiting) |
| `migrate.go` | Migration runner using golang-migrate | cmd/minipay/migrate.go |

## internal/api/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `doc.go` | Package documentation | — |
| `router.go` | HTTP router setup and middleware chain | cmd/minipay/api.go |
| `health.go` | /healthz and /readyz endpoints | router.go |
| `middleware.go` | Panic recovery, request ID generation | router.go |

## db/migrations/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `000001_init.up.sql` | Baseline schema: enables pgcrypto | migrate command |
| `000001_init.down.sql` | Rolls back baseline schema | migrate command |

## deploy/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `docker-compose.yml` | Local Postgres 16 and Redis 7 containers | `make up` |

## api/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `openapi.yaml` | API specification (OpenAPI 3.1) | CI validation, documentation |

## docs/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `architecture.md` | Mermaid diagrams: components, state machine, sequences | README |
| `code-map.md` | This file — maps every file to its purpose | Developers |
| `work-log.md` | Chronological log of work done and why | Developers, interviews |
| `ai-usage-log.md` | Log of AI-generated content and how it was verified | Interviews |
| `adr/0001-stack-and-why.md` | Technology stack selection rationale | Architecture decisions |

## .github/workflows/

| Path | Purpose | Depended On By |
|------|---------|---------------|
| `ci.yml` | CI pipeline: lint, test, build on PRs | GitHub Actions |

## Stub Directories (to be populated)

| Path | Purpose |
|------|---------|
| `internal/payments/` | Payment service, state machine, repository |
| `internal/ledger/` | Posting rules, invariant checker |
| `internal/idempotency/` | Key store and replay logic |
| `internal/outbox/` | Claim, lease, relay |
| `internal/webhooks/` | Signer, dispatcher, backoff, SSRF guard |
| `internal/processor/` | Interface, simulator, Stripe adapter |
| `internal/risk/` | Rules and evaluator |
| `internal/ratelimit/` | Limiter interface, Redis and memory implementations |
| `db/queries/` | sqlc SQL queries |
| `test/` | Integration tests, k6 scripts, fault tests |

