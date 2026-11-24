package test_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/minipay/minipay/internal/api"
	"github.com/minipay/minipay/internal/ledger"
	"github.com/minipay/minipay/internal/outbox"
	"github.com/minipay/minipay/internal/platform"
	"github.com/minipay/minipay/internal/platform/db"
	"github.com/minipay/minipay/internal/processor"
	"github.com/minipay/minipay/internal/ratelimit"
	"github.com/minipay/minipay/internal/webhooks"
)

func TestRateLimitingHeadersAndThrottling(t *testing.T) {
	devPolicy := webhooks.SecurityPolicy{AllowPrivate: true, AllowInsecure: true}
	pool, queries, _, apiKey, _ := setupPhase2Environment(t, devPolicy)

	// Build router with a strict rate limiter: capacity 3, refill 1/sec
	strictLimiter := ratelimit.NewMemoryLimiter()
	rateLimitMw := ratelimit.Middleware(strictLimiter, 3, 1)

	mux := http.NewServeMux()
	paymentsHandler := api.NewPaymentsHandler(pool, queries)
	authMw := api.Authenticate(queries)

	mux.Handle("POST /v1/payments", authMw(rateLimitMw(http.HandlerFunc(paymentsHandler.CreatePayment))))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// 1. Issue 3 requests within limit -> all succeed with 201 and rate limit headers
	for i := 1; i <= 3; i++ {
		payKey := fmt.Sprintf("rl-ok-%d-%d", i, time.Now().UnixNano())
		req, _ := http.NewRequest("POST", ts.URL+"/v1/payments", bytes.NewReader([]byte(`{"amount":1000,"currency":"usd"}`)))
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Idempotency-Key", payKey)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("req %d failed: %v", i, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("req %d: expected 201 Created, got %d", i, resp.StatusCode)
		}

		limitH := resp.Header.Get("X-RateLimit-Limit")
		remH := resp.Header.Get("X-RateLimit-Remaining")
		resetH := resp.Header.Get("X-RateLimit-Reset")

		if limitH != "3" {
			t.Errorf("req %d: expected X-RateLimit-Limit=3, got %s", i, limitH)
		}
		if remH == "" || resetH == "" {
			t.Errorf("req %d: missing rate limit headers (rem: %s, reset: %s)", i, remH, resetH)
		}
	}

	// 2. 4th request exceeds burst capacity -> 429 Too Many Requests
	payKey := fmt.Sprintf("rl-exceed-%d", time.Now().UnixNano())
	req, _ := http.NewRequest("POST", ts.URL+"/v1/payments", bytes.NewReader([]byte(`{"amount":1000,"currency":"usd"}`)))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Idempotency-Key", payKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("4th request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 429 Too Many Requests, got %d: %s", resp.StatusCode, string(body))
	}

	remH := resp.Header.Get("X-RateLimit-Remaining")
	if remH != "0" {
		t.Errorf("expected X-RateLimit-Remaining=0, got %s", remH)
	}
}

