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

	"github.com/minipay/minipay/internal/platform/db"
	"github.com/minipay/minipay/internal/webhooks"
)

// NewRouter creates the HTTP handler with all routes and middleware.
func NewRouter(logger *slog.Logger, pool *pgxpool.Pool, rdb *redis.Client, policies ...webhooks.SecurityPolicy) http.Handler {
	mux := http.NewServeMux()

	var policy webhooks.SecurityPolicy
	if len(policies) > 0 {
		policy = policies[0]
	}

	// Health checks — no auth required.
	mux.HandleFunc("GET /healthz", handleHealthz())
	mux.HandleFunc("GET /readyz", handleReadyz(pool, rdb))

	// Database queries and handlers
	queries := db.New(pool)
	paymentsHandler := NewPaymentsHandler(pool, queries)
	merchantsHandler := NewMerchantsHandler(queries, policy)

	// Authenticated routes
	authMiddleware := Authenticate(queries)

	mux.Handle("POST /v1/payments", authMiddleware(http.HandlerFunc(paymentsHandler.CreatePayment)))
	mux.Handle("GET /v1/payments/{id}", authMiddleware(http.HandlerFunc(paymentsHandler.GetPayment)))
	mux.Handle("GET /v1/payments", authMiddleware(http.HandlerFunc(paymentsHandler.ListPayments)))

	// Webhook configuration routes
	mux.Handle("PUT /v1/merchants/webhook", authMiddleware(http.HandlerFunc(merchantsHandler.UpdateWebhook)))
	mux.Handle("GET /v1/merchants/webhook", authMiddleware(http.HandlerFunc(merchantsHandler.GetWebhook)))

	// Wrap with global middleware (outermost first).
	var handler http.Handler = mux
	handler = requestIDMiddleware(handler)
	handler = recoverMiddleware(handler, logger)

	return handler
}
