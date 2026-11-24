package idempotency_test

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/minipay/minipay/internal/idempotency"
	"github.com/minipay/minipay/internal/platform"
	"github.com/minipay/minipay/internal/platform/db"
)

func TestComputeHash(t *testing.T) {
	hash1 := idempotency.ComputeHash("POST", "/v1/payments", []byte(`{"amount":1000,"currency":"usd"}`))
	hash2 := idempotency.ComputeHash("POST", "/v1/payments", []byte(`{"amount":1000,"currency":"usd"}`))
	hash3 := idempotency.ComputeHash("POST", "/v1/payments", []byte(`{"amount":2000,"currency":"usd"}`))
	hash4 := idempotency.ComputeHash("GET", "/v1/payments", []byte(`{"amount":1000,"currency":"usd"}`))

	if !bytes.Equal(hash1, hash2) {
		t.Errorf("expected hashes to match for identical inputs")
	}
	if bytes.Equal(hash1, hash3) {
		t.Errorf("expected different hashes for different body")
	}
	if bytes.Equal(hash1, hash4) {
		t.Errorf("expected different hashes for different method")
	}
}

func TestValidateKey(t *testing.T) {
	if err := idempotency.ValidateKey("valid-key-123"); err != nil {
		t.Errorf("unexpected error for valid key: %v", err)
	}

	if err := idempotency.ValidateKey(""); err == nil {
		t.Errorf("expected error for empty key")
	}

	longKey := make([]byte, 256)
	if err := idempotency.ValidateKey(string(longKey)); err == nil {
		t.Errorf("expected error for key exceeding 255 chars")
	}
}

func TestIdempotencyStatsQueries(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://minipay:minipay@localhost:5432/minipay?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pool, err := platform.ConnectDB(ctx, dbURL)
	if err != nil {
		t.Skipf("skipping db query test: %v", err)
	}
	defer pool.Close()

	queries := db.New(pool)
	stats, err := queries.GetIdempotencyStats(ctx)
	if err != nil {
		t.Fatalf("failed to query stats: %v", err)
	}
	if stats.TotalKeys < 0 || stats.TotalReplays < 0 || stats.TotalConflicts < 0 {
		t.Errorf("stats counts must not be negative: %+v", stats)
	}

	dupCheck, err := queries.CheckDuplicatePayments(ctx)
	if err != nil {
		t.Fatalf("failed to query duplicate payments: %v", err)
	}
	if dupCheck.DuplicatePayments != 0 {
		t.Errorf("expected 0 duplicate payments, got %d", dupCheck.DuplicatePayments)
	}

	keys, err := queries.ListRecentIdempotencyKeys(ctx, 5)
	if err != nil {
		t.Fatalf("failed to list recent keys: %v", err)
	}
	if len(keys) > 5 {
		t.Errorf("expected at most 5 keys, got %d", len(keys))
	}
}
