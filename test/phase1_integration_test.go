package test_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/minipay/minipay/internal/api"
	"github.com/minipay/minipay/internal/ledger"
	"github.com/minipay/minipay/internal/outbox"
	"github.com/minipay/minipay/internal/platform"
	"github.com/minipay/minipay/internal/platform/db"
	"github.com/minipay/minipay/internal/processor"
)

func setupTestEnvironment(t *testing.T) (*pgxpool.Pool, *db.Queries, *db.Merchants, string, *httptest.Server) {
	t.Helper()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://minipay:minipay@localhost:5432/minipay?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := platform.ConnectDB(ctx, dbURL)
	if err != nil {
		t.Skipf("skipping integration test: postgres not reachable at %s: %v", dbURL, err)
	}

	queries := db.New(pool)

	// Create test merchant
	apiKey := fmt.Sprintf("mp_test_%d", time.Now().UnixNano())
	hash := sha256.Sum256([]byte(apiKey))
	merchantID := platform.NewID(platform.PrefixMerchant)

	merchant, err := queries.CreateMerchant(context.Background(), db.CreateMerchantParams{
		ID:            merchantID,
		Name:          "Integration Test Merchant",
		ApiKeyHash:    hash[:],
		WebhookSecret: "whsec_test",
	})
	if err != nil {
		t.Fatalf("failed to create test merchant: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := api.NewRouter(logger, pool, nil)
	ts := httptest.NewServer(router)

	t.Cleanup(func() {
		ts.Close()
		pool.Close()
	})

	return pool, queries, &merchant, apiKey, ts
}

func TestIdempotentReplay(t *testing.T) {
	_, _, _, apiKey, ts := setupTestEnvironment(t)

	key := fmt.Sprintf("key-replay-%d", time.Now().UnixNano())
	reqBody := []byte(`{"amount":1000,"currency":"usd","metadata":{"order_id":"123"}}`)

	// First Request
	req1, _ := http.NewRequest("POST", ts.URL+"/v1/payments", bytes.NewReader(reqBody))
	req1.Header.Set("Authorization", "Bearer "+apiKey)
	req1.Header.Set("Idempotency-Key", key)
	req1.Header.Set("Content-Type", "application/json")

	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("request 1 failed: %v", err)
	}
	defer resp1.Body.Close()

	if resp1.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp1.Body)
		t.Fatalf("expected 201 Created, got %d: %s", resp1.StatusCode, string(body))
	}

	body1, _ := io.ReadAll(resp1.Body)
	if resp1.Header.Get("Idempotent-Replayed") == "true" {
		t.Errorf("first response should not have Idempotent-Replayed: true")
	}

	var res1 api.PaymentResponse
	if err := json.Unmarshal(body1, &res1); err != nil {
		t.Fatalf("failed to unmarshal response 1: %v", err)
	}

	// Second Request with identical payload and key
	req2, _ := http.NewRequest("POST", ts.URL+"/v1/payments", bytes.NewReader(reqBody))
	req2.Header.Set("Authorization", "Bearer "+apiKey)
	req2.Header.Set("Idempotency-Key", key)
	req2.Header.Set("Content-Type", "application/json")

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("request 2 failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("expected 201 Created on replay, got %d: %s", resp2.StatusCode, string(body))
	}

	if resp2.Header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("second response MUST have Idempotent-Replayed: true header")
	}

	body2, _ := io.ReadAll(resp2.Body)
	var res2 api.PaymentResponse
	if err := json.Unmarshal(body2, &res2); err != nil {
		t.Fatalf("failed to unmarshal response 2: %v", err)
	}

	if res1.ID != res2.ID {
		t.Errorf("replayed payment ID mismatch: %s != %s", res1.ID, res2.ID)
	}
	if res1.Amount != res2.Amount {
		t.Errorf("amount mismatch: %d != %d", res1.Amount, res2.Amount)
	}
}

func TestIdempotencyKeyReuseDifferentPayload(t *testing.T) {
	_, _, _, apiKey, ts := setupTestEnvironment(t)

	key := fmt.Sprintf("key-reuse-%d", time.Now().UnixNano())

	// First Request: 1000 usd
	req1, _ := http.NewRequest("POST", ts.URL+"/v1/payments", bytes.NewReader([]byte(`{"amount":1000,"currency":"usd"}`)))
	req1.Header.Set("Authorization", "Bearer "+apiKey)
	req1.Header.Set("Idempotency-Key", key)

	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("request 1 failed: %v", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d", resp1.StatusCode)
	}

	// Second Request with SAME key but DIFFERENT payload (2000 usd)
	req2, _ := http.NewRequest("POST", ts.URL+"/v1/payments", bytes.NewReader([]byte(`{"amount":2000,"currency":"usd"}`)))
	req2.Header.Set("Authorization", "Bearer "+apiKey)
	req2.Header.Set("Idempotency-Key", key)

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("request 2 failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusUnprocessableEntity {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("expected 422 Unprocessable Entity, got %d: %s", resp2.StatusCode, string(body))
	}
}

