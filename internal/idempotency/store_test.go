package idempotency_test

import (
	"bytes"
	"testing"

	"github.com/minipay/minipay/internal/idempotency"
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
