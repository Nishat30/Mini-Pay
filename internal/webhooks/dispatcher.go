package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/minipay/minipay/internal/platform"
	"github.com/minipay/minipay/internal/platform/db"
)

const (
	DefaultMaxAttempts    = 5
	DefaultInitialBackoff = 10 * time.Second
	DefaultRequestTimeout = 5 * time.Second
)

// WebhookEnvelope represents the standardized JSON structure delivered to merchant endpoints.
type WebhookEnvelope struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	CreatedAt string         `json:"created_at"`
	Data      map[string]any `json:"data"`
}

// Dispatcher manages polling, signing, delivery, retry schedules, and dead-lettering for webhooks.
type Dispatcher struct {
	pool           *pgxpool.Pool
	queries        *db.Queries
	client         *http.Client
	clock          platform.Clock
	policy         SecurityPolicy
	logger         *slog.Logger
	maxAttempts    int32
	initialBackoff time.Duration
	batchSize      int32
	pollInterval   time.Duration
}

// Config holds initialization settings for the Dispatcher.
type Config struct {
	Policy         SecurityPolicy
	MaxAttempts    int32
	InitialBackoff time.Duration
	RequestTimeout time.Duration
	BatchSize      int32
	PollInterval   time.Duration
}

// NewDispatcher constructs a new webhook Dispatcher.
func NewDispatcher(
	pool *pgxpool.Pool,
	queries *db.Queries,
	clock platform.Clock,
	logger *slog.Logger,
	cfg Config,
) *Dispatcher {
	if clock == nil {
		clock = platform.RealClock{}
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = DefaultMaxAttempts
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = DefaultInitialBackoff
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = DefaultRequestTimeout
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 10
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 200 * time.Millisecond
	}

	client := NewSafeHTTPClient(cfg.Policy, cfg.RequestTimeout)

	return &Dispatcher{
		pool:           pool,
		queries:        queries,
		client:         client,
		clock:          clock,
		policy:         cfg.Policy,
		logger:         logger,
		maxAttempts:    cfg.MaxAttempts,
		initialBackoff: cfg.InitialBackoff,
		batchSize:      cfg.BatchSize,
		pollInterval:   cfg.PollInterval,
	}
}

// Run executes the polling loop until the context is cancelled.
func (d *Dispatcher) Run(ctx context.Context) error {
	d.logger.Info("starting webhook dispatcher loop")
	ticker := time.NewTicker(d.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("stopping webhook dispatcher loop")
			return ctx.Err()
		case <-ticker.C:
			processed, err := d.DispatchBatch(ctx)
			if err != nil && ctx.Err() == nil {
				d.logger.Error("error in webhook dispatch batch", "error", err)
			}
			if processed > 0 {
				d.logger.Debug("processed webhook events", "count", processed)
			}
		}
	}
}

// DispatchBatch processes pending outbox notification events and retries pending deliveries.
// Returns the total count of processed events and deliveries.
func (d *Dispatcher) DispatchBatch(ctx context.Context) (int, error) {
	total := 0

	// Step 1: Claim new outbox notification events (payment.succeeded, payment.failed, etc.)
	outboxEvents, err := d.queries.ClaimOutboxEventsForWebhooks(ctx, d.batchSize)
	if err != nil {
		return 0, fmt.Errorf("failed to claim outbox webhook events: %w", err)
	}

	for _, event := range outboxEvents {
		total++
		if err := d.enqueueDelivery(ctx, event); err != nil {
			d.logger.Error("failed to enqueue webhook delivery", "event_id", event.ID, "error", err)
		}
	}

	// Step 2: Claim and deliver pending webhook attempts
	deliveries, err := d.queries.ClaimPendingDeliveries(ctx, d.batchSize)
	if err != nil {
		return total, fmt.Errorf("failed to claim pending deliveries: %w", err)
	}

	for _, delivery := range deliveries {
		total++
		if err := d.deliver(ctx, delivery); err != nil {
			d.logger.Error("failed to deliver webhook", "delivery_id", delivery.ID, "error", err)
		}
	}

	return total, nil
}

func (d *Dispatcher) enqueueDelivery(ctx context.Context, event db.OutboxEvents) error {
	whdID := platform.NewID(platform.PrefixWebhookDelivery)

	delivery, err := d.queries.CreateWebhookDelivery(ctx, db.CreateWebhookDeliveryParams{
		ID:         whdID,
		MerchantID: event.MerchantID,
		EventID:    event.ID,
	})
	if err != nil {
		return fmt.Errorf("failed to create delivery record: %w", err)
	}

	// Check if merchant has a webhook URL configured
	merchant, err := d.queries.GetMerchantByID(ctx, event.MerchantID)
	if err != nil {
		return fmt.Errorf("failed to get merchant %s: %w", event.MerchantID, err)
	}

	if !merchant.WebhookUrl.Valid || merchant.WebhookUrl.String == "" {
		// Merchant has not configured a webhook endpoint; mark delivery succeeded and outbox processed
		_ = d.queries.UpdateDeliverySuccess(ctx, db.UpdateDeliverySuccessParams{
			ID:         delivery.ID,
			LastStatus: pgtype.Int4{Int32: 204, Valid: true},
		})
		_ = d.queries.MarkOutboxProcessed(ctx, event.ID)
		return nil
	}

	return nil
}

