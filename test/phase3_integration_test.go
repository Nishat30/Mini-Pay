package test_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/minipay/minipay/internal/api"
	"github.com/minipay/minipay/internal/ledger"
	"github.com/minipay/minipay/internal/outbox"
	"github.com/minipay/minipay/internal/platform"
	"github.com/minipay/minipay/internal/platform/db"
	"github.com/minipay/minipay/internal/processor"
	"github.com/minipay/minipay/internal/webhooks"
)

func TestPartialAndFullRefund(t *testing.T) {
	devPolicy := webhooks.SecurityPolicy{AllowPrivate: true, AllowInsecure: true}
	pool, queries, _, apiKey, ts := setupPhase2Environment(t, devPolicy)
	ctx := context.Background()

	// 1. Create payment for 10000 ($100.00)
	payKey := fmt.Sprintf("pay-rf-%d", time.Now().UnixNano())
	payReq, _ := http.NewRequest("POST", ts.URL+"/v1/payments", bytes.NewReader([]byte(`{"amount":10000,"currency":"usd"}`)))
	payReq.Header.Set("Authorization", "Bearer "+apiKey)
	payReq.Header.Set("Idempotency-Key", payKey)

	payResp, err := http.DefaultClient.Do(payReq)
	if err != nil {
		t.Fatalf("failed to create payment: %v", err)
	}
	defer payResp.Body.Close()
	if payResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d", payResp.StatusCode)
	}

	var payment api.PaymentResponse
	_ = json.NewDecoder(payResp.Body).Decode(&payment)

	// Process payment to succeeded via worker
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ledgerService := ledger.NewService(queries)
	psp := processor.NewSimulator()
	worker := outbox.NewWorker(pool, queries, ledgerService, psp, platform.RealClock{}, logger)
	for i := 0; i < 5; i++ {
		_, _ = worker.ProcessBatch(ctx)
	}

	// 2. Partial Refund of 4000 ($40.00)
	refKey1 := fmt.Sprintf("ref-partial-%d", time.Now().UnixNano())
	refReq1, _ := http.NewRequest("POST", fmt.Sprintf("%s/v1/payments/%s/refunds", ts.URL, payment.ID), bytes.NewReader([]byte(`{"amount":4000,"reason":"customer_return"}`)))
	refReq1.Header.Set("Authorization", "Bearer "+apiKey)
	refReq1.Header.Set("Idempotency-Key", refKey1)

	refResp1, err := http.DefaultClient.Do(refReq1)
	if err != nil {
		t.Fatalf("partial refund request failed: %v", err)
	}
	defer refResp1.Body.Close()

	if refResp1.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(refResp1.Body)
		t.Fatalf("expected 201 Created for partial refund, got %d: %s", refResp1.StatusCode, string(b))
	}

	// Verify payment row in DB
	p1, err := queries.GetPaymentByID(ctx, db.GetPaymentByIDParams{
		ID:         payment.ID,
		MerchantID: payment.MerchantID,
	})
	if err != nil {
		t.Fatalf("failed to query payment: %v", err)
	}
	if p1.AmountRefunded != 4000 {
		t.Errorf("expected amount_refunded 4000, got %d", p1.AmountRefunded)
	}
	if p1.Status != "succeeded" {
		t.Errorf("status should remain succeeded on partial refund, got %s", p1.Status)
	}

	// 3. Second Refund of remaining 6000 ($60.00) -> Full Refund
	refKey2 := fmt.Sprintf("ref-full-%d", time.Now().UnixNano())
	refReq2, _ := http.NewRequest("POST", fmt.Sprintf("%s/v1/payments/%s/refunds", ts.URL, payment.ID), bytes.NewReader([]byte(`{"amount":6000}`)))
	refReq2.Header.Set("Authorization", "Bearer "+apiKey)
	refReq2.Header.Set("Idempotency-Key", refKey2)

	refResp2, err := http.DefaultClient.Do(refReq2)
	if err != nil {
		t.Fatalf("full refund request failed: %v", err)
	}
	defer refResp2.Body.Close()

	if refResp2.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(refResp2.Body)
		t.Fatalf("expected 201 Created for full refund, got %d: %s", refResp2.StatusCode, string(b))
	}

	// Verify payment row in DB now transitioned to 'refunded'
	p2, err := queries.GetPaymentByID(ctx, db.GetPaymentByIDParams{
		ID:         payment.ID,
		MerchantID: payment.MerchantID,
	})
	if err != nil {
		t.Fatalf("failed to query payment: %v", err)
	}
	if p2.AmountRefunded != 10000 {
		t.Errorf("expected amount_refunded 10000, got %d", p2.AmountRefunded)
	}
	if p2.Status != "refunded" {
		t.Errorf("status should transition to refunded, got %s", p2.Status)
	}

	// 4. Verify Ledger Invariants
	violations, err := ledgerService.CheckInvariants(ctx)
	if err != nil {
		t.Fatalf("invariant check failed: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("found invariant violations: %+v", violations)
	}
}