func TestDuplicateStorm(t *testing.T) {
	pool, _, merchant, apiKey, ts := setupTestEnvironment(t)

	key := fmt.Sprintf("key-storm-%d", time.Now().UnixNano())
	reqBody := `{"amount":1500,"currency":"eur"}`

	const concurrency = 10
	var wg sync.WaitGroup
	wg.Add(concurrency)

	statusCodes := make([]int, concurrency)

	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()
			req, _ := http.NewRequest("POST", ts.URL+"/v1/payments", bytes.NewReader([]byte(reqBody)))
			req.Header.Set("Authorization", "Bearer "+apiKey)
			req.Header.Set("Idempotency-Key", key)
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				statusCodes[idx] = resp.StatusCode
				resp.Body.Close()
			}
		}(i)
	}

	wg.Wait()

	// Verify database state: EXACTLY 1 payment must exist for this key
	var count int
	err := pool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM payments WHERE merchant_id = $1 AND amount = 1500 AND currency = 'eur'",
		merchant.ID).Scan(&count)
	if err != nil {
		t.Fatalf("failed to query payments count: %v", err)
	}

	if count != 1 {
		t.Errorf("duplicate storm violation: expected exactly 1 payment in DB, got %d", count)
	}
}

func TestParallelDistinctKeys(t *testing.T) {
	pool, _, merchant, apiKey, ts := setupTestEnvironment(t)

	const count = 10
	var wg sync.WaitGroup
	wg.Add(count)

	for i := 0; i < count; i++ {
		go func(idx int) {
			defer wg.Done()
			key := fmt.Sprintf("key-distinct-%d-%d", time.Now().UnixNano(), idx)
			reqBody := fmt.Sprintf(`{"amount":%d,"currency":"usd"}`, 100+idx)
			req, _ := http.NewRequest("POST", ts.URL+"/v1/payments", bytes.NewReader([]byte(reqBody)))
			req.Header.Set("Authorization", "Bearer "+apiKey)
			req.Header.Set("Idempotency-Key", key)

			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				resp.Body.Close()
			}
		}(i)
	}

	wg.Wait()

	var paymentCount int
	err := pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM payments WHERE merchant_id = $1", merchant.ID).Scan(&paymentCount)
	if err != nil {
		t.Fatalf("failed to query payments count: %v", err)
	}

	if paymentCount != count {
		t.Errorf("expected %d distinct payments, got %d", count, paymentCount)
	}
}

