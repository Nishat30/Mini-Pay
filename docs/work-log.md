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
- `make up` starts Postgres and Redis containers.
- `curl localhost:8080/healthz` returns 200 `{"status":"ok"}`.
- `go test ./...` passes (config tests).
- CI YAML is syntactically valid.

### What's Left
- Phase 1: merchants, auth, payments, idempotency, state machine, ledger, worker.

