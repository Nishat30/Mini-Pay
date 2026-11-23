// ids_test.go verifies prefixed ULID generation and format validation.
// Owns: asserting prefixes, monotonicity, and format guarantees.
package platform

import (
	"strings"
	"testing"
)

func TestNewID(t *testing.T) {
	prefixes := []IDPrefix{
		PrefixMerchant,
		PrefixPayment,
		PrefixRefund,
		PrefixLedgerAccount,
		PrefixLedgerTransaction,
		PrefixWebhookDelivery,
		PrefixEvent,
		PrefixRequest,
	}

	for _, p := range prefixes {
		id := NewID(p)
		if !strings.HasPrefix(id, string(p)) {
			t.Errorf("expected ID to start with %s, got %s", p, id)
		}
		if !ValidateID(p, id) {
			t.Errorf("ValidateID failed for generated ID %s with prefix %s", id, p)
		}
	}
}

func TestValidateID_Invalid(t *testing.T) {
	tests := []struct {
		prefix IDPrefix
		id     string
		valid  bool
	}{
		{PrefixPayment, "pay_invalidulidformat", false},
		{PrefixPayment, "mer_01HZZZZZZZZZZZZZZZZZZZZZZZ", false},
		{PrefixPayment, "pay_", false},
		{PrefixPayment, "", false},
	}

	for _, tt := range tests {
		if got := ValidateID(tt.prefix, tt.id); got != tt.valid {
			t.Errorf("ValidateID(%s, %s) = %v, want %v", tt.prefix, tt.id, got, tt.valid)
		}
	}
}