func (d *Dispatcher) deliver(ctx context.Context, delivery db.WebhookDeliveries) error {
	merchant, err := d.queries.GetMerchantByID(ctx, delivery.MerchantID)
	if err != nil {
		return fmt.Errorf("failed to lookup merchant %s: %w", delivery.MerchantID, err)
	}

	if !merchant.WebhookUrl.Valid || merchant.WebhookUrl.String == "" {
		_ = d.queries.UpdateDeliverySuccess(ctx, db.UpdateDeliverySuccessParams{
			ID:         delivery.ID,
			LastStatus: pgtype.Int4{Int32: 204, Valid: true},
		})
		_ = d.queries.MarkOutboxProcessed(ctx, delivery.EventID)
		return nil
	}

	rawURL := merchant.WebhookUrl.String
	targetURL, err := ValidateURL(rawURL, d.policy)
	if err != nil {
		return d.recordFailure(ctx, delivery, 0, fmt.Sprintf("invalid webhook URL: %v", err))
	}

	outboxEvent, err := d.queries.GetOutboxEventByID(ctx, delivery.EventID)
	if err != nil {
		return fmt.Errorf("failed to lookup outbox event %d: %w", delivery.EventID, err)
	}

	var data map[string]any
	if err := json.Unmarshal(outboxEvent.Payload, &data); err != nil {
		data = map[string]any{"raw": string(outboxEvent.Payload)}
	}

	eventID := platform.NewID(platform.PrefixEvent)
	envelope := WebhookEnvelope{
		ID:        eventID,
		Type:      outboxEvent.Type,
		CreatedAt: outboxEvent.CreatedAt.Time.Format(time.RFC3339),
		Data:      data,
	}

	bodyBytes, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("failed to serialize webhook envelope: %w", err)
	}

	now := d.clock.Now()
	sigHeader := SignPayload(bodyBytes, merchant.WebhookSecret, now.Unix())

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		return d.recordFailure(ctx, delivery, 0, fmt.Sprintf("failed to create HTTP request: %v", err))
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(SignatureHeader, sigHeader)
	req.Header.Set("X-Delivery-Id", delivery.ID)
	req.Header.Set("User-Agent", "MiniPay-Webhooks/1.0")

	resp, err := d.client.Do(req)
	if err != nil {
		return d.recordFailure(ctx, delivery, 0, err.Error())
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// Delivery succeeded!
		err = d.queries.UpdateDeliverySuccess(ctx, db.UpdateDeliverySuccessParams{
			ID:         delivery.ID,
			LastStatus: pgtype.Int4{Int32: int32(resp.StatusCode), Valid: true},
		})
		if err != nil {
			return fmt.Errorf("failed to update delivery success: %w", err)
		}
		return d.queries.MarkOutboxProcessed(ctx, delivery.EventID)
	}

	return d.recordFailure(ctx, delivery, resp.StatusCode, fmt.Sprintf("HTTP %d", resp.StatusCode))
}

func (d *Dispatcher) recordFailure(ctx context.Context, delivery db.WebhookDeliveries, statusCode int, errStr string) error {
	now := d.clock.Now()

	statusParam := pgtype.Int4{Valid: statusCode > 0, Int32: int32(statusCode)}
	errParam := pgtype.Text{Valid: errStr != "", String: errStr}

	if delivery.Attempts >= d.maxAttempts {
		// Max attempts reached: transition to dead letter
		d.logger.Warn("webhook delivery exhausted max attempts, dead-lettering",
			"delivery_id", delivery.ID,
			"attempts", delivery.Attempts,
			"status", statusCode,
			"error", errStr,
		)

		err := d.queries.UpdateDeliveryFailure(ctx, db.UpdateDeliveryFailureParams{
			ID:            delivery.ID,
			State:         "dead",
			NextAttemptAt: pgtype.Timestamptz{Time: now, Valid: true},
			LastStatus:    statusParam,
			LastError:     errParam,
		})
		if err != nil {
			return err
		}
		// Mark outbox processed so it doesn't loop forever
		return d.queries.MarkOutboxProcessed(ctx, delivery.EventID)
	}

	// Calculate exponential backoff: initialBackoff * 2^(attempts-1)
	shift := delivery.Attempts - 1
	if shift < 0 {
		shift = 0
	}
	if shift > 10 {
		shift = 10
	}
	backoff := d.initialBackoff * (1 << shift)
	if backoff > 24*time.Hour {
		backoff = 24 * time.Hour
	}

	nextAttemptAt := now.Add(backoff)

	return d.queries.UpdateDeliveryFailure(ctx, db.UpdateDeliveryFailureParams{
		ID:            delivery.ID,
		State:         "pending",
		NextAttemptAt: pgtype.Timestamptz{Time: nextAttemptAt, Valid: true},
		LastStatus:    statusParam,
		LastError:     errParam,
	})
}
