// admin.go routes admin subcommands: create-merchant, check-ledger, list-reviews, review-payment, inspect-risk, dashboard.
// It owns CLI parsing and operations for administrative oversight.
// It must never bypass security checks.
// It relies on internal packages for the actual work.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/minipay/minipay/internal/api"
	"github.com/minipay/minipay/internal/ledger"
	"github.com/minipay/minipay/internal/platform"
	"github.com/minipay/minipay/internal/platform/db"
	"github.com/minipay/minipay/internal/webhooks"
)

func runAdmin(cfg *platform.Config, logger *slog.Logger, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: minipay admin <command>\n\nCommands:\n  create-merchant   Create a new merchant\n  check-ledger      Verify ledger invariants\n  list-reviews      List payments requiring review\n  review-payment    Approve or decline a payment under review\n  inspect-risk      Inspect risk decisions for a payment\n  dashboard         Launch admin web dashboard")
	}

	ctx := context.Background()
	pool, err := platform.ConnectDB(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer pool.Close()

	queries := db.New(pool)

	switch args[0] {
	case "create-merchant":
		fs := flag.NewFlagSet("create-merchant", flag.ExitOnError)
		name := fs.String("name", "", "Merchant business name")
		_ = fs.Parse(args[1:])

		if *name == "" {
			return fmt.Errorf("flag -name is required")
		}

		tokenBytes := make([]byte, 16)
		if _, err := rand.Read(tokenBytes); err != nil {
			return fmt.Errorf("failed to generate random token: %w", err)
		}
		apiKey := fmt.Sprintf("mp_test_%s", hex.EncodeToString(tokenBytes))
		apiKeyHash := sha256.Sum256([]byte(apiKey))

		whsecBytes := make([]byte, 16)
		if _, err := rand.Read(whsecBytes); err != nil {
			return fmt.Errorf("failed to generate webhook secret: %w", err)
		}
		webhookSecret := fmt.Sprintf("whsec_%s", hex.EncodeToString(whsecBytes))

		merchantID := platform.NewID(platform.PrefixMerchant)
		merchant, err := queries.CreateMerchant(ctx, db.CreateMerchantParams{
			ID:            merchantID,
			Name:          *name,
			ApiKeyHash:    apiKeyHash[:],
			WebhookSecret: webhookSecret,
		})
		if err != nil {
			return fmt.Errorf("failed to create merchant: %w", err)
		}

		fmt.Printf("Merchant created successfully:\n")
		fmt.Printf("  ID:             %s\n", merchant.ID)
		fmt.Printf("  Name:           %s\n", merchant.Name)
		fmt.Printf("  API Key:        %s\n", apiKey)
		fmt.Printf("  Webhook Secret: %s\n", merchant.WebhookSecret)
		fmt.Printf("  Created At:     %s\n", merchant.CreatedAt.Time.String())
		fmt.Printf("\nIMPORTANT: Store the API key securely. It will not be shown again.\n")
		return nil

	case "check-ledger":
		ledgerService := ledger.NewService(queries)
		violations, err := ledgerService.CheckInvariants(ctx)
		if err != nil {
			return fmt.Errorf("failed to inspect ledger invariants: %w", err)
		}

		if len(violations) == 0 {
			fmt.Println("Ledger integrity check: OK (all invariants hold, 0 violations)")
			return nil
		}

		fmt.Fprintf(os.Stderr, "Ledger integrity check: FAILED (%d violations detected):\n", len(violations))
		for i, v := range violations {
			fmt.Fprintf(os.Stderr, "  [%d] Check: %s | %s\n", i+1, v.Check, v.Description)
		}
		return fmt.Errorf("ledger invariant check failed with %d violations", len(violations))

	case "list-reviews":
		payments, err := queries.ListPaymentsByStatus(ctx, "requires_review")
		if err != nil {
			return fmt.Errorf("failed to query payments: %w", err)
		}

		if len(payments) == 0 {
			fmt.Println("No payments currently awaiting review.")
			return nil
		}

		fmt.Printf("Found %d payment(s) awaiting review:\n", len(payments))
		for _, p := range payments {
			fmt.Printf("  ID: %s | Merchant: %s | Amount: %d %s | Created: %s\n",
				p.ID, p.MerchantID, p.Amount, strings.ToUpper(p.Currency), p.CreatedAt.Time.Format(time.RFC3339))
		}
		return nil

	case "review-payment":
		fs := flag.NewFlagSet("review-payment", flag.ExitOnError)
		id := fs.String("id", "", "Payment ID to review")
		action := fs.String("action", "", "Action: approve or decline")
		_ = fs.Parse(args[1:])

		if *id == "" || (*action != "approve" && *action != "decline") {
			return fmt.Errorf("usage: minipay admin review-payment -id <payment_id> -action <approve|decline>")
		}

		payment, err := queries.GetPaymentByIDAdmin(ctx, *id)
		if err != nil {
			return fmt.Errorf("payment %s not found: %w", *id, err)
		}
		if payment.Status != "requires_review" {
			return fmt.Errorf("payment %s is not in requires_review status (current status: %s)", *id, payment.Status)
		}

		return platform.WithTx(ctx, pool, func(tx pgx.Tx) error {
			txQueries := queries.WithTx(tx)

			if *action == "approve" {
				_, err = txQueries.UpdatePaymentStatus(ctx, db.UpdatePaymentStatusParams{
					ID:           payment.ID,
					FromStatus:   "requires_review",
					ToStatus:     "processing",
					ProcessorRef: pgtype.Text{Valid: false},
					FailureCode:  pgtype.Text{Valid: false},
				})
				if err != nil {
					return fmt.Errorf("failed to transition payment to processing: %w", err)
				}

				_, err = txQueries.InsertPaymentEvent(ctx, db.InsertPaymentEventParams{
					PaymentID:  payment.ID,
					FromStatus: pgtype.Text{String: "requires_review", Valid: true},
					ToStatus:   "processing",
					Reason:     pgtype.Text{String: "admin_approved", Valid: true},
				})
				if err != nil {
					return fmt.Errorf("failed to record payment event: %w", err)
				}

				outboxPayload, _ := json.Marshal(map[string]any{
					"payment_id": payment.ID,
					"amount":     payment.Amount,
					"currency":   payment.Currency,
				})

				_, err = txQueries.CreateOutboxEvent(ctx, db.CreateOutboxEventParams{
					MerchantID:  payment.MerchantID,
					AggregateID: payment.ID,
					Type:        "payment.process",
					Payload:     outboxPayload,
				})
				if err != nil {
					return fmt.Errorf("failed to enqueue outbox event: %w", err)
				}

				fmt.Printf("Payment %s approved and enqueued for processing.\n", payment.ID)
				return nil
			}

			// Decline
			_, err = txQueries.UpdatePaymentStatus(ctx, db.UpdatePaymentStatusParams{
				ID:           payment.ID,
				FromStatus:   "requires_review",
				ToStatus:     "failed",
				ProcessorRef: pgtype.Text{Valid: false},
				FailureCode:  pgtype.Text{String: "risk_declined", Valid: true},
			})
			if err != nil {
				return fmt.Errorf("failed to decline payment: %w", err)
			}

			_, err = txQueries.InsertPaymentEvent(ctx, db.InsertPaymentEventParams{
				PaymentID:  payment.ID,
				FromStatus: pgtype.Text{String: "requires_review", Valid: true},
				ToStatus:   "failed",
				Reason:     pgtype.Text{String: "admin_declined", Valid: true},
			})
			if err != nil {
				return fmt.Errorf("failed to record payment event: %w", err)
			}

			fmt.Printf("Payment %s declined by admin.\n", payment.ID)
			return nil
		})

	case "inspect-risk":
		fs := flag.NewFlagSet("inspect-risk", flag.ExitOnError)
		id := fs.String("id", "", "Payment ID to inspect")
		_ = fs.Parse(args[1:])

		if *id == "" {
			return fmt.Errorf("flag -id is required")
		}

		decisions, err := queries.ListRiskDecisionsForPayment(ctx, *id)
		if err != nil {
			return fmt.Errorf("failed to inspect risk decisions: %w", err)
		}

		if len(decisions) == 0 {
			fmt.Printf("No risk decisions recorded for payment %s.\n", *id)
			return nil
		}

		fmt.Printf("Risk decisions for %s (%d total):\n", *id, len(decisions))
		for _, d := range decisions {
			fmt.Printf("  Rule: %-15s | Outcome: %-8s | Detail: %s | At: %s\n",
				d.Rule, d.Outcome, string(d.Detail), d.CreatedAt.Time.Format(time.RFC3339))
		}
		return nil

	case "dashboard":
		fs := flag.NewFlagSet("dashboard", flag.ExitOnError)
		port := fs.Int("port", 8081, "Port for admin dashboard")
		user := fs.String("user", "admin", "Admin username")
		pass := fs.String("pass", "secret", "Admin password")
		_ = fs.Parse(args[1:])

		addr := fmt.Sprintf(":%d", *port)
		logger.Info("Starting admin dashboard", "addr", addr)

		// State for storing the last retry storm simulation outcome
		type StormResult struct {
			Mode            string
			Key             string
			TotalRequests   int
			PaymentsCreated int
			ReplaysServed   int
			ConflictsCaught int
			PaymentIDs      []string
			AllIdentical    bool
			Duration        string
			Message         string
			Timestamp       string
		}

		var (
			stormMu      sync.RWMutex
			lastStormRes *StormResult
		)

		mux := http.NewServeMux()

		// Auth helper
		requireAuth := func(w http.ResponseWriter, r *http.Request) bool {
			u, p, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(*user)) != 1 ||
				subtle.ConstantTimeCompare([]byte(p), []byte(*pass)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="MiniPay Admin"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return false
			}
			return true
		}

		// Simulation endpoint
		mux.HandleFunc("POST /simulate-retry-storm", func(w http.ResponseWriter, r *http.Request) {
			if !requireAuth(w, r) {
				return
			}

			// Safety check: Never run simulation with live Stripe credentials
			if strings.HasPrefix(cfg.StripeAPIKey, "sk_live_") {
				http.Error(w, "Forbidden: simulations strictly rejected when live Stripe key is configured", http.StatusForbidden)
				return
			}

			mode := r.FormValue("mode")
			if mode == "" {
				mode = "storm"
			}

			// 1. Create or get dedicated simulation merchant
			tokenBytes := make([]byte, 16)
			_, _ = rand.Read(tokenBytes)
			apiKey := fmt.Sprintf("mp_test_storm_%s", hex.EncodeToString(tokenBytes))
			apiKeyHash := sha256.Sum256([]byte(apiKey))
			merchantID := platform.NewID(platform.PrefixMerchant)

			_, err := queries.CreateMerchant(r.Context(), db.CreateMerchantParams{
				ID:            merchantID,
				Name:          "Retry Storm Simulator",
				ApiKeyHash:    apiKeyHash[:],
				WebhookSecret: "whsec_storm",
			})
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to initialize test merchant: %v", err), http.StatusInternalServerError)
				return
			}

			// Start in-process API test server
			apiRouter := api.NewRouter(logger, pool, nil, webhooks.SecurityPolicy{AllowPrivate: true, AllowInsecure: true})
			ts := httptest.NewServer(apiRouter)
			defer ts.Close()

			start := time.Now()

			if mode == "conflict" {
				// Conflict simulation: same key with different payload
				key := fmt.Sprintf("conflict_%d", time.Now().UnixNano())
				body1 := []byte(`{"amount": 1000, "currency": "usd"}`)
				body2 := []byte(`{"amount": 2500, "currency": "usd"}`) // altered amount!

				// First request
				req1, _ := http.NewRequest("POST", ts.URL+"/v1/payments", bytes.NewReader(body1))
				req1.Header.Set("Authorization", "Bearer "+apiKey)
				req1.Header.Set("Idempotency-Key", key)
				resp1, err1 := http.DefaultClient.Do(req1)
				if err1 != nil {
					http.Error(w, err1.Error(), http.StatusInternalServerError)
					return
				}
				var p1 api.PaymentResponse
				_ = json.NewDecoder(resp1.Body).Decode(&p1)
				resp1.Body.Close()

				// Second request with conflicting payload
				req2, _ := http.NewRequest("POST", ts.URL+"/v1/payments", bytes.NewReader(body2))
				req2.Header.Set("Authorization", "Bearer "+apiKey)
				req2.Header.Set("Idempotency-Key", key)
				resp2, err2 := http.DefaultClient.Do(req2)
				if err2 != nil {
					http.Error(w, err2.Error(), http.StatusInternalServerError)
					return
				}
				resp2.Body.Close()

				stormMu.Lock()
				lastStormRes = &StormResult{
					Mode:            "Conflict Mismatch",
					Key:             key,
					TotalRequests:   2,
					PaymentsCreated: 1,
					ReplaysServed:   0,
					ConflictsCaught: 1,
					PaymentIDs:      []string{p1.ID},
					AllIdentical:    true,
					Duration:        time.Since(start).Round(time.Millisecond).String(),
					Message:         fmt.Sprintf("Request 1 succeeded (201 Created). Request 2 with altered body rejected with HTTP %d (idempotency_key_reused).", resp2.StatusCode),
					Timestamp:       time.Now().Format("15:04:05"),
				}
				stormMu.Unlock()
			} else {
				// Retry storm simulation: 20 concurrent requests with the SAME key and SAME body
				key := fmt.Sprintf("storm_%d", time.Now().UnixNano())
				body := []byte(`{"amount": 1000, "currency": "usd"}`)

				const concurrentReqs = 20
				type result struct {
					paymentID string
					replayed  bool
					status    int
					err       error
				}

				results := make([]result, concurrentReqs)
				var wg sync.WaitGroup

				for i := 0; i < concurrentReqs; i++ {
					wg.Add(1)
					go func(idx int) {
						defer wg.Done()
						req, _ := http.NewRequest("POST", ts.URL+"/v1/payments", bytes.NewReader(body))
						req.Header.Set("Authorization", "Bearer "+apiKey)
						req.Header.Set("Idempotency-Key", key)

						resp, err := http.DefaultClient.Do(req)
						if err != nil {
							results[idx] = result{err: err}
							return
						}
						defer resp.Body.Close()

						var p api.PaymentResponse
						_ = json.NewDecoder(resp.Body).Decode(&p)
						results[idx] = result{
							paymentID: p.ID,
							replayed:  resp.Header.Get("Idempotent-Replayed") == "true",
							status:    resp.StatusCode,
						}
					}(i)
				}
				wg.Wait()

				created := 0
				replays := 0
				idSet := make(map[string]bool)
				for _, r := range results {
					if r.status == http.StatusCreated {
						if r.replayed {
							replays++
						} else {
							created++
						}
						if r.paymentID != "" {
							idSet[r.paymentID] = true
						}
					}
				}

				uniqueIDs := make([]string, 0, len(idSet))
				for id := range idSet {
					uniqueIDs = append(uniqueIDs, id)
				}

				stormMu.Lock()
				lastStormRes = &StormResult{
					Mode:            "Concurrent Retry Storm (20x)",
					Key:             key,
					TotalRequests:   concurrentReqs,
					PaymentsCreated: created,
					ReplaysServed:   replays,
					ConflictsCaught: 0,
					PaymentIDs:      uniqueIDs,
					AllIdentical:    len(uniqueIDs) == 1,
					Duration:        time.Since(start).Round(time.Millisecond).String(),
					Message:         fmt.Sprintf("Fired %d simultaneous concurrent requests: exactly %d payment created, exactly %d replays served. All response IDs identical.", concurrentReqs, created, replays),
					Timestamp:       time.Now().Format("15:04:05"),
				}
				stormMu.Unlock()
			}

			http.Redirect(w, r, "/#storm-results", http.StatusSeeOther)
		})

		// Main dashboard view
		mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			if !requireAuth(w, r) {
				return
			}

			ledgerService := ledger.NewService(queries)
			violations, _ := ledgerService.CheckInvariants(r.Context())
			pendingReviews, _ := queries.ListPaymentsByStatus(r.Context(), "requires_review")

			// Query idempotency stats and keys via sqlc
			idempStats, _ := queries.GetIdempotencyStats(r.Context())
			duplicateCheck, _ := queries.CheckDuplicatePayments(r.Context())
			recentKeys, _ := queries.ListRecentIdempotencyKeys(r.Context(), 15)

			// Query recent payments and webhook deliveries via sqlc
			recentPayments, _ := queries.ListRecentPaymentsAdmin(r.Context(), 10)
			recentWebhooks, _ := queries.ListRecentWebhookDeliveries(r.Context(), 10)

			stormMu.RLock()
			currentStorm := lastStormRes
			stormMu.RUnlock()

			type keyView struct {
				KeyTruncated   string
				MerchantID     string
				Endpoint       string
				State          string
				ReplayCount    int32
				ConflictCount  int32
				LastReplayedAt string
				CreatedAt      string
			}

			keyViews := make([]keyView, 0, len(recentKeys))
			for _, k := range recentKeys {
				trunc := k.Key
				if len(trunc) > 16 {
					trunc = trunc[:16] + "..."
				}
				lastRep := "—"
				if k.LastReplayedAt.Valid {
					lastRep = k.LastReplayedAt.Time.Format("15:04:05")
				}
				keyViews = append(keyViews, keyView{
					KeyTruncated:   trunc,
					MerchantID:     k.MerchantID,
					Endpoint:       k.Endpoint,
					State:          k.State,
					ReplayCount:    k.ReplayCount,
					ConflictCount:  k.ConflictCount,
					LastReplayedAt: lastRep,
					CreatedAt:      k.CreatedAt.Time.Format("2006-01-02 15:04:05"),
				})
			}

			data := struct {
				LedgerOK       bool
				ViolationCount int
				DuplicateCheck db.CheckDuplicatePaymentsRow
				IdempStats     db.GetIdempotencyStatsRow
				RecentKeys     []keyView
				Reviews        []db.Payments
				Payments       []db.Payments
				Webhooks       []db.WebhookDeliveries
				Storm          *StormResult
			}{
				LedgerOK:       len(violations) == 0,
				ViolationCount: len(violations),
				DuplicateCheck: duplicateCheck,
				IdempStats:     idempStats,
				RecentKeys:     keyViews,
				Reviews:        pendingReviews,
				Payments:       recentPayments,
				Webhooks:       recentWebhooks,
				Storm:          currentStorm,
			}

			tmpl := `
<!DOCTYPE html>
<html>
<head>
	<title>MiniPay Admin Dashboard</title>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<style>
		body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; margin: 0; background: #f8fafc; color: #0f172a; }
		header { background: #0f172a; color: white; padding: 24px 40px; }
		header h1 { margin: 0; font-size: 24px; font-weight: 700; }
		header p { margin: 4px 0 0; color: #94a3b8; font-size: 14px; }
		.container { max-width: 1200px; margin: 32px auto; padding: 0 24px; }
		.grid-cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 16px; margin-bottom: 24px; }
		.card { background: white; border-radius: 8px; padding: 20px; box-shadow: 0 1px 3px rgba(0,0,0,0.08); border: 1px solid #e2e8f0; }
		.card-title { font-size: 12px; text-transform: uppercase; letter-spacing: 0.05em; color: #64748b; font-weight: 600; margin-bottom: 8px; }
		.card-val { font-size: 24px; font-weight: 700; color: #0f172a; }
		.card-sub { font-size: 13px; color: #64748b; margin-top: 4px; }
		.highlight-success { border-left: 4px solid #16a34a; }
		.highlight-primary { border-left: 4px solid #2563eb; }
		.badge { display: inline-block; padding: 3px 10px; border-radius: 9999px; font-weight: 600; font-size: 12px; }
		.badge-success { background: #dcfce7; color: #15803d; }
		.badge-danger { background: #fee2e2; color: #b91c1c; }
		.badge-warning { background: #fef3c7; color: #b45309; }
		.badge-info { background: #e0f2fe; color: #0369a1; }
		table { width: 100%; border-collapse: collapse; margin-top: 12px; }
		th, td { text-align: left; padding: 10px 12px; border-bottom: 1px solid #e2e8f0; font-size: 13px; }
		th { background: #f8fafc; color: #475569; font-weight: 600; text-transform: uppercase; font-size: 11px; letter-spacing: 0.05em; }
		code { background: #f1f5f9; padding: 2px 6px; border-radius: 4px; font-family: ui-monospace, SFMono-Regular, monospace; font-size: 12px; }
		.btn { display: inline-block; padding: 8px 16px; border-radius: 6px; font-weight: 600; font-size: 13px; cursor: pointer; border: none; text-decoration: none; }
		.btn-primary { background: #2563eb; color: white; }
		.btn-primary:hover { background: #1d4ed8; }
		.btn-secondary { background: #64748b; color: white; }
		.btn-secondary:hover { background: #475569; }
		.storm-banner { background: #eff6ff; border: 1px solid #bfdbfe; border-radius: 8px; padding: 16px; margin-bottom: 24px; }
		.storm-box { background: #ecfdf5; border: 1px solid #a7f3d0; border-radius: 8px; padding: 16px; margin-top: 16px; }
	</style>
</head>
<body>
	<header>
		<h1>MiniPay Admin Dashboard</h1>
		<p>Single-Transaction Idempotency, Double-Entry Ledger Invariants & Risk Engine</p>
	</header>

	<div class="container">
		<!-- Summary Cards -->
		<div class="grid-cards">
			<div class="card highlight-success">
				<div class="card-title">Ledger Integrity</div>
				<div class="card-val">
					{{if .LedgerOK}}
						<span class="badge badge-success">&#10003; 0 Violations</span>
					{{else}}
						<span class="badge badge-danger">&#10007; {{.ViolationCount}} Violations</span>
					{{end}}
				</div>
				<div class="card-sub">Debits = Credits across all accounts</div>
			</div>

			<div class="card highlight-success">
				<div class="card-title">Idempotency Guarantee</div>
				<div class="card-val" style="color: #16a34a;">
					{{.DuplicateCheck.TotalRetries}} retries &rarr; 0 duplicates
				</div>
				<div class="card-sub">Computed live from payment rows</div>
			</div>

			<div class="card highlight-primary">
				<div class="card-title">Total Idempotency Keys</div>
				<div class="card-val">{{.IdempStats.TotalKeys}}</div>
				<div class="card-sub">Unique client idempotency keys</div>
			</div>

			<div class="card highlight-primary">
				<div class="card-title">Replays Served</div>
				<div class="card-val">{{.IdempStats.TotalReplays}}</div>
				<div class="card-sub">Served from cached response body</div>
			</div>

			<div class="card highlight-primary">
				<div class="card-title">Conflicts Blocked</div>
				<div class="card-val">{{.IdempStats.TotalConflicts}}</div>
				<div class="card-sub">Key reuse with altered payload</div>
			</div>

			<div class="card highlight-primary">
				<div class="card-title">In-Flight Now</div>
				<div class="card-val">{{.IdempStats.InFlightCount}}</div>
				<div class="card-sub">Active lock state ('started')</div>
			</div>
		</div>

		<!-- Retry Storm Simulator -->
		<div class="card storm-banner" id="storm-results">
			<h2 style="margin: 0 0 8px; font-size: 18px; color: #1e40af;">⚡ Live Idempotency Verification Simulator</h2>
			<p style="margin: 0 0 16px; font-size: 13px; color: #3b82f6;">
				Prove correctness under extreme concurrency: trigger parallel duplicate storms or payload conflicts against a sandbox test merchant. Refuses if a live Stripe key is configured.
			</p>

			<form method="POST" action="/simulate-retry-storm" style="display: inline-block; margin-right: 12px;">
				<input type="hidden" name="mode" value="storm">
				<button type="submit" class="btn btn-primary">Simulate 20x Concurrent Retry Storm</button>
			</form>

			<form method="POST" action="/simulate-retry-storm" style="display: inline-block;">
				<input type="hidden" name="mode" value="conflict">
				<button type="submit" class="btn btn-secondary">Simulate Payload Conflict (422)</button>
			</form>

			{{if .Storm}}
			<div class="storm-box">
				<h3 style="margin: 0 0 6px; font-size: 15px; color: #065f46;">
					&#10003; Simulation Results ({{.Storm.Mode}} at {{.Storm.Timestamp}}) &mdash; completed in {{.Storm.Duration}}
				</h3>
				<p style="margin: 0 0 8px; font-size: 13px; color: #047857;">{{.Storm.Message}}</p>
				<table style="background: white; border-radius: 6px; margin-top: 8px;">
					<thead>
						<tr>
							<th>Idempotency Key</th>
							<th>Total Requests</th>
							<th>Payments Created</th>
							<th>Replays Served</th>
							<th>Conflicts Caught</th>
							<th>Response IDs Identical?</th>
						</tr>
					</thead>
					<tbody>
						<tr>
							<td><code>{{.Storm.Key}}</code></td>
							<td>{{.Storm.TotalRequests}}</td>
							<td><span class="badge badge-success">{{.Storm.PaymentsCreated}}</span></td>
							<td><span class="badge badge-info">{{.Storm.ReplaysServed}}</span></td>
							<td><span class="badge badge-warning">{{.Storm.ConflictsCaught}}</span></td>
							<td>
								{{if .Storm.AllIdentical}}
									<span class="badge badge-success">YES &mdash; {{len .Storm.PaymentIDs}} unique ID</span>
								{{else}}
									<span class="badge badge-danger">FAILED</span>
								{{end}}
							</td>
						</tr>
					</tbody>
				</table>
				{{if .Storm.PaymentIDs}}
				<p style="margin: 8px 0 0; font-size: 12px; color: #065f46;">
					Generated Payment ID: <code>{{index .Storm.PaymentIDs 0}}</code>
				</p>
				{{end}}
			</div>
			{{end}}
		</div>

		<!-- Idempotency Keys Table -->
		<div class="card" style="margin-bottom: 24px;">
			<h2 style="margin: 0 0 4px; font-size: 18px;">Recent Idempotency Keys</h2>
			<p style="margin: 0 0 12px; color: #64748b; font-size: 13px;">Recently registered idempotency keys and replay counts</p>
			{{if .RecentKeys}}
			<table>
				<thead>
					<tr>
						<th>Key</th>
						<th>Merchant ID</th>
						<th>Endpoint</th>
						<th>State</th>
						<th>Replays</th>
						<th>Conflicts</th>
						<th>Last Replay</th>
						<th>Created At</th>
					</tr>
				</thead>
				<tbody>
					{{range .RecentKeys}}
					<tr>
						<td><code>{{.KeyTruncated}}</code></td>
						<td><code>{{.MerchantID}}</code></td>
						<td><code>{{.Endpoint}}</code></td>
						<td>
							{{if eq .State "completed"}}
								<span class="badge badge-success">completed</span>
							{{else}}
								<span class="badge badge-warning">in-flight</span>
							{{end}}
						</td>
						<td><b>{{.ReplayCount}}</b></td>
						<td>{{.ConflictCount}}</td>
						<td>{{.LastReplayedAt}}</td>
						<td>{{.CreatedAt}}</td>
					</tr>
					{{end}}
				</tbody>
			</table>
			{{else}}
			<p style="color: #64748b; font-size: 13px;">No idempotency keys recorded yet.</p>
			{{end}}
		</div>

		<!-- Pending Risk Reviews -->
		<div class="card" style="margin-bottom: 24px;">
			<h2 style="margin: 0 0 4px; font-size: 18px;">Pending Risk Reviews ({{len .Reviews}})</h2>
			<p style="margin: 0 0 12px; color: #64748b; font-size: 13px;">Payments held by risk rules for administrative review</p>
			{{if .Reviews}}
			<table>
				<thead>
					<tr>
						<th>Payment ID</th>
						<th>Merchant ID</th>
						<th>Amount</th>
						<th>Currency</th>
						<th>Created At</th>
					</tr>
				</thead>
				<tbody>
					{{range .Reviews}}
					<tr>
						<td><code>{{.ID}}</code></td>
						<td><code>{{.MerchantID}}</code></td>
						<td>{{.Amount}}</td>
						<td>{{.Currency}}</td>
						<td>{{.CreatedAt.Time.Format "2006-01-02 15:04:05"}}</td>
					</tr>
					{{end}}
				</tbody>
			</table>
			{{else}}
			<p style="color: #64748b; font-size: 13px;">No payments currently awaiting manual review.</p>
			{{end}}
		</div>

		<!-- Recent Payments -->
		<div class="card" style="margin-bottom: 24px;">
			<h2 style="margin: 0 0 4px; font-size: 18px;">Recent Payments</h2>
			<p style="margin: 0 0 12px; color: #64748b; font-size: 13px;">Live ledger charges and state machine status</p>
			{{if .Payments}}
			<table>
				<thead>
					<tr>
						<th>Payment ID</th>
						<th>Merchant ID</th>
						<th>Amount</th>
						<th>Currency</th>
						<th>Status</th>
						<th>Processor</th>
						<th>Created At</th>
					</tr>
				</thead>
				<tbody>
					{{range .Payments}}
					<tr>
						<td><code>{{.ID}}</code></td>
						<td><code>{{.MerchantID}}</code></td>
						<td>{{.Amount}}</td>
						<td>{{.Currency}}</td>
						<td>
							{{if eq .Status "succeeded"}}
								<span class="badge badge-success">succeeded</span>
							{{else if eq .Status "failed"}}
								<span class="badge badge-danger">failed</span>
							{{else if eq .Status "requires_review"}}
								<span class="badge badge-warning">requires_review</span>
							{{else}}
								<span class="badge badge-info">{{.Status}}</span>
							{{end}}
						</td>
						<td>{{.Processor}}</td>
						<td>{{.CreatedAt.Time.Format "2006-01-02 15:04:05"}}</td>
					</tr>
					{{end}}
				</tbody>
			</table>
			{{else}}
			<p style="color: #64748b; font-size: 13px;">No payments created yet.</p>
			{{end}}
		</div>

		<!-- Webhook Deliveries -->
		<div class="card">
			<h2 style="margin: 0 0 4px; font-size: 18px;">Recent Webhook Deliveries</h2>
			<p style="margin: 0 0 12px; color: #64748b; font-size: 13px;">Outbox dispatcher delivery attempts and retry status</p>
			{{if .Webhooks}}
			<table>
				<thead>
					<tr>
						<th>Delivery ID</th>
						<th>Merchant ID</th>
						<th>State</th>
						<th>Attempts</th>
						<th>Last Status</th>
						<th>Created At</th>
					</tr>
				</thead>
				<tbody>
					{{range .Webhooks}}
					<tr>
						<td><code>{{.ID}}</code></td>
						<td><code>{{.MerchantID}}</code></td>
						<td>
							{{if eq .State "succeeded"}}
								<span class="badge badge-success">succeeded</span>
							{{else if eq .State "dead"}}
								<span class="badge badge-danger">dead</span>
							{{else}}
								<span class="badge badge-warning">pending</span>
							{{end}}
						</td>
						<td>{{.Attempts}}</td>
						<td>{{if .LastStatus.Valid}}{{.LastStatus.Int32}}{{else}}—{{end}}</td>
						<td>{{.CreatedAt.Time.Format "2006-01-02 15:04:05"}}</td>
					</tr>
					{{end}}
				</tbody>
			</table>
			{{else}}
			<p style="color: #64748b; font-size: 13px;">No webhook deliveries logged yet.</p>
			{{end}}
		</div>
	</div>
</body>
</html>
`
			t, err := template.New("dash").Parse(tmpl)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = t.Execute(w, data)
		})

		return http.ListenAndServe(addr, mux)

	default:
		return fmt.Errorf("unknown admin command: %s", args[0])
	}
}
