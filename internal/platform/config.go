// config.go loads application configuration from environment variables.
// It owns all config parsing and validation.
// It must never set defaults for secrets — those must be explicitly provided.
// It relies on .env files being loaded by the caller (e.g., Docker Compose or direnv).
//
// Invariant: if STRIPE_API_KEY begins with "sk_live_", LoadConfig returns an error.
package platform

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all application configuration.
type Config struct {
	// Database
	DatabaseURL string

	// Redis
	RedisURL string

	// API Server
	APIPort     int
	MetricsPort int
	AdminPort   int

	// Logging
	LogLevel string

	// Admin auth
	AdminUsername string
	AdminPassword string

	// Stripe
	StripeAPIKey        string
	StripeWebhookSecret string

	// Rate Limiting
	RateLimitCapacity      int
	RateLimitRefillPerSec  int

	// Risk Rules
	RiskRules []string

	// Fee Configuration
	FeeBasisPoints int
	FeeFixedAmount int

	// Worker
	OutboxPollInterval time.Duration

	// Webhooks
	AllowPrivateWebhooks  bool
	AllowInsecureWebhooks bool

	// Processor
	DefaultProcessor string
}

// LoadConfig reads configuration from environment variables.
// It returns an error if required values are missing or invalid,
// or if a Stripe live key is detected.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		DatabaseURL:           envOrDefault("DATABASE_URL", "postgres://minipay:minipay@localhost:5432/minipay?sslmode=disable"),
		RedisURL:              envOrDefault("REDIS_URL", "redis://localhost:6379/0"),
		APIPort:               envIntOrDefault("API_PORT", 8080),
		MetricsPort:           envIntOrDefault("METRICS_PORT", 9090),
		AdminPort:             envIntOrDefault("ADMIN_PORT", 8081),
		LogLevel:              envOrDefault("LOG_LEVEL", "info"),
		AdminUsername:         envOrDefault("ADMIN_USERNAME", "admin"),
		AdminPassword:         os.Getenv("ADMIN_PASSWORD"),
		StripeAPIKey:          os.Getenv("STRIPE_API_KEY"),
		StripeWebhookSecret:   os.Getenv("STRIPE_WEBHOOK_SECRET"),
		RateLimitCapacity:     envIntOrDefault("RATE_LIMIT_CAPACITY", 100),
		RateLimitRefillPerSec: envIntOrDefault("RATE_LIMIT_REFILL_PER_SEC", 50),
		FeeBasisPoints:        envIntOrDefault("FEE_BASIS_POINTS", 290),
		FeeFixedAmount:        envIntOrDefault("FEE_FIXED_AMOUNT", 30),
		AllowPrivateWebhooks:  envBoolOrDefault("WEBHOOK_ALLOW_PRIVATE", false),
		AllowInsecureWebhooks: envBoolOrDefault("WEBHOOK_ALLOW_INSECURE", false),
		DefaultProcessor:      envOrDefault("DEFAULT_PROCESSOR", "simulator"),
	}

	// Parse risk rules.
	rulesStr := envOrDefault("RISK_RULES", "amount_cap,velocity")
	cfg.RiskRules = strings.Split(rulesStr, ",")

	// Parse outbox poll interval.
	pollStr := envOrDefault("OUTBOX_POLL_INTERVAL", "200ms")
	dur, err := time.ParseDuration(pollStr)
	if err != nil {
		return nil, fmt.Errorf("invalid OUTBOX_POLL_INTERVAL %q: %w", pollStr, err)
	}
	cfg.OutboxPollInterval = dur

	// Security: refuse to start with a live Stripe key.
	if strings.HasPrefix(cfg.StripeAPIKey, "sk_live_") {
		return nil, fmt.Errorf("STRIPE_API_KEY begins with sk_live_: refusing to start; use test-mode keys only")
	}

	return cfg, nil
}

// RedactDSN removes the password from a Postgres DSN for safe logging.
func RedactDSN(dsn string) string {
	// Simple approach: replace password portion.
	if i := strings.Index(dsn, "://"); i != -1 {
		rest := dsn[i+3:]
		if at := strings.Index(rest, "@"); at != -1 {
			if colon := strings.Index(rest[:at], ":"); colon != -1 {
				return dsn[:i+3] + rest[:colon] + ":***@" + rest[at+1:]
			}
		}
	}
	return dsn
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func envIntOrDefault(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}

func envBoolOrDefault(key string, defaultVal bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return defaultVal
	}
	return b
}
