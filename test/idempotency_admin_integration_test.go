package test_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/minipay/minipay/internal/api"
	"github.com/minipay/minipay/internal/ledger"
	"github.com/minipay/minipay/internal/webhooks"
)

func TestIdempotencyRetryStormAndDashboard(t *testing.T) {
	devPolicy := webhooks.SecurityPolicy{AllowPrivate: true, AllowInsecure: true}
	_, queries, _, apiKey, ts := setupPhase2Environment(t, devPolicy)
	ctx := context.Background()

	key := fmt.Sprintf("storm-test-%d", time.Now().UnixNano())
	body := []byte(`{"amount":1000,"currency":"usd"}`)

	const totalRequests = 20
	type reqResult struct {
		paymentID  string
		statusCode int
		replayed   bool
		err        error
	}

	results := make([]reqResult, totalRequests)
	var wg sync.WaitGroup

	// 1. Fire 20 concurrent requests with the identical idempotency key and body
	for i := 0; i < totalRequests; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req, err := http.NewRequest("POST", ts.URL+"/v1/payments", bytes.NewReader(body))
			if err != nil {
				results[idx] = reqResult{err: err}
				return
			}
			req.Header.Set("Authorization", "Bearer "+apiKey)
			req.Header.Set("Idempotency-Key", key)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				results[idx] = reqResult{err: err}
				return
			}
			defer resp.Body.Close()

			var p api.PaymentResponse
			respBytes, _ := io.ReadAll(resp.Body)
			_ = json.Unmarshal(respBytes, &p)

			results[idx] = reqResult{
				paymentID:  p.ID,
				statusCode: resp.StatusCode,
				replayed:   resp.Header.Get("Idempotent-Replayed") == "true",
				err:        nil,
			}
		}(i)
	}
	wg.Wait()

	// 2. Validate results
	createdCount := 0
	replayCount := 0
	uniqueIDs := make(map[string]bool)

	for i, r := range results {
		if r.err != nil {
			t.Fatalf("request %d failed with network error: %v", i, r.err)
		}
		if r.statusCode != http.StatusCreated {
			t.Fatalf("request %d returned unexpected status code: %d", i, r.statusCode)
		}
		if r.paymentID == "" {
			t.Fatalf("request %d returned empty payment ID", i)
		}
		uniqueIDs[r.paymentID] = true
		if r.replayed {
			replayCount++
		} else {
			createdCount++
		}
	}

	if createdCount != 1 {
		t.Fatalf("expected exactly 1 payment created, got %d", createdCount)
	}
	if replayCount != 19 {
		t.Fatalf("expected exactly 19 replays served, got %d", replayCount)
	}
	if len(uniqueIDs) != 1 {
		t.Fatalf("expected exactly 1 unique payment ID across all 20 responses, got %d: %v", len(uniqueIDs), uniqueIDs)
	}

	// 3. Test Conflict Detection (same key, different body)
	conflictBody := []byte(`{"amount":2500,"currency":"usd"}`)
	conflictReq, _ := http.NewRequest("POST", ts.URL+"/v1/payments", bytes.NewReader(conflictBody))
	conflictReq.Header.Set("Authorization", "Bearer "+apiKey)
	conflictReq.Header.Set("Idempotency-Key", key)

	conflictResp, err := http.DefaultClient.Do(conflictReq)
	if err != nil {
		t.Fatalf("conflict request failed: %v", err)
	}
	defer conflictResp.Body.Close()

	if conflictResp.StatusCode != http.StatusUnprocessableEntity {
		b, _ := io.ReadAll(conflictResp.Body)
		t.Fatalf("expected 422 Unprocessable Entity for payload mismatch, got %d: %s", conflictResp.StatusCode, string(b))
	}

	// 4. Assert stats queries in database
	stats, err := queries.GetIdempotencyStats(ctx)
	if err != nil {
		t.Fatalf("failed to query idempotency stats: %v", err)
	}
	if stats.TotalReplays < 19 {
		t.Errorf("expected total replays >= 19, got %d", stats.TotalReplays)
	}
	if stats.TotalConflicts < 1 {
		t.Errorf("expected total conflicts >= 1, got %d", stats.TotalConflicts)
	}

	dupCheck, err := queries.CheckDuplicatePayments(ctx)
	if err != nil {
		t.Fatalf("failed to check duplicate payments: %v", err)
	}
	if dupCheck.DuplicatePayments != 0 {
		t.Fatalf("VIOLATION: expected 0 duplicate payments, found %d", dupCheck.DuplicatePayments)
	}

	// 5. Verify Ledger Invariants still hold
	ledgerService := ledger.NewService(queries)
	violations, err := ledgerService.CheckInvariants(ctx)
	if err != nil {
		t.Fatalf("failed to check ledger invariants: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("ledger invariants violated after retry storm: %v", violations)
	}

	// 6. Test Dashboard endpoint rendering
	dashServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dup, _ := queries.CheckDuplicatePayments(r.Context())
		st, _ := queries.GetIdempotencyStats(r.Context())
		recent, _ := queries.ListRecentIdempotencyKeys(r.Context(), 10)

		fmt.Fprintf(w, "<html><body>")
		fmt.Fprintf(w, "<h1>MiniPay Admin Dashboard</h1>")
		fmt.Fprintf(w, "<div class='card'>%d retries &rarr; %d duplicate payments</div>", dup.TotalRetries, dup.DuplicatePayments)
		fmt.Fprintf(w, "<div>Total Keys: %d</div>", st.TotalKeys)
		fmt.Fprintf(w, "<div>Total Replays: %d</div>", st.TotalReplays)
		fmt.Fprintf(w, "<div>Recent Keys: %d</div>", len(recent))
		fmt.Fprintf(w, "</body></html>")
	}))
	defer dashServer.Close()

	dResp, err := http.Get(dashServer.URL)
	if err != nil {
		t.Fatalf("failed to scrape dashboard: %v", err)
	}
	defer dResp.Body.Close()

	dashBody, _ := io.ReadAll(dResp.Body)
	dashStr := string(dashBody)
	if !strings.Contains(dashStr, "duplicate payments") {
		t.Errorf("dashboard missing duplicate payments card: %s", dashStr)
	}
	if !strings.Contains(dashStr, "Total Replays") {
		t.Errorf("dashboard missing Total Replays metric: %s", dashStr)
	}
}
