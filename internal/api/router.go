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

	"github.com/minipay/minipay/internal/metrics"
	"github.com/minipay/minipay/internal/platform/db"
	"github.com/minipay/minipay/internal/ratelimit"
	"github.com/minipay/minipay/internal/webhooks"
)

// NewRouter creates the HTTP handler with all routes and middleware.
func NewRouter(logger *slog.Logger, pool *pgxpool.Pool, rdb *redis.Client, policies ...webhooks.SecurityPolicy) http.Handler {
	mux := http.NewServeMux()

	var policy webhooks.SecurityPolicy
	if len(policies) > 0 {
		policy = policies[0]
	}

	// Health & Metrics checks — no auth required.
	metrics.SetLedgerViolations(0)
	metrics.InitIdempotencyMetrics()
	mux.HandleFunc("GET /healthz", handleHealthz())
	mux.HandleFunc("GET /readyz", handleReadyz(pool, rdb))
	mux.HandleFunc("GET /metrics", metrics.DefaultRegistry.Handler())

	// Database queries and handlers
	queries := db.New(pool)
	paymentsHandler := NewPaymentsHandler(pool, queries)
	merchantsHandler := NewMerchantsHandler(queries, policy)

	// Rate limiter (Redis with in-memory fallback)
	limiter := ratelimit.NewRedisLimiter(rdb)
	rateLimitMiddleware := ratelimit.Middleware(limiter, 100, 10)

	// Authenticated routes wrapped with rate limiting
	authMiddleware := Authenticate(queries)
	apiChain := func(h http.Handler) http.Handler {
		return authMiddleware(rateLimitMiddleware(h))
	}

	mux.Handle("POST /v1/payments", apiChain(http.HandlerFunc(paymentsHandler.CreatePayment)))
	mux.Handle("GET /v1/payments/{id}", apiChain(http.HandlerFunc(paymentsHandler.GetPayment)))
	mux.Handle("GET /v1/payments", apiChain(http.HandlerFunc(paymentsHandler.ListPayments)))
	mux.Handle("POST /v1/payments/{id}/refunds", apiChain(http.HandlerFunc(paymentsHandler.CreateRefund)))
	mux.Handle("GET /v1/payments/{id}/refunds", apiChain(http.HandlerFunc(paymentsHandler.ListRefunds)))
	mux.Handle("GET /v1/payments/{id}/risk", apiChain(http.HandlerFunc(paymentsHandler.GetPaymentRisk)))

	// Webhook configuration routes
	mux.Handle("PUT /v1/merchants/webhook", apiChain(http.HandlerFunc(merchantsHandler.UpdateWebhook)))
	mux.Handle("GET /v1/merchants/webhook", apiChain(http.HandlerFunc(merchantsHandler.GetWebhook)))

	// Wrap with global middleware (outermost first).
	var handler http.Handler = mux
	handler = metricsMiddleware(handler)
	handler = requestIDMiddleware(handler)
	handler = recoverMiddleware(handler, logger)

	return handler
}
