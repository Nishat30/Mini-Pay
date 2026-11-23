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
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/minipay/minipay/internal/api"
	"github.com/minipay/minipay/internal/ledger"
	"github.com/minipay/minipay/internal/outbox"
	"github.com/minipay/minipay/internal/platform"
	"github.com/minipay/minipay/internal/platform/db"
	"github.com/minipay/minipay/internal/processor"
	"github.com/minipay/minipay/internal/webhooks"
)

func setupPhase2Environment(t *testing.T, policy webhooks.SecurityPolicy) (*pgxpool.Pool, *db.Queries, *db.Merchants, string, *httptest.Server) {
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

	apiKey := fmt.Sprintf("mp_test_p2_%d", time.Now().UnixNano())
	hash := sha256.Sum256([]byte(apiKey))
	merchantID := platform.NewID(platform.PrefixMerchant)
	whsec := fmt.Sprintf("whsec_test_%d", time.Now().UnixNano())

	merchant, err := queries.CreateMerchant(context.Background(), db.CreateMerchantParams{
		ID:            merchantID,
		Name:          "Phase 2 Merchant",
		ApiKeyHash:    hash[:],
		WebhookSecret: whsec,
	})
	if err != nil {
		t.Fatalf("failed to create test merchant: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := api.NewRouter(logger, pool, nil, policy)
	ts := httptest.NewServer(router)

	t.Cleanup(func() {
		ts.Close()
		pool.Close()
	})

	return pool, queries, &merchant, apiKey, ts
}

func TestWebhookDeliverySuccess(t *testing.T) {
	devPolicy := webhooks.SecurityPolicy{AllowPrivate: true, AllowInsecure: true}
	pool, queries, merchant, apiKey, ts := setupPhase2Environment(t, devPolicy)
	ctx := context.Background()

	var (
		receivedBody   []byte
		receivedSig    string
		receivedDelID  string
		receivedCount  atomic.Int32
		receiverServer *httptest.Server
	)

	receiverServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCount.Add(1)
		receivedSig = r.Header.Get(webhooks.SignatureHeader)
		receivedDelID = r.Header.Get("X-Delivery-Id")
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"received":true}`))
	}))
	defer receiverServer.Close()

	// 1. Configure merchant webhook endpoint via API
	configPayload := fmt.Sprintf(`{"url":"%s"}`, receiverServer.URL)
	req, _ := http.NewRequest("PUT", ts.URL+"/v1/merchants/webhook", bytes.NewReader([]byte(configPayload)))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to configure webhook endpoint: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 OK from PUT /v1/merchants/webhook, got %d: %s", resp.StatusCode, string(b))
	}

	// 2. Submit payment to generate payment.succeeded outbox event
	paymentKey := fmt.Sprintf("pay-wh-%d", time.Now().UnixNano())
	payReq, _ := http.NewRequest("POST", ts.URL+"/v1/payments", bytes.NewReader([]byte(`{"amount":2500,"currency":"usd"}`)))
	payReq.Header.Set("Authorization", "Bearer "+apiKey)
	payReq.Header.Set("Idempotency-Key", paymentKey)

	payResp, err := http.DefaultClient.Do(payReq)
	if err != nil {
		t.Fatalf("failed to create payment: %v", err)
	}
	defer payResp.Body.Close()
	if payResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created from POST /v1/payments, got %d", payResp.StatusCode)
	}

	// 3. Process payment through outbox worker
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ledgerService := ledger.NewService(queries)
	psp := processor.NewSimulator()
	worker := outbox.NewWorker(pool, queries, ledgerService, psp, platform.RealClock{}, logger)

	for i := 0; i < 5; i++ {
		_, _ = worker.ProcessBatch(ctx)
	}

	// 4. Dispatch webhook
	dispatcher := webhooks.NewDispatcher(pool, queries, platform.RealClock{}, logger, webhooks.Config{
		Policy: devPolicy,
	})

	// Run dispatcher batch twice (once to enqueue from outbox, once to deliver)
	for i := 0; i < 5; i++ {
		n, err := dispatcher.DispatchBatch(ctx)
		if err != nil {
			t.Fatalf("dispatch batch failed: %v", err)
		}
		if n == 0 && receivedCount.Load() > 0 {
			break
		}
	}

	// 5. Verify webhook receiver assertions
	if receivedCount.Load() == 0 {
		t.Fatalf("expected webhook receiver to receive event, but count is 0")
	}
	if receivedSig == "" {
		t.Errorf("missing MiniPay-Signature header on received webhook")
	}
	if receivedDelID == "" {
		t.Errorf("missing X-Delivery-Id header on received webhook")
	}

	// Verify HMAC signature
	err = webhooks.VerifySignature(receivedBody, receivedSig, merchant.WebhookSecret, 5*time.Minute, platform.RealClock{})
	if err != nil {
		t.Errorf("signature verification failed on delivered payload: %v", err)
	}

	// Verify payload contains payment.succeeded
	var envelope webhooks.WebhookEnvelope
	if err := json.Unmarshal(receivedBody, &envelope); err != nil {
		t.Fatalf("failed to parse received webhook envelope: %v", err)
	}
	if envelope.Type != "payment.succeeded" {
		t.Errorf("expected envelope type payment.succeeded, got %s", envelope.Type)
	}

	// 6. Verify delivery record in database
	delivery, err := queries.GetWebhookDelivery(ctx, receivedDelID)
	if err != nil {
		t.Fatalf("failed to load delivery %s: %v", receivedDelID, err)
	}
	if delivery.State != "succeeded" {
		t.Errorf("expected delivery state succeeded, got %s", delivery.State)
	}
	if !delivery.LastStatus.Valid || delivery.LastStatus.Int32 != 200 {
		t.Errorf("expected last_status 200, got %v", delivery.LastStatus)
	}
}

func TestWebhookRetryOnFailure(t *testing.T) {
	devPolicy := webhooks.SecurityPolicy{AllowPrivate: true, AllowInsecure: true}
	pool, queries, merchant, _, _ := setupPhase2Environment(t, devPolicy)
	ctx := context.Background()

	// Receiver always fails with 500
	receiverServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"temporary_server_error"}`))
	}))
	defer receiverServer.Close()

	// Update merchant webhook URL directly
	_, err := queries.UpdateMerchantWebhook(ctx, db.UpdateMerchantWebhookParams{
		ID:         merchant.ID,
		WebhookUrl: pgtype.Text{String: receiverServer.URL, Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to update merchant webhook: %v", err)
	}

	// Insert test outbox event
	eventPayload, _ := json.Marshal(map[string]any{"payment_id": "pay_test_retry", "amount": 1000})
	event, err := queries.CreateOutboxEvent(ctx, db.CreateOutboxEventParams{
		MerchantID:  merchant.ID,
		AggregateID: "pay_test_retry",
		Type:        "payment.succeeded",
		Payload:     eventPayload,
	})
	if err != nil {
		t.Fatalf("failed to create test outbox event: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fakeClock := platform.NewFakeClock(time.Now())
	dispatcher := webhooks.NewDispatcher(pool, queries, fakeClock, logger, webhooks.Config{
		Policy:         devPolicy,
		InitialBackoff: 10 * time.Second,
		MaxAttempts:    5,
	})

	// Dispatch batch (first enqueues, then attempts and fails)
	_, _ = dispatcher.DispatchBatch(ctx)
	_, _ = dispatcher.DispatchBatch(ctx)

	// Verify delivery is still pending with attempt 1
	delivery, err := queries.GetWebhookDeliveryByEvent(ctx, db.GetWebhookDeliveryByEventParams{
		MerchantID: merchant.ID,
		EventID:    event.ID,
	})
	if err != nil {
		t.Fatalf("failed to load delivery by event: %v", err)
	}

	if delivery.State != "pending" {
		t.Errorf("expected delivery state to remain pending after 1 failure, got %s", delivery.State)
	}
	if delivery.Attempts != 1 {
		t.Errorf("expected attempts to be 1, got %d", delivery.Attempts)
	}
	if !delivery.LastStatus.Valid || delivery.LastStatus.Int32 != 500 {
		t.Errorf("expected last_status 500, got %v", delivery.LastStatus)
	}
	if delivery.NextAttemptAt.Time.Before(fakeClock.Now()) {
		t.Errorf("expected next_attempt_at to be in the future, got %v <= %v", delivery.NextAttemptAt.Time, fakeClock.Now())
	}
}

func TestWebhookDeadLetterAfterMaxAttempts(t *testing.T) {
	devPolicy := webhooks.SecurityPolicy{AllowPrivate: true, AllowInsecure: true}
	pool, queries, merchant, _, _ := setupPhase2Environment(t, devPolicy)
	ctx := context.Background()

	// Receiver always fails
	receiverServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer receiverServer.Close()

	_, err := queries.UpdateMerchantWebhook(ctx, db.UpdateMerchantWebhookParams{
		ID:         merchant.ID,
		WebhookUrl: pgtype.Text{String: receiverServer.URL, Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to update merchant webhook: %v", err)
	}

	eventPayload, _ := json.Marshal(map[string]any{"payment_id": "pay_dead_letter"})
	event, err := queries.CreateOutboxEvent(ctx, db.CreateOutboxEventParams{
		MerchantID:  merchant.ID,
		AggregateID: "pay_dead_letter",
		Type:        "payment.failed",
		Payload:     eventPayload,
	})
	if err != nil {
		t.Fatalf("failed to create outbox event: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fakeClock := platform.NewFakeClock(time.Now())
	dispatcher := webhooks.NewDispatcher(pool, queries, fakeClock, logger, webhooks.Config{
		Policy:         devPolicy,
		InitialBackoff: 1 * time.Second,
		MaxAttempts:    3, // Set lower for testing
	})

	// Run dispatcher loop advancing clock each time to trigger retries
	for attempt := 0; attempt < 5; attempt++ {
		_, _ = dispatcher.DispatchBatch(ctx)
		fakeClock.Advance(10 * time.Minute)
		// Update next_attempt_at in DB for testing so ClaimPendingDeliveries picks it up
		_, _ = pool.Exec(ctx, "UPDATE webhook_deliveries SET next_attempt_at = now() - interval '1 second' WHERE state = 'pending'")
	}

	// Verify delivery is dead-lettered
	delivery, err := queries.GetWebhookDeliveryByEvent(ctx, db.GetWebhookDeliveryByEventParams{
		MerchantID: merchant.ID,
		EventID:    event.ID,
	})
	if err != nil {
		t.Fatalf("failed to find delivery: %v", err)
	}

	if delivery.State != "dead" {
		t.Errorf("expected delivery state dead, got %s", delivery.State)
	}
	if delivery.Attempts < 3 {
		t.Errorf("expected attempts >= 3, got %d", delivery.Attempts)
	}
}

func TestMerchantWebhookSSRFRejection(t *testing.T) {
	strictPolicy := webhooks.SecurityPolicy{AllowPrivate: false, AllowInsecure: false}
	_, _, _, apiKey, ts := setupPhase2Environment(t, strictPolicy)

	blockedTargets := []struct {
		name string
		url  string
	}{
		{"AWS Metadata", "http://169.254.169.254/latest/meta-data"},
		{"Localhost", "http://127.0.0.1:8080/admin"},
		{"Private RFC1918", "https://192.168.1.10/webhook"},
		{"Insecure HTTP", "http://example.com/webhook"},
		{"User Credentials", "https://admin:secret@api.example.com/webhook"},
	}

	for _, tt := range blockedTargets {
		t.Run(tt.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"url":"%s"}`, tt.url)
			req, _ := http.NewRequest("PUT", ts.URL+"/v1/merchants/webhook", bytes.NewReader([]byte(body)))
			req.Header.Set("Authorization", "Bearer "+apiKey)
			req.Header.Set("Content-Type", "application/json")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusBadRequest {
				b, _ := io.ReadAll(resp.Body)
				t.Errorf("expected 400 Bad Request for %s, got %d: %s", tt.url, resp.StatusCode, string(b))
			}
		})
	}
}
