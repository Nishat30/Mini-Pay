// admin.go routes admin subcommands: create-merchant, check-ledger, list-reviews, review-payment, inspect-risk, dashboard.
// It owns CLI parsing and operations for administrative oversight.
// It must never bypass security checks.
// It relies on internal packages for the actual work.
package main

import (
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
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/minipay/minipay/internal/ledger"
	"github.com/minipay/minipay/internal/platform"
	"github.com/minipay/minipay/internal/platform/db"
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

		mux := http.NewServeMux()
		mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			u, p, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(*user)) != 1 ||
				subtle.ConstantTimeCompare([]byte(p), []byte(*pass)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="MiniPay Admin"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			ledgerService := ledger.NewService(queries)
			violations, _ := ledgerService.CheckInvariants(r.Context())
			pendingReviews, _ := queries.ListPaymentsByStatus(r.Context(), "requires_review")

			data := struct {
				LedgerOK       bool
				ViolationCount int
				Reviews        []db.Payments
			}{
				LedgerOK:       len(violations) == 0,
				ViolationCount: len(violations),
				Reviews:        pendingReviews,
			}

			tmpl := `
<!DOCTYPE html>
<html>
<head>
	<title>MiniPay Admin Dashboard</title>
	<style>
		body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; margin: 40px; background: #f8fafc; color: #1e293b; }
		h1 { color: #0f172a; margin-bottom: 8px; }
		.card { background: white; border-radius: 8px; padding: 24px; margin-bottom: 24px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); }
		.badge { display: inline-block; padding: 4px 12px; border-radius: 9999px; font-weight: 600; font-size: 13px; }
		.badge-success { background: #dcfce7; color: #15803d; }
		.badge-danger { background: #fee2e2; color: #b91c1c; }
		table { width: 100%; border-collapse: collapse; margin-top: 16px; }
		th, td { text-align: left; padding: 12px; border-bottom: 1px solid #e2e8f0; }
		th { background: #f1f5f9; font-size: 13px; text-transform: uppercase; letter-spacing: 0.05em; }
	</style>
</head>
<body>
	<h1>MiniPay Admin Dashboard</h1>
	<p style="color: #64748b; margin-top: 0;">Operational Oversight, Ledger Integrity & Risk Review</p>

	<div class="card">
		<h2>Ledger Integrity Status</h2>
		{{if .LedgerOK}}
			<span class="badge badge-success">&#10003; All Invariants Hold (0 Violations)</span>
		{{else}}
			<span class="badge badge-danger">&#10007; {{.ViolationCount}} Invariant Violations Detected</span>
		{{end}}
	</div>

	<div class="card">
		<h2>Pending Risk Reviews ({{len .Reviews}})</h2>
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
		<p style="color: #64748b;">No payments currently awaiting manual review.</p>
		{{end}}
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
