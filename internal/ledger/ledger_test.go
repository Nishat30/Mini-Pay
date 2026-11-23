package ledger_test

import (
	"testing"

	"github.com/minipay/minipay/internal/ledger"
)

func TestCalculateFee(t *testing.T) {
	tests := []struct {
		name        string
		amount      int64
		bps         int64
		fixed       int64
		wantFee     int64
		expectError bool
	}{
		{
			name:        "1000 minor units with 290 bps + 30",
			amount:      1000,
			bps:         290,
			fixed:       30,
			wantFee:     59, // 1000 * 290 / 10000 = 29 + 30 = 59
			expectError: false,
		},
		{
			name:        "10000 minor units with 290 bps + 30",
			amount:      10000,
			bps:         290,
			fixed:       30,
			wantFee:     320, // 10000 * 290 / 10000 = 290 + 30 = 320
			expectError: false,
		},
		{
			name:        "fee exceeds amount rejects",
			amount:      50,
			bps:         5000,
			fixed:       30,
			expectError: true, // 50 * 5000 / 10000 = 25 + 30 = 55 >= 50
		},
		{
			name:        "zero amount rejects",
			amount:      0,
			bps:         290,
			fixed:       30,
			expectError: true,
		},
		{
			name:        "negative amount rejects",
			amount:      -100,
			bps:         290,
			fixed:       30,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ledger.CalculateFee(tt.amount, tt.bps, tt.fixed)
			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tt.wantFee {
					t.Fatalf("expected %d, got %d", tt.wantFee, got)
				}
			}
		})
	}
}

func TestCaptureEntries(t *testing.T) {
	accounts := ledger.Accounts{
		ClearingAccountID: "acc_clearing_123",
		MerchantAccountID: "acc_merchant_456",
		PlatformFeesID:    "acc_fees_789",
	}

	entries, err := ledger.CaptureEntries(accounts, "usd", 1000, 59)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Clearing Debit
	if entries[0].AccountID != accounts.ClearingAccountID || entries[0].Direction != ledger.DirectionDebit || entries[0].Amount != 1000 {
		t.Errorf("invalid clearing entry: %+v", entries[0])
	}

	// Merchant Payable Credit
	if entries[1].AccountID != accounts.MerchantAccountID || entries[1].Direction != ledger.DirectionCredit || entries[1].Amount != 941 {
		t.Errorf("invalid merchant payable entry: %+v", entries[1])
	}

	// Platform Fees Credit
	if entries[2].AccountID != accounts.PlatformFeesID || entries[2].Direction != ledger.DirectionCredit || entries[2].Amount != 59 {
		t.Errorf("invalid platform fees entry: %+v", entries[2])
	}

	// Sum debits == Sum credits
	var debits, credits int64
	for _, e := range entries {
		if e.Direction == ledger.DirectionDebit {
			debits += e.Amount
		} else {
			credits += e.Amount
		}
	}
	if debits != credits {
		t.Fatalf("debits %d != credits %d", debits, credits)
	}
}

func TestRefundEntries(t *testing.T) {
	accounts := ledger.Accounts{
		ClearingAccountID: "acc_clearing_123",
		MerchantAccountID: "acc_merchant_456",
		PlatformFeesID:    "acc_fees_789",
	}

	entries, err := ledger.RefundEntries(accounts, "usd", 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if entries[0].Direction != ledger.DirectionDebit || entries[0].Amount != 500 {
		t.Errorf("expected merchant debit 500, got %+v", entries[0])
	}
	if entries[1].Direction != ledger.DirectionCredit || entries[1].Amount != 500 {
		t.Errorf("expected clearing credit 500, got %+v", entries[1])
	}
}
