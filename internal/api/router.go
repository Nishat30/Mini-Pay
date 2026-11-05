// router.go sets up the HTTP router and middleware chain.
// It owns route registration and middleware ordering.
// It must not contain handler logic — handlers go in their own files.
// It relies on net/http built-in routing (Go 1.22+ method patterns).
//
// Middleware order: recover → request ID → logging/metrics → body limit →
// request timeout → auth → rate limit → handler.
package api

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// NewRouter creates the HTTP handler with all routes and middleware.
func NewRouter(logger *slog.Logger, db *pgxpool.Pool, rdb *redis.Client) http.Handler {
	mux := http.NewServeMux()

	// Health checks — no auth required.
	mux.HandleFunc("GET /healthz", handleHealthz())
	mux.HandleFunc("GET /readyz", handleReadyz(db, rdb))

	// TODO(phase-1): add /v1/payments, /v1/events, /v1/merchant routes.

	// Wrap with middleware (outermost first).
	var handler http.Handler = mux
	handler = requestIDMiddleware(handler)
	handler = recoverMiddleware(handler, logger)

	return handler
}
