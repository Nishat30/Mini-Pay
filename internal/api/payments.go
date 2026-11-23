package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/minipay/minipay/internal/idempotency"
	"github.com/minipay/minipay/internal/ledger"
	"github.com/minipay/minipay/internal/platform"
	"github.com/minipay/minipay/internal/platform/db"
)

// SupportedCurrencies lists standard currencies for MiniPay.
var supportedCurrencies = map[string]bool{
	"usd": true,
	"eur": true,
	"gbp": true,
	"inr": true,
	"jpy": true,
}

// PaymentRequest represents the payload for POST /v1/payments.
type PaymentRequest struct {
	Amount   int64          `json:"amount"`
	Currency string         `json:"currency"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// PaymentResponse represents a payment resource returned by the API.
type PaymentResponse struct {
	ID             string         `json:"id"`
	MerchantID     string         `json:"merchant_id"`
	Amount         int64          `json:"amount"`
	AmountRefunded int64          `json:"amount_refunded"`
	Currency       string         `json:"currency"`
	Status         string         `json:"status"`
	Processor      string         `json:"processor"`
	ProcessorRef   *string        `json:"processor_ref,omitempty"`
	FailureCode    *string        `json:"failure_code,omitempty"`
	Metadata       map[string]any `json:"metadata"`
	CreatedAt      string         `json:"created_at"`
	UpdatedAt      string         `json:"updated_at"`
}

// PaymentsHandler handles /v1/payments endpoints.
type PaymentsHandler struct {
	pool          *pgxpool.Pool
	queries       *db.Queries
	ledgerService *ledger.Service
}

// NewPaymentsHandler creates a new PaymentsHandler.
func NewPaymentsHandler(pool *pgxpool.Pool, queries *db.Queries) *PaymentsHandler {
	return &PaymentsHandler{
		pool:          pool,
		queries:       queries,
		ledgerService: ledger.NewService(queries),
	}
}

// RefundRequest represents the payload for POST /v1/payments/{id}/refunds.
type RefundRequest struct {
	Amount int64  `json:"amount"`
	Reason string `json:"reason,omitempty"`
}

// RefundResponse represents a created refund resource.
type RefundResponse struct {
	ID        string `json:"id"`
	PaymentID string `json:"payment_id"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

// CreatePayment handles POST /v1/payments.
func (h *PaymentsHandler) CreatePayment(w http.ResponseWriter, r *http.Request) {
	merchant, err := MerchantFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthenticated merchant")
		return
	}

	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key header is required")
		return
	}
	if err := idempotency.ValidateKey(idempotencyKey); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_idempotency_key", err.Error())
		return
	}

	// Limit request body to 1 MB
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "request_body_too_large", "request body exceeds 1MB limit")
		return
	}

	var req PaymentRequest
	dec := json.NewDecoder(strings.NewReader(string(bodyBytes)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_body", fmt.Sprintf("malformed JSON body: %v", err))
		return
	}

	// Validation
	if req.Amount < 50 || req.Amount > 99999999 {
		writeError(w, http.StatusBadRequest, "invalid_amount", "amount must be between 50 and 99,999,999 minor units")
		return
	}

	currency := strings.ToLower(strings.TrimSpace(req.Currency))
	if !supportedCurrencies[currency] {
		writeError(w, http.StatusBadRequest, "invalid_currency", "currency must be one of usd, eur, gbp, inr, jpy")
		return
	}

	if req.Metadata == nil {
		req.Metadata = make(map[string]any)
	}
	metadataJSON, err := json.Marshal(req.Metadata)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_metadata", "metadata could not be serialized")
		return
	}

	requestHash := idempotency.ComputeHash("POST", "/v1/payments", bodyBytes)

	var (
		paymentResp *PaymentResponse
		replayed    bool
	)

	// Execute inside a single PostgreSQL transaction
	err = platform.WithTx(r.Context(), h.pool, func(tx pgx.Tx) error {
		txQueries := h.queries.WithTx(tx)

		// 1. Check or lock idempotency key
		record, err := idempotency.CheckOrLock(r.Context(), txQueries, merchant.ID, idempotencyKey, requestHash)
		if err != nil {
			return err
		}

		if record.Replayed {
			replayed = true
			var cached PaymentResponse
			if err := json.Unmarshal(record.ResponseBody, &cached); err == nil {
				paymentResp = &cached
			}
			return nil
		}

		// 2. Generate Payment ID
		paymentID := platform.NewID(platform.PrefixPayment)

		// 3. Insert Payment in 'processing' status
		paymentRow, err := txQueries.CreatePayment(r.Context(), db.CreatePaymentParams{
			ID:           paymentID,
			MerchantID:   merchant.ID,
			Amount:       req.Amount,
			Currency:     currency,
			Status:       "processing",
			Processor:    "simulator",
			ProcessorRef: pgtype.Text{Valid: false},
			FailureCode:  pgtype.Text{Valid: false},
			Metadata:     metadataJSON,
		})
		if err != nil {
			return fmt.Errorf("failed to create payment: %w", err)
		}

		// 4. Record initial payment event
		_, err = txQueries.InsertPaymentEvent(r.Context(), db.InsertPaymentEventParams{
			PaymentID:  paymentRow.ID,
			FromStatus: pgtype.Text{Valid: false},
			ToStatus:   "processing",
			Reason:     pgtype.Text{String: "payment_created", Valid: true},
		})
		if err != nil {
			return fmt.Errorf("failed to record payment event: %w", err)
		}

		// 5. Create outbox event for async processing
		simulateOpt := ""
		if sim, ok := req.Metadata["simulate"].(string); ok {
			simulateOpt = sim
		}

		outboxPayload, _ := json.Marshal(map[string]any{
			"payment_id": paymentRow.ID,
			"amount":     paymentRow.Amount,
			"currency":   paymentRow.Currency,
			"simulate":   simulateOpt,
		})

		_, err = txQueries.CreateOutboxEvent(r.Context(), db.CreateOutboxEventParams{
			MerchantID:  merchant.ID,
			AggregateID: paymentRow.ID,
			Type:        "payment.process",
			Payload:     outboxPayload,
		})
		if err != nil {
			return fmt.Errorf("failed to create outbox event: %w", err)
		}

		// 6. Build response
		paymentResp = formatPaymentResponse(paymentRow)

		respBytes, err := json.Marshal(paymentResp)
		if err != nil {
			return fmt.Errorf("failed to encode response: %w", err)
		}

		// 7. Store response in idempotency record
		return idempotency.SaveResponse(r.Context(), txQueries, merchant.ID, idempotencyKey, http.StatusCreated, respBytes)
	})

	if err != nil {
		if errors.Is(err, idempotency.ErrKeyReusedDifferentPayload) {
			writeError(w, http.StatusUnprocessableEntity, "idempotency_key_reused", "idempotency key was previously used with a different request payload")
			return
		}
		if errors.Is(err, idempotency.ErrConcurrentRequest) {
			writeError(w, http.StatusConflict, "concurrent_request", "a concurrent request with this idempotency key is already in progress")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", fmt.Sprintf("failed to process payment: %v", err))
		return
	}

	if replayed {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(paymentResp)
}

// GetPayment handles GET /v1/payments/{id}.
func (h *PaymentsHandler) GetPayment(w http.ResponseWriter, r *http.Request) {
	merchant, err := MerchantFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthenticated merchant")
		return
	}

	paymentID := r.PathValue("id")
	if paymentID == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "payment id is required")
		return
	}

	payment, err := h.queries.GetPaymentByID(r.Context(), db.GetPaymentByIDParams{
		ID:         paymentID,
		MerchantID: merchant.ID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "payment_not_found", "payment does not exist")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to retrieve payment")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(formatPaymentResponse(payment))
}

