package webhooks_test

import (
	"errors"
	"testing"
	"time"

	"github.com/minipay/minipay/internal/platform"
	"github.com/minipay/minipay/internal/webhooks"
)

func TestSignAndVerifyPayload(t *testing.T) {
	secret := "whsec_test_secret_12345"
	body := []byte(`{"event":"payment.succeeded","data":{"id":"pay_123"}}`)
	now := time.Unix(1700000000, 0)
	clock := platform.NewFakeClock(now)

	// 1. Sign
	header := webhooks.SignPayload(body, secret, now.Unix())
	if header == "" {
		t.Fatalf("expected non-empty signature header")
	}

	// 2. Verify valid
	err := webhooks.VerifySignature(body, header, secret, 5*time.Minute, clock)
	if err != nil {
		t.Fatalf("expected signature to be valid: %v", err)
	}

	// 3. Reject tampered body
	tamperedBody := []byte(`{"event":"payment.succeeded","data":{"id":"pay_TAMPERED"}}`)
	err = webhooks.VerifySignature(tamperedBody, header, secret, 5*time.Minute, clock)
	if !errors.Is(err, webhooks.ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature for tampered body, got: %v", err)
	}

	// 4. Reject wrong secret
	err = webhooks.VerifySignature(body, header, "wrong_secret", 5*time.Minute, clock)
	if !errors.Is(err, webhooks.ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature for wrong secret, got: %v", err)
	}

	// 5. Reject expired timestamp (replay attack simulation)
	futureClock := platform.NewFakeClock(now.Add(10 * time.Minute))
	err = webhooks.VerifySignature(body, header, secret, 5*time.Minute, futureClock)
	if !errors.Is(err, webhooks.ErrSignatureExpired) {
		t.Fatalf("expected ErrSignatureExpired for stale signature, got: %v", err)
	}

	// 6. Reject malformed header
	err = webhooks.VerifySignature(body, "malformed", secret, 5*time.Minute, clock)
	if !errors.Is(err, webhooks.ErrInvalidSignatureHeader) {
		t.Fatalf("expected ErrInvalidSignatureHeader for malformed header, got: %v", err)
	}
}
