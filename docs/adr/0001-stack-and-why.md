# ADR 0001: Technology Stack Selection

## Status
Accepted

## Date
2026-11-03

## Context
We are building MiniPay, a payments backend that must demonstrate correctness under retries and races, double-entry accounting, reliable webhook delivery, and production engineering habits. The project is for interview preparation, so the code must be explainable and the choices defensible.

## Decision

### Language: Go (1.25)
- Strong concurrency model needed for webhook dispatching and outbox processing.
- Standard library is excellent for HTTP servers (built-in method routing since 1.22).
- Static typing catches errors at compile time, important for money handling.
- Single binary deployment simplifies Docker builds.

### Database: PostgreSQL 16
- ACID transactions are essential for double-entry accounting and idempotency.
- Advisory locks, `FOR UPDATE SKIP LOCKED`, and deferred constraint triggers support our concurrency patterns.
- Mature, well-understood, and free.

### SQL Tools: pgx v5 + sqlc + golang-migrate
- **pgx v5**: Native Go Postgres driver with connection pooling, no CGo required.
- **sqlc**: Generates type-safe Go from SQL queries. SQL is the source of truth, not an ORM.
- **golang-migrate**: Simple, file-based migrations. Up and down migrations ensure reversibility.

### Cache: Redis 7
- Used only for rate limiting (token buckets), not for business data.
- Behind an interface with an in-memory fallback for testing and single-process mode.
- Fail-open on errors (see ADR for rate limiting).

### HTTP: net/http standard library
- Go 1.22+ method routing eliminates the need for chi/gorilla/echo.
- Fewer dependencies, smaller attack surface, easier to explain.

### Logging: log/slog
- Standard library structured logging, added in Go 1.21.
- JSON output for machine parsing, with request_id correlation.

### Observability: Prometheus + OpenTelemetry + Grafana
- Industry standard. Prometheus for metrics, OTel for traces, Grafana for dashboards.
- Dashboard JSON stored in repo for reproducibility.

### Testing: testcontainers-go + k6 + toxiproxy
- **testcontainers-go**: Real Postgres and Redis in integration tests, no mocks.
- **k6**: Load testing with scriptable scenarios.
- **toxiproxy**: Fault injection between services.

### Containers: Multi-stage Docker + distroless
- Small, secure runtime image with no shell.
- Non-root execution.

## Alternatives Considered

| Choice | Alternative | Why Not |
|--------|-----------|---------|
| Go | Rust, Java | Go's simplicity is better for explainability; Java is verbose |
| net/http | chi, echo | Extra dependency with no benefit since Go 1.22 |
| pgx | database/sql | pgx is faster, has native types, and still implements database/sql |
| sqlc | GORM, sqlx | ORM hides SQL; sqlc generates from SQL so the queries are visible |
| Redis | In-memory only | Redis simulates production multi-process rate limiting |

## Consequences
- All dependencies must be justified in PR descriptions.
- Developers need Docker for integration tests.
- The project compiles to a single binary, simplifying deployment.
- PostgreSQL is the only source of truth; Redis data is ephemeral.