func TestConcurrentRefundRaceCondition(t *testing.T) {
	devPolicy := webhooks.SecurityPolicy{AllowPrivate: true, AllowInsecure: true}
	pool, queries, _, apiKey, ts := setupPhase2Environment(t, devPolicy)
	ctx := context.Background()

	// 1. Create payment for 1000 ($10.00)
	payKey := fmt.Sprintf("pay-race-%d", time.Now().UnixNano())
	payReq, _ := http.NewRequest("POST", ts.URL+"/v1/payments", bytes.NewReader([]byte(`{"amount":1000,"currency":"usd"}`)))
	payReq.Header.Set("Authorization", "Bearer "+apiKey)
	payReq.Header.Set("Idempotency-Key", payKey)

	payResp, err := http.DefaultClient.Do(payReq)
	if err != nil {
		t.Fatalf("failed to create payment: %v", err)
	}
	defer payResp.Body.Close()

	var payment api.PaymentResponse
	_ = json.NewDecoder(payResp.Body).Decode(&payment)

	// Process to succeeded
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ledgerService := ledger.NewService(queries)
	psp := processor.NewSimulator()
	worker := outbox.NewWorker(pool, queries, ledgerService, psp, platform.RealClock{}, logger)
	for i := 0; i < 5; i++ {
		_, _ = worker.ProcessBatch(ctx)
	}

	// 2. Launch 10 simultaneous refund requests of 200 ($2.00)
	// Total requested: 10 * 200 = 2000 on a 1000 payment.
	// EXACTLY 5 must succeed (200 * 5 = 1000), and 5 must fail with 400.
	const totalReps = 10
	var wg sync.WaitGroup
	wg.Add(totalReps)

	var (
		successCount atomic.Int32
		failCount    atomic.Int32
	)

	for i := 0; i < totalReps; i++ {
		go func(idx int) {
			defer wg.Done()
			refKey := fmt.Sprintf("race-ref-%d-%d", time.Now().UnixNano(), idx)
			req, _ := http.NewRequest("POST", fmt.Sprintf("%s/v1/payments/%s/refunds", ts.URL, payment.ID), bytes.NewReader([]byte(`{"amount":200}`)))
			req.Header.Set("Authorization", "Bearer "+apiKey)
			req.Header.Set("Idempotency-Key", refKey)

			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				if resp.StatusCode == http.StatusCreated {
					successCount.Add(1)
				} else if resp.StatusCode == http.StatusBadRequest {
					failCount.Add(1)
				}
				resp.Body.Close()
			}
		}(i)
	}

	wg.Wait()

	t.Logf("Race result: %d succeeded, %d failed", successCount.Load(), failCount.Load())

	if successCount.Load() != 5 {
		t.Errorf("expected exactly 5 refunds to succeed, got %d", successCount.Load())
	}
	if failCount.Load() != 5 {
		t.Errorf("expected exactly 5 refunds to be rejected with 400, got %d", failCount.Load())
	}

	// Verify DB state: total amount_refunded MUST BE exactly 1000
	p, err := queries.GetPaymentByID(ctx, db.GetPaymentByIDParams{
		ID:         payment.ID,
		MerchantID: payment.MerchantID,
	})
	if err != nil {
		t.Fatalf("failed to query payment: %v", err)
	}

	if p.AmountRefunded != 1000 {
		t.Fatalf("OVER-REFUND VIOLATION: amount_refunded is %d, expected exactly 1000", p.AmountRefunded)
	}
	if p.Status != "refunded" {
		t.Errorf("status should be refunded, got %s", p.Status)
	}
}
