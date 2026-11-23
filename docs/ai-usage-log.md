# AI Usage Log

This log records every non-trivial piece of AI-generated content, how it was verified, and what changed after review. Honesty is the point — this file is evidence of judgment.

---

## 2026-11-03: Phase 0 Scaffold

### Generated
- Project structure and all bootstrap files (config, logging, router, health checks, middleware, migrations, Makefile, CI, ADR, docs).

### Verification
- **Config**: Unit tests verify sk_live_ rejection, default values, and DSN redaction.
- **Health endpoints**: Manual curl test against running server.
- **Makefile**: Verified each target runs without error.
- **CI YAML**: Validated syntax matches GitHub Actions schema.
- **ADR**: Reviewed against current Go, pgx, sqlc, and Redis documentation.

### Changes After Review
- Anchored root binary ignore pattern in `.gitignore` (`/minipay`) after discovering that bare `minipay` caused Git to ignore `cmd/minipay/`.
- Adjusted `.github/workflows/ci.yml` to compile `golangci-lint` with the runner's Go compiler and run `go mod download` prior to `go build` to ensure clean builds in CI.
- Handled unhandled errors in `internal/api/health.go` (`_ = json.NewEncoder...`) to satisfy `errcheck`.

