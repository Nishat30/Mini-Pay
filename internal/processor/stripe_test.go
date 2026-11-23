package processor_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/minipay/minipay/internal/processor"
)

func TestNewStripeAdapter_RejectsLiveKey(t *testing.T) {
	_, err := processor.NewStripeAdapter("sk_live_1234567890", nil)
	if !errors.Is(err, processor.ErrLiveKeyRejected) {
		t.Fatalf("expected ErrLiveKeyRejected, got %v", err)
	}

	adapter, err := processor.NewStripeAdapter("sk_test_1234567890", nil)
	if err != nil {
		t.Fatalf("expected test key to be accepted, got: %v", err)
	}
	if adapter == nil {
		t.Fatalf("expected non-nil adapter")
	}
}

func TestStripeAdapter_ChargeSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk_test_valid" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Idempotency-Key") != "pay_123" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"ch_stripe_mock_999","status":"succeeded","paid":true}`))
	}))
	defer server.Close()

	adapter, err := processor.NewStripeAdapter("sk_test_valid", server.Client())
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}

	adapter.SetBaseURL(server.URL)

	req := processor.ChargeRequest{
		PaymentID: "pay_123",
		Amount:    1000,
		Currency:  "usd",
	}

	result, err := adapter.Charge(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected charge error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected charge success, got failure: %s", result.FailureCode)
	}
	if result.ProcessorRef != "ch_stripe_mock_999" {
		t.Errorf("expected processor_ref ch_stripe_mock_999, got %s", result.ProcessorRef)
	}
}
