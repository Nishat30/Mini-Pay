package payments_test

import (
	"errors"
	"testing"

	"github.com/minipay/minipay/internal/payments"
)

func TestTransition(t *testing.T) {
	tests := []struct {
		name      string
		from      payments.PaymentStatus
		event     payments.PaymentEvent
		want      payments.PaymentStatus
		wantError bool
	}{
		// Valid initial transitions
		{
			name:      "initial creation to processing",
			from:      "",
			event:     payments.EventProcess,
			want:      payments.StatusProcessing,
			wantError: false,
		},
		{
			name:      "initial creation with invalid event fail",
			from:      "",
			event:     payments.EventFail,
			wantError: true,
		},

		// From Processing
		{
			name:      "processing to succeeded",
			from:      payments.StatusProcessing,
			event:     payments.EventSucceed,
			want:      payments.StatusSucceeded,
			wantError: false,
		},
		{
			name:      "processing to failed",
			from:      payments.StatusProcessing,
			event:     payments.EventFail,
			want:      payments.StatusFailed,
			wantError: false,
		},
		{
			name:      "processing to requires_review",
			from:      payments.StatusProcessing,
			event:     payments.EventRequireReview,
			want:      payments.StatusRequiresReview,
			wantError: false,
		},
		{
			name:      "processing to refunded (illegal direct refund)",
			from:      payments.StatusProcessing,
			event:     payments.EventRefund,
			wantError: true,
		},

		// From RequiresReview
		{
			name:      "requires_review to succeeded",
			from:      payments.StatusRequiresReview,
			event:     payments.EventSucceed,
			want:      payments.StatusSucceeded,
			wantError: false,
		},
		{
			name:      "requires_review to failed",
			from:      payments.StatusRequiresReview,
			event:     payments.EventFail,
			want:      payments.StatusFailed,
			wantError: false,
		},
		{
			name:      "requires_review with process event (illegal)",
			from:      payments.StatusRequiresReview,
			event:     payments.EventProcess,
			wantError: true,
		},

		// From Succeeded
		{
			name:      "succeeded to refunded",
			from:      payments.StatusSucceeded,
			event:     payments.EventRefund,
			want:      payments.StatusRefunded,
			wantError: false,
		},
		{
			name:      "succeeded cannot re-succeed",
			from:      payments.StatusSucceeded,
			event:     payments.EventSucceed,
			wantError: true,
		},
		{
			name:      "succeeded cannot fail",
			from:      payments.StatusSucceeded,
			event:     payments.EventFail,
			wantError: true,
		},

		// Terminal states cannot transition
		{
			name:      "failed cannot succeed",
			from:      payments.StatusFailed,
			event:     payments.EventSucceed,
			wantError: true,
		},
		{
			name:      "refunded cannot succeed",
			from:      payments.StatusRefunded,
			event:     payments.EventSucceed,
			wantError: true,
		},
		{
			name:      "refunded cannot refund again",
			from:      payments.StatusRefunded,
			event:     payments.EventRefund,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := payments.Transition(tt.from, tt.event)
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !errors.Is(err, payments.ErrInvalidTransition) {
					t.Fatalf("expected ErrInvalidTransition, got %v", err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tt.want {
					t.Fatalf("expected %q, got %q", tt.want, got)
				}
			}
		})
	}
}

func TestIsTerminal(t *testing.T) {
	if payments.IsTerminal(payments.StatusProcessing) {
		t.Errorf("processing should not be terminal")
	}
	if payments.IsTerminal(payments.StatusRequiresReview) {
		t.Errorf("requires_review should not be terminal")
	}
	if payments.IsTerminal(payments.StatusSucceeded) {
		t.Errorf("succeeded should not be terminal")
	}
	if !payments.IsTerminal(payments.StatusFailed) {
		t.Errorf("failed should be terminal")
	}
	if !payments.IsTerminal(payments.StatusRefunded) {
		t.Errorf("refunded should be terminal")
	}
}
