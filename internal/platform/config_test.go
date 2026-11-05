// config_test.go tests configuration loading and validation.
// It owns verifying the sk_live_ rejection invariant.
package platform

import (
	"os"
	"testing"
)

func TestLoadConfig_Defaults(t *testing.T) {
	// Clear any env that might interfere.
	os.Unsetenv("STRIPE_API_KEY")
	os.Unsetenv("DATABASE_URL")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.APIPort != 8080 {
		t.Errorf("expected default APIPort 8080, got %d", cfg.APIPort)
	}
	if cfg.DefaultProcessor != "simulator" {
		t.Errorf("expected default processor simulator, got %s", cfg.DefaultProcessor)
	}
}

func TestLoadConfig_RejectsLiveStripeKey(t *testing.T) {
	t.Setenv("STRIPE_API_KEY", "sk_live_abc123")
	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for sk_live_ key, got nil")
	}
}

func TestLoadConfig_AcceptsTestStripeKey(t *testing.T) {
	t.Setenv("STRIPE_API_KEY", "sk_test_abc123")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.StripeAPIKey != "sk_test_abc123" {
		t.Errorf("expected stripe key to be set, got %s", cfg.StripeAPIKey)
	}
}

func TestRedactDSN(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			"postgres://minipay:secret@localhost:5432/minipay",
			"postgres://minipay:***@localhost:5432/minipay",
		},
		{
			"postgres://localhost:5432/minipay",
			"postgres://localhost:5432/minipay",
		},
	}

	for _, tt := range tests {
		got := RedactDSN(tt.input)
		if got != tt.expected {
			t.Errorf("RedactDSN(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
