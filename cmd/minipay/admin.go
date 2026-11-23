// admin.go routes admin subcommands: create-merchant, check-ledger.
// It owns CLI parsing for admin operations.
// It must never bypass security checks.
// It relies on internal packages for the actual work.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/minipay/minipay/internal/ledger"
	"github.com/minipay/minipay/internal/platform"
	"github.com/minipay/minipay/internal/platform/db"
)

func runAdmin(cfg *platform.Config, logger *slog.Logger, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: minipay admin <command>\n\nCommands:\n  create-merchant   Create a new merchant\n  check-ledger      Verify ledger invariants")
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

		// Generate random 16-byte token -> 32 hex chars
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

	default:
		return fmt.Errorf("unknown admin command: %s", args[0])
	}
}