// ListPayments handles GET /v1/payments.
func (h *PaymentsHandler) ListPayments(w http.ResponseWriter, r *http.Request) {
	merchant, err := MerchantFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthenticated merchant")
		return
	}

	limit := int32(10)
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			if parsed > 100 {
				parsed = 100
			}
			limit = int32(parsed)
		}
	}

	startingAfter := r.URL.Query().Get("starting_after")
	startingAfterText := pgtype.Text{
		String: startingAfter,
		Valid:  startingAfter != "",
	}

	payments, err := h.queries.ListPayments(r.Context(), db.ListPaymentsParams{
		MerchantID:      merchant.ID,
		Limit:           limit + 1, // Fetch +1 to check has_more
		StartingAfterID: startingAfterText,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list payments")
		return
	}

	hasMore := false
	if int32(len(payments)) > limit {
		hasMore = true
		payments = payments[:limit]
	}

	data := make([]*PaymentResponse, 0, len(payments))
	for _, p := range payments {
		data = append(data, formatPaymentResponse(p))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":     data,
		"has_more": hasMore,
	})
}

func formatPaymentResponse(p db.Payments) *PaymentResponse {
	var meta map[string]any
	if len(p.Metadata) > 0 {
		_ = json.Unmarshal(p.Metadata, &meta)
	}
	if meta == nil {
		meta = make(map[string]any)
	}

	var processorRef *string
	if p.ProcessorRef.Valid {
		ref := p.ProcessorRef.String
		processorRef = &ref
	}

	var failureCode *string
	if p.FailureCode.Valid {
		fc := p.FailureCode.String
		failureCode = &fc
	}

	return &PaymentResponse{
		ID:             p.ID,
		MerchantID:     p.MerchantID,
		Amount:         p.Amount,
		AmountRefunded: p.AmountRefunded,
		Currency:       p.Currency,
		Status:         p.Status,
		Processor:      p.Processor,
		ProcessorRef:   processorRef,
		FailureCode:    failureCode,
		Metadata:       meta,
		CreatedAt:      p.CreatedAt.Time.Format(time.RFC3339),
		UpdatedAt:      p.UpdatedAt.Time.Format(time.RFC3339),
	}
}