func TestLedgerTriggers(t *testing.T) {
	pool, queries, merchant, _, _ := setupTestEnvironment(t)
	ctx := context.Background()

	// Create test payment
	paymentID := platform.NewID(platform.PrefixPayment)
	_, err := queries.CreatePayment(ctx, db.CreatePaymentParams{
		ID:         paymentID,
		MerchantID: merchant.ID,
		Amount:     1000,
		Currency:   "usd",
		Status:     "processing",
		Processor:  "simulator",
		Metadata:   []byte("{}"),
	})
	if err != nil {
		t.Fatalf("failed to create payment: %v", err)
	}

	// 1. Unbalanced trigger test: attempt to insert unbalanced transaction
	txID := platform.NewID(platform.PrefixLedgerTransaction)
	ref := fmt.Sprintf("test-unbalanced-%d", time.Now().UnixNano())

	ledgerService := ledger.NewService(queries)
	accounts, err := ledgerService.EnsureAccounts(ctx, merchant.ID, "usd")
	if err != nil {
		t.Fatalf("failed to ensure accounts: %v", err)
	}

	err = platform.WithTx(ctx, pool, func(tx pgx.Tx) error {
		txQueries := queries.WithTx(tx)
		_, err := txQueries.CreateLedgerTransaction(ctx, db.CreateLedgerTransactionParams{
			ID:        txID,
			PaymentID: paymentID,
			Kind:      "capture",
			Reference: ref,
		})
		if err != nil {
			return err
		}

		// Insert only DEBIT without matching CREDIT (unbalanced)
		_, err = txQueries.CreateLedgerEntry(ctx, db.CreateLedgerEntryParams{
			TransactionID: txID,
			AccountID:     accounts.ClearingAccountID,
			Direction:     "debit",
			Amount:        1000,
			Currency:      "usd",
		})
		return err
	})

	if err == nil {
		t.Fatalf("expected trigger to reject unbalanced transaction, but transaction succeeded!")
	}
	t.Logf("assert_ledger_balanced trigger properly caught unbalanced transaction: %v", err)

	// 2. Immutability trigger test: attempt to update or delete a payment_events row
	event, err := queries.InsertPaymentEvent(ctx, db.InsertPaymentEventParams{
		PaymentID:  paymentID,
		FromStatus: pgtype.Text{String: "processing", Valid: true},
		ToStatus:   "succeeded",
		Reason:     pgtype.Text{String: "test", Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to insert payment event: %v", err)
	}

	_, err = pool.Exec(ctx, "DELETE FROM payment_events WHERE id = $1", event.ID)
	if err == nil {
		t.Fatalf("expected forbid_mutation trigger to block DELETE on payment_events, but delete succeeded!")
	}
	t.Logf("forbid_mutation trigger properly blocked DELETE on payment_events: %v", err)

	_, err = pool.Exec(ctx, "UPDATE payment_events SET reason = 'tampered' WHERE id = $1", event.ID)
	if err == nil {
		t.Fatalf("expected forbid_mutation trigger to block UPDATE on payment_events, but update succeeded!")
	}
	t.Logf("forbid_mutation trigger properly blocked UPDATE on payment_events: %v", err)
}

func TestWorkerEndToEnd(t *testing.T) {
	pool, queries, _, apiKey, ts := setupTestEnvironment(t)
	ctx := context.Background()

	key := fmt.Sprintf("key-e2e-%d", time.Now().UnixNano())
	reqBody := `{"amount":1000,"currency":"usd","metadata":{"order_id":"order_999"}}`

	// 1. Submit Payment Request
	req, _ := http.NewRequest("POST", ts.URL+"/v1/payments", bytes.NewReader([]byte(reqBody)))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Idempotency-Key", key)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 201 Created, got %d: %s", resp.StatusCode, string(b))
	}

	var payment api.PaymentResponse
	_ = json.NewDecoder(resp.Body).Decode(&payment)

	if payment.Status != "processing" {
		t.Errorf("initial payment status should be processing, got %s", payment.Status)
	}

	// 2. Run Worker Batch until event is processed
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ledgerService := ledger.NewService(queries)
	psp := processor.NewSimulator()
	w := outbox.NewWorker(pool, queries, ledgerService, psp, platform.RealClock{}, logger)

	var totalProcessed int
	for i := 0; i < 10; i++ {
		n, err := w.ProcessBatch(ctx)
		if err != nil {
			t.Fatalf("worker process batch failed: %v", err)
		}
		totalProcessed += n
		if n == 0 {
			break
		}
	}
	if totalProcessed == 0 {
		t.Fatalf("expected worker to process at least 1 outbox event")
	}

	// 3. Verify Payment transitioned to succeeded in DB
	updatedPayment, err := queries.GetPaymentByID(ctx, db.GetPaymentByIDParams{
		ID:         payment.ID,
		MerchantID: payment.MerchantID,
	})
	if err != nil {
		t.Fatalf("failed to retrieve updated payment: %v", err)
	}

	if updatedPayment.Status != "succeeded" {
		t.Errorf("payment did not transition to succeeded, status is: %s", updatedPayment.Status)
	}
	if !updatedPayment.ProcessorRef.Valid || updatedPayment.ProcessorRef.String == "" {
		t.Errorf("processor_ref was not set on payment")
	}

	// 4. Verify Ledger Transaction
	ref := fmt.Sprintf("capture:%s", payment.ID)
	ltx, err := queries.GetLedgerTransactionByRef(ctx, ref)
	if err != nil {
		t.Fatalf("ledger transaction %s not found: %v", ref, err)
	}
	if ltx.Kind != "capture" {
		t.Errorf("expected kind capture, got %s", ltx.Kind)
	}

	// 5. Invariant check
	violations, err := ledgerService.CheckInvariants(ctx)
	if err != nil {
		t.Fatalf("invariant check failed: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("found %d invariant violations: %+v", len(violations), violations)
	}
}
