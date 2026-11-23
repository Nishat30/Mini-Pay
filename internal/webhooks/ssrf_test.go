package webhooks_test

import (
	"errors"
	"net"
	"testing"

	"github.com/minipay/minipay/internal/webhooks"
)

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		ip       string
		expected bool
	}{
		// Private / Blocked
		{"127.0.0.1", true},
		{"127.0.1.5", true},
		{"10.0.0.1", true},
		{"10.254.1.2", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true}, // AWS/cloud metadata
		{"169.254.1.1", true},     // link-local
		{"100.64.0.1", true},      // CGNAT
		{"0.0.0.0", true},
		{"::1", true},
		{"fc00::1", true},
		{"fe80::1", true},

		// Public IPs
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"93.184.216.34", false},
		{"2606:4700:4700::1111", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP %s", tt.ip)
			}
			got := webhooks.IsPrivateIP(ip)
			if got != tt.expected {
				t.Errorf("IsPrivateIP(%s) = %v, want %v", tt.ip, got, tt.expected)
			}
		})
	}
}

func TestValidateURL(t *testing.T) {
	strictPolicy := webhooks.SecurityPolicy{AllowPrivate: false, AllowInsecure: false}
	devPolicy := webhooks.SecurityPolicy{AllowPrivate: true, AllowInsecure: true}

	tests := []struct {
		name        string
		url         string
		policy      webhooks.SecurityPolicy
		expectError bool
		errIs       error
	}{
		{
			name:        "valid public https URL",
			url:         "https://api.example.com/webhooks",
			policy:      strictPolicy,
			expectError: false,
		},
		{
			name:        "http rejected by strict policy",
			url:         "http://api.example.com/webhooks",
			policy:      strictPolicy,
			expectError: true,
			errIs:       webhooks.ErrInvalidScheme,
		},
		{
			name:        "http allowed by dev policy",
			url:         "http://api.example.com/webhooks",
			policy:      devPolicy,
			expectError: false,
		},
		{
			name:        "direct loopback IP rejected by strict policy",
			url:         "https://127.0.0.1:8080/hook",
			policy:      strictPolicy,
			expectError: true,
			errIs:       webhooks.ErrPrivateIP,
		},
		{
			name:        "direct metadata IP rejected by strict policy",
			url:         "https://169.254.169.254/latest/meta-data",
			policy:      strictPolicy,
			expectError: true,
			errIs:       webhooks.ErrPrivateIP,
		},
		{
			name:        "direct loopback IP allowed by dev policy",
			url:         "http://127.0.0.1:8080/hook",
			policy:      devPolicy,
			expectError: false,
		},
		{
			name:        "user credentials in URL blocked",
			url:         "https://user:password@example.com/webhook",
			policy:      strictPolicy,
			expectError: true,
			errIs:       webhooks.ErrUserInfoBlocked,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := webhooks.ValidateURL(tt.url, tt.policy)
			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errIs != nil && !errors.Is(err, tt.errIs) {
					t.Fatalf("expected error %v, got %v", tt.errIs, err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}