// CreateRefund handles POST /v1/payments/{id}/refunds.
func (h *PaymentsHandler) CreateRefund(w http.ResponseWriter, r *http.Request) {
	merchant, err := MerchantFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthenticated merchant")
		return
	}

	paymentID := r.PathValue("id")
	if paymentID == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "payment id is required")
		return
	}

	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key header is required")
		return
	}
	if err := idempotency.ValidateKey(idempotencyKey); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_idempotency_key", err.Error())
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "request_body_too_large", "request body exceeds 1MB limit")
		return
	}

	var req RefundRequest
	dec := json.NewDecoder(strings.NewReader(string(bodyBytes)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_body", fmt.Sprintf("malformed JSON body: %v", err))
		return
	}

	if req.Amount <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_amount", "refund amount must be greater than 0")
		return
	}

	reqHash := idempotency.ComputeHash("POST", "/v1/payments/"+paymentID+"/refunds", bodyBytes)

	var (
		refundResp *RefundResponse
		replayed   bool
		userErr    error
		userCode   string
		userStatus int
	)

	err = platform.WithTx(r.Context(), h.pool, func(tx pgx.Tx) error {
		txQueries := h.queries.WithTx(tx)

		// 1. Check or lock idempotency key
		record, err := idempotency.CheckOrLock(r.Context(), txQueries, merchant.ID, idempotencyKey, reqHash)
		if err != nil {
			return err
		}

		if record.Replayed {
			replayed = true
			var cached RefundResponse
			if err := json.Unmarshal(record.ResponseBody, &cached); err == nil {
				refundResp = &cached
			}
			return nil
		}

		// 2. Lock payment row to prevent concurrent over-refunds
		payment, err := txQueries.GetPaymentByIDForUpdate(r.Context(), db.GetPaymentByIDForUpdateParams{
			ID:         paymentID,
			MerchantID: merchant.ID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				userStatus = http.StatusNotFound
				userCode = "payment_not_found"
				userErr = errors.New("payment not found")
				return userErr
			}
			return fmt.Errorf("failed to lock payment: %w", err)
		}

		// 3. Status and balance validation
		if payment.Status != "succeeded" && payment.Status != "partially_refunded" {
			userStatus = http.StatusBadRequest
			userCode = "invalid_payment_status"
			userErr = fmt.Errorf("payment status '%s' cannot be refunded", payment.Status)
			return userErr
		}

		if payment.AmountRefunded+req.Amount > payment.Amount {
			userStatus = http.StatusBadRequest
			userCode = "refund_exceeds_balance"
			userErr = fmt.Errorf("refund amount %d exceeds refundable balance of %d", req.Amount, payment.Amount-payment.AmountRefunded)
			return userErr
		}

		// 4. Create refund record
		refundID := platform.NewID(platform.PrefixRefund)
		refundRow, err := txQueries.CreateRefund(r.Context(), db.CreateRefundParams{
			ID:        refundID,
			PaymentID: payment.ID,
			Amount:    req.Amount,
		})
		if err != nil {
			return fmt.Errorf("failed to create refund record: %w", err)
		}

		// 5. Update payment refunded amount & status
		updatedPayment, err := txQueries.UpdatePaymentRefund(r.Context(), db.UpdatePaymentRefundParams{
			ID:           payment.ID,
			MerchantID:   merchant.ID,
			RefundAmount: req.Amount,
		})
		if err != nil {
			return fmt.Errorf("failed to update payment refund: %w", err)
		}

		// 6. Record payment event
		_, err = txQueries.InsertPaymentEvent(r.Context(), db.InsertPaymentEventParams{
			PaymentID:  payment.ID,
			FromStatus: pgtype.Text{String: payment.Status, Valid: true},
			ToStatus:   updatedPayment.Status,
			Reason:     pgtype.Text{String: "refund_issued", Valid: true},
		})
		if err != nil {
			return fmt.Errorf("failed to record payment event: %w", err)
		}

		// 7. Post double-entry refund ledger entries
		err = h.ledgerService.PostRefund(r.Context(), txQueries, refundRow.ID, payment.ID, merchant.ID, payment.Currency, req.Amount)
		if err != nil {
			return fmt.Errorf("failed to post refund ledger: %w", err)
		}

		// 8. Enqueue outbox event for webhooks
		outboxPayload, _ := json.Marshal(map[string]any{
			"refund_id":  refundRow.ID,
			"payment_id": payment.ID,
			"amount":     req.Amount,
			"currency":   payment.Currency,
			"status":     "succeeded",
		})

		_, err = txQueries.CreateOutboxEvent(r.Context(), db.CreateOutboxEventParams{
			MerchantID:  merchant.ID,
			AggregateID: refundRow.ID,
			Type:        "refund.succeeded",
			Payload:     outboxPayload,
		})
		if err != nil {
			return fmt.Errorf("failed to enqueue refund outbox event: %w", err)
		}

		// 9. Format response and cache in idempotency store
		refundResp = &RefundResponse{
			ID:        refundRow.ID,
			PaymentID: payment.ID,
			Amount:    refundRow.Amount,
			Currency:  payment.Currency,
			Status:    "succeeded",
			CreatedAt: refundRow.CreatedAt.Time.Format(time.RFC3339),
		}

		respBytes, err := json.Marshal(refundResp)
		if err != nil {
			return fmt.Errorf("failed to marshal refund response: %w", err)
		}

		return idempotency.SaveResponse(r.Context(), txQueries, merchant.ID, idempotencyKey, http.StatusCreated, respBytes)
	})

	if userErr != nil {
		writeError(w, userStatus, userCode, userErr.Error())
		return
	}

	if err != nil {
		if errors.Is(err, idempotency.ErrKeyReusedDifferentPayload) {
			writeError(w, http.StatusUnprocessableEntity, "idempotency_key_reused", "idempotency key was previously used with a different request payload")
			return
		}
		if errors.Is(err, idempotency.ErrConcurrentRequest) {
			writeError(w, http.StatusConflict, "concurrent_request", "a concurrent request with this idempotency key is already in progress")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", fmt.Sprintf("failed to process refund: %v", err))
		return
	}

	if replayed {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(refundResp)
}

// ListRefunds handles GET /v1/payments/{id}/refunds.
func (h *PaymentsHandler) ListRefunds(w http.ResponseWriter, r *http.Request) {
	merchant, err := MerchantFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthenticated merchant")
		return
	}

	paymentID := r.PathValue("id")
	if paymentID == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "payment id is required")
		return
	}

	// Verify payment belongs to merchant
	payment, err := h.queries.GetPaymentByID(r.Context(), db.GetPaymentByIDParams{
		ID:         paymentID,
		MerchantID: merchant.ID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "payment_not_found", "payment does not exist")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to retrieve payment")
		return
	}

	refunds, err := h.queries.ListRefundsForPayment(r.Context(), paymentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list refunds")
		return
	}

	data := make([]RefundResponse, 0, len(refunds))
	for _, rf := range refunds {
		data = append(data, RefundResponse{
			ID:        rf.ID,
			PaymentID: rf.PaymentID,
			Amount:    rf.Amount,
			Currency:  payment.Currency,
			Status:    "succeeded",
			CreatedAt: rf.CreatedAt.Time.Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": data,
	})
}
