package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/minipay/minipay/internal/platform/db"
	"github.com/minipay/minipay/internal/webhooks"
)

// WebhookConfigRequest represents the payload for configuring a merchant's webhook endpoint.
type WebhookConfigRequest struct {
	URL string `json:"url"`
}

// WebhookConfigResponse represents the returned webhook configuration.
type WebhookConfigResponse struct {
	MerchantID    string  `json:"merchant_id"`
	WebhookURL    *string `json:"webhook_url"`
	WebhookSecret string  `json:"webhook_secret"`
}

// MerchantsHandler handles merchant management endpoints.
type MerchantsHandler struct {
	queries *db.Queries
	policy  webhooks.SecurityPolicy
}

// NewMerchantsHandler creates a new MerchantsHandler.
func NewMerchantsHandler(queries *db.Queries, policy webhooks.SecurityPolicy) *MerchantsHandler {
	return &MerchantsHandler{
		queries: queries,
		policy:  policy,
	}
}

// UpdateWebhook handles PUT /v1/merchants/webhook.
func (h *MerchantsHandler) UpdateWebhook(w http.ResponseWriter, r *http.Request) {
	merchant, err := MerchantFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthenticated merchant")
		return
	}

	var req WebhookConfigRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_body", fmt.Sprintf("malformed JSON body: %v", err))
		return
	}

	trimmedURL := strings.TrimSpace(req.URL)
	var urlParam pgtype.Text

	if trimmedURL != "" {
		validatedURL, err := webhooks.ValidateURL(trimmedURL, h.policy)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_webhook_url", err.Error())
			return
		}
		urlParam = pgtype.Text{String: validatedURL.String(), Valid: true}
	} else {
		urlParam = pgtype.Text{Valid: false}
	}

	updated, err := h.queries.UpdateMerchantWebhook(r.Context(), db.UpdateMerchantWebhookParams{
		ID:         merchant.ID,
		WebhookUrl: urlParam,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update webhook configuration")
		return
	}

	var whURL *string
	if updated.WebhookUrl.Valid {
		u := updated.WebhookUrl.String
		whURL = &u
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(WebhookConfigResponse{
		MerchantID:    updated.ID,
		WebhookURL:    whURL,
		WebhookSecret: updated.WebhookSecret,
	})
}

// GetWebhook handles GET /v1/merchants/webhook.
func (h *MerchantsHandler) GetWebhook(w http.ResponseWriter, r *http.Request) {
	merchant, err := MerchantFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthenticated merchant")
		return
	}

	current, err := h.queries.GetMerchantByID(r.Context(), merchant.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to retrieve merchant details")
		return
	}

	var whURL *string
	if current.WebhookUrl.Valid {
		u := current.WebhookUrl.String
		whURL = &u
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(WebhookConfigResponse{
		MerchantID:    current.ID,
		WebhookURL:    whURL,
		WebhookSecret: current.WebhookSecret,
	})
}
