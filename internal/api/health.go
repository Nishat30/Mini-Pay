// health.go implements liveness and readiness probes.
// It owns /healthz and /readyz endpoints.
// It must not contain business logic — only dependency checks.
// /readyz checks Postgres and (if configured) Redis connectivity.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func handleHealthz() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func handleReadyz(db *pgxpool.Pool, rdb *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		checks := map[string]string{}
		healthy := true

		// Check Postgres.
		if err := db.Ping(ctx); err != nil {
			checks["postgres"] = err.Error()
			healthy = false
		} else {
			checks["postgres"] = "ok"
		}

		// Check Redis (optional).
		if rdb != nil {
			if err := rdb.Ping(ctx).Err(); err != nil {
				checks["redis"] = err.Error()
				healthy = false
			} else {
				checks["redis"] = "ok"
			}
		}

		w.Header().Set("Content-Type", "application/json")
		if !healthy {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": map[bool]string{true: "ok", false: "degraded"}[healthy],
			"checks": checks,
		})
	}
}