func TestRiskEngineAmountCapAndReviews(t *testing.T) {
	devPolicy := webhooks.SecurityPolicy{AllowPrivate: true, AllowInsecure: true}
	pool, queries, merchant, apiKey, ts := setupPhase2Environment(t, devPolicy)
	ctx := context.Background()

	// 1. Amount < $500 ($100 = 10000 minor units) -> allowed -> status 'processing'
	payKey1 := fmt.Sprintf("pay-risk-allow-%d", time.Now().UnixNano())
	req1, _ := http.NewRequest("POST", ts.URL+"/v1/payments", bytes.NewReader([]byte(`{"amount":10000,"currency":"usd"}`)))
	req1.Header.Set("Authorization", "Bearer "+apiKey)
	req1.Header.Set("Idempotency-Key", payKey1)

	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("payment 1 failed: %v", err)
	}
	defer resp1.Body.Close()

	var p1 api.PaymentResponse
	_ = json.NewDecoder(resp1.Body).Decode(&p1)
	if p1.Status != "processing" {
		t.Fatalf("expected status 'processing' for $100, got %q", p1.Status)
	}

	// Check risk decision recorded
	riskReq, _ := http.NewRequest("GET", fmt.Sprintf("%s/v1/payments/%s/risk", ts.URL, p1.ID), nil)
	riskReq.Header.Set("Authorization", "Bearer "+apiKey)
	riskResp, err := http.DefaultClient.Do(riskReq)
	if err != nil || riskResp.StatusCode != http.StatusOK {
		t.Fatalf("failed to query risk decisions: %v (status: %d)", err, riskResp.StatusCode)
	}
	defer riskResp.Body.Close()

	var riskData struct {
		Data []struct {
			Rule    string `json:"rule"`
			Outcome string `json:"outcome"`
		} `json:"data"`
	}
	_ = json.NewDecoder(riskResp.Body).Decode(&riskData)
	if len(riskData.Data) == 0 {
		t.Errorf("expected recorded risk decisions for payment %s", p1.ID)
	}

	// 2. Amount > $500, < $5000 ($600 = 60000 minor units) -> requires_review
	payKey2 := fmt.Sprintf("pay-risk-review-%d", time.Now().UnixNano())
	req2, _ := http.NewRequest("POST", ts.URL+"/v1/payments", bytes.NewReader([]byte(`{"amount":60000,"currency":"usd"}`)))
	req2.Header.Set("Authorization", "Bearer "+apiKey)
	req2.Header.Set("Idempotency-Key", payKey2)

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("payment 2 failed: %v", err)
	}
	defer resp2.Body.Close()

	var p2 api.PaymentResponse
	_ = json.NewDecoder(resp2.Body).Decode(&p2)
	if p2.Status != "requires_review" {
		t.Fatalf("expected status 'requires_review' for $600, got %q", p2.Status)
	}

	// Verify worker does NOT process requires_review payment
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ledgerService := ledger.NewService(queries)
	psp := processor.NewSimulator()
	worker := outbox.NewWorker(pool, queries, ledgerService, psp, platform.RealClock{}, logger)
	_, _ = worker.ProcessBatch(ctx)

	p2Check, err := queries.GetPaymentByID(ctx, db.GetPaymentByIDParams{ID: p2.ID, MerchantID: merchant.ID})
	if err != nil {
		t.Fatalf("failed to reload payment: %v", err)
	}
	if p2Check.Status != "requires_review" {
		t.Fatalf("payment should remain in requires_review before approval, got %s", p2Check.Status)
	}

	// 3. Admin Approves the Payment
	err = platform.WithTx(ctx, pool, func(tx pgx.Tx) error {
		txQ := queries.WithTx(tx)
		_, err := txQ.UpdatePaymentStatus(ctx, db.UpdatePaymentStatusParams{
			ID:           p2.ID,
			FromStatus:   "requires_review",
			ToStatus:     "processing",
			ProcessorRef: pgtype.Text{Valid: false},
			FailureCode:  pgtype.Text{Valid: false},
		})
		if err != nil {
			return err
		}

		outboxPayload, _ := json.Marshal(map[string]any{
			"payment_id": p2.ID,
			"amount":     p2.Amount,
			"currency":   p2.Currency,
		})
		_, err = txQ.CreateOutboxEvent(ctx, db.CreateOutboxEventParams{
			MerchantID:  merchant.ID,
			AggregateID: p2.ID,
			Type:        "payment.process",
			Payload:     outboxPayload,
		})
		return err
	})
	if err != nil {
		t.Fatalf("failed admin approval: %v", err)
	}

	// Worker now processes the approved payment
	for i := 0; i < 3; i++ {
		_, _ = worker.ProcessBatch(ctx)
	}

	p2Final, err := queries.GetPaymentByID(ctx, db.GetPaymentByIDParams{ID: p2.ID, MerchantID: merchant.ID})
	if err != nil || p2Final.Status != "succeeded" {
		t.Fatalf("expected status 'succeeded' after admin approval, got %q (err: %v)", p2Final.Status, err)
	}

	// 4. Amount >= $5000 ($6000 = 600000 minor units) -> blocked (status 'failed', failure_code 'risk_blocked')
	payKey3 := fmt.Sprintf("pay-risk-block-%d", time.Now().UnixNano())
	req3, _ := http.NewRequest("POST", ts.URL+"/v1/payments", bytes.NewReader([]byte(`{"amount":600000,"currency":"usd"}`)))
	req3.Header.Set("Authorization", "Bearer "+apiKey)
	req3.Header.Set("Idempotency-Key", payKey3)

	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatalf("payment 3 failed: %v", err)
	}
	defer resp3.Body.Close()

	var p3 api.PaymentResponse
	_ = json.NewDecoder(resp3.Body).Decode(&p3)
	if p3.Status != "failed" {
		t.Fatalf("expected status 'failed' for $6000, got %q", p3.Status)
	}
	if p3.FailureCode == nil || *p3.FailureCode != "risk_blocked" {
		t.Fatalf("expected failure_code 'risk_blocked', got %v", p3.FailureCode)
	}
}

func TestPrometheusMetricsScrape(t *testing.T) {
	devPolicy := webhooks.SecurityPolicy{AllowPrivate: true, AllowInsecure: true}
	_, _, _, _, ts := setupPhase2Environment(t, devPolicy)

	// Scrape /metrics
	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("failed to scrape /metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from /metrics, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Errorf("expected Content-Type text/plain, got %s", ct)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read /metrics body: %v", err)
	}

	bodyStr := string(body)
	if !strings.Contains(bodyStr, "http_requests_total") {
		t.Errorf("expected http_requests_total in metrics output:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, "payments_total") {
		t.Errorf("expected payments_total in metrics output:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, "ledger_invariants_violations") {
		t.Errorf("expected ledger_invariants_violations in metrics output:\n%s", bodyStr)
	}
}
