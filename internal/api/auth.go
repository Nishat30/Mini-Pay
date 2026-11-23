// Package api provides HTTP request handlers, middlewares, and routing for MiniPay.
package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/minipay/minipay/internal/platform/db"
)

const merchantContextKey contextKey = "merchant"

var (
	ErrUnauthorized = errors.New("unauthorized")
)

// Authenticate returns middleware that validates the Bearer token in the Authorization header.
func Authenticate(queries *db.Queries) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				writeError(w, http.StatusUnauthorized, "unauthorized", "missing Authorization header")
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
				writeError(w, http.StatusUnauthorized, "unauthorized", "malformed Authorization header, expected 'Bearer <token>'")
				return
			}

			token := strings.TrimSpace(parts[1])
			if token == "" {
				writeError(w, http.StatusUnauthorized, "unauthorized", "empty bearer token")
				return
			}

			// Prohibit live secrets
			if strings.HasPrefix(token, "sk_live_") {
				writeError(w, http.StatusUnauthorized, "unauthorized", "live keys are not permitted")
				return
			}

			hash := sha256.Sum256([]byte(token))
			merchant, err := queries.GetMerchantByAPIKeyHash(r.Context(), hash[:])
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					writeError(w, http.StatusUnauthorized, "unauthorized", "invalid API key")
					return
				}
				writeError(w, http.StatusInternalServerError, "internal_error", "failed to authenticate merchant")
				return
			}

			ctx := context.WithValue(r.Context(), merchantContextKey, &merchant)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// MerchantFromContext extracts the authenticated merchant from request context.
func MerchantFromContext(ctx context.Context) (*db.Merchants, error) {
	merchant, ok := ctx.Value(merchantContextKey).(*db.Merchants)
	if !ok || merchant == nil {
		return nil, ErrUnauthorized
	}
	return merchant, nil
}

// writeError serializes a standardized JSON error response conforming to MiniPay API guidelines.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}
