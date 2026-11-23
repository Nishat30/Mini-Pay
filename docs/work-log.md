# Work Log

Newest entries first. Each entry: what was done, why, how verified, what's left.

---

## 2026-11-03: Phase 0 — Bootstrap

### What
- Initialized Go module, repository layout, and all scaffold files.
- Created Docker Compose with Postgres 16 and Redis 7.
- Implemented config loading with sk_live_ rejection.
- Implemented structured JSON logging via slog.
- Implemented /healthz and /readyz endpoints.
- Created migration runner and baseline migration.
- Set up Makefile, CI workflow, .env.example.
- Wrote ADR 0001 (stack selection).
- Created docs: architecture.md, code-map.md, this work-log, ai-usage-log.md.

### Why
Phase 0 establishes the project skeleton so all subsequent phases have a working build, test, and deployment foundation.

### Verification
- `docker-compose -f deploy/docker-compose.yml up -d` brings up `postgres:16-alpine` and `redis:7-alpine`.
- `go run ./cmd/minipay migrate up` successfully connected to Postgres and applied baseline migration: `applied: 1`.
- Built binary `bin/minipay.exe` from `./cmd/minipay`.
- Started `minipay api` server; probed endpoints via curl:
  - `curl -i -s http://localhost:8080/healthz` returned `HTTP/1.1 200 OK` with `{"status":"ok"}` and `X-Request-Id`.
  - `curl -i -s http://localhost:8080/readyz` returned `HTTP/1.1 200 OK` with `{"checks":{"postgres":"ok","redis":"ok"},"status":"ok"}`.
- Unit tests pass with race detection: `go test -race -shuffle=on -count=1 ./...`.
- GitHub Actions CI is 100% green on all jobs (Lint, Unit Tests, Build).

### Issues Resolved
- `.gitignore` contained unanchored `minipay` which caused Git to ignore `cmd/minipay/`. Fixed by anchoring to `/minipay`.
- GitHub Actions `golangci-lint-action` had AST parsing mismatch with Go 1.25. Resolved by installing `golangci-lint` using the runner's Go compiler.

### What's Left
- Phase 1: merchants, auth, payments, idempotency, state machine, ledger, worker.

