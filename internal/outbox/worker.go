// Package outbox implements the transactional outbox pattern and asynchronous worker loop for MiniPay.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/minipay/minipay/internal/ledger"
	"github.com/minipay/minipay/internal/platform"
	"github.com/minipay/minipay/internal/platform/db"
	"github.com/minipay/minipay/internal/processor"
)

// PaymentProcessPayload is the payload stored in outbox for 'payment.process' events.
type PaymentProcessPayload struct {
	PaymentID string `json:"payment_id"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Simulate  string `json:"simulate"`
}

// Worker processes background events from outbox_events.
type Worker struct {
	pool          *pgxpool.Pool
	queries       *db.Queries
	ledgerService *ledger.Service
	processor     processor.Processor
	clock         platform.Clock
	logger        *slog.Logger
	batchSize     int32
	pollInterval  time.Duration
}

// NewWorker creates an outbox worker instance.
func NewWorker(
	pool *pgxpool.Pool,
	queries *db.Queries,
	ledgerService *ledger.Service,
	psp processor.Processor,
	clock platform.Clock,
	logger *slog.Logger,
) *Worker {
	if clock == nil {
		clock = platform.RealClock{}
	}
	return &Worker{
		pool:          pool,
		queries:       queries,
		ledgerService: ledgerService,
		processor:     psp,
		clock:         clock,
		logger:        logger,
		batchSize:     10,
		pollInterval:  200 * time.Millisecond,
	}
}

// Run executes the worker loop until the context is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	w.logger.Info("starting outbox worker loop")
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("stopping outbox worker loop")
			return ctx.Err()
		case <-ticker.C:
			processed, err := w.ProcessBatch(ctx)
			if err != nil && ctx.Err() == nil {
				w.logger.Error("error processing outbox batch", "error", err)
			}
			if processed > 0 {
				w.logger.Debug("processed outbox events", "count", processed)
			}
		}
	}
}

// ProcessBatch claims and processes up to batchSize outbox events.
// It returns the number of events processed.
func (w *Worker) ProcessBatch(ctx context.Context) (int, error) {
	events, err := w.queries.ClaimOutboxEvents(ctx, db.ClaimOutboxEventsParams{
		Limit: w.batchSize,
		Type:  "payment.process",
	})
	if err != nil {
		return 0, fmt.Errorf("failed to claim outbox events: %w", err)
	}

	if len(events) == 0 {
		return 0, nil
	}

	for _, event := range events {
		if err := w.processEvent(ctx, event); err != nil {
			w.logger.Error("failed to process event", "event_id", event.ID, "type", event.Type, "error", err)
		}
	}

	return len(events), nil
}

func (w *Worker) processEvent(ctx context.Context, event db.OutboxEvents) error {
	switch event.Type {
	case "payment.process":
		return w.handlePaymentProcess(ctx, event)
	default:
		// Unknown event type, mark as processed to prevent poison pill loops
		w.logger.Warn("unknown outbox event type, marking processed", "type", event.Type, "event_id", event.ID)
		return w.queries.MarkOutboxProcessed(ctx, event.ID)
	}
}

func (w *Worker) handlePaymentProcess(ctx context.Context, event db.OutboxEvents) error {
	var payload PaymentProcessPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		// Malformed JSON, cannot retry
		w.logger.Error("invalid outbox payload", "event_id", event.ID, "error", err)
		return w.queries.MarkOutboxProcessed(ctx, event.ID)
	}

	// 1. Call Payment Processor
	chargeRes, err := w.processor.Charge(ctx, processor.ChargeRequest{
		PaymentID: payload.PaymentID,
		Amount:    payload.Amount,
		Currency:  payload.Currency,
		Simulate:  payload.Simulate,
	})

	now := w.clock.Now()

	// Handle transient network retry
	if err != nil && chargeRes != nil && chargeRes.Retryable {
		backoff := time.Duration(math.Pow(2, float64(event.Attempts))) * time.Second
		if backoff > 5*time.Minute {
			backoff = 5 * time.Minute
		}
		retryAt := now.Add(backoff)
		return w.queries.ScheduleOutboxRetry(ctx, db.ScheduleOutboxRetryParams{
			ID:          event.ID,
			AvailableAt: pgtype.Timestamptz{Time: retryAt, Valid: true},
		})
	}

	// 2. Wrap state transition, ledger posting, and outbox update in a single DB transaction
	return platform.WithTx(ctx, w.pool, func(tx pgx.Tx) error {
		txQueries := w.queries.WithTx(tx)

		// Lock payment row
		payment, err := txQueries.GetPaymentByIDForUpdate(ctx, db.GetPaymentByIDForUpdateParams{
			ID:         payload.PaymentID,
			MerchantID: event.MerchantID,
		})
		if err != nil {
			return fmt.Errorf("failed to lock payment %s: %w", payload.PaymentID, err)
		}

		// If payment is already in terminal state, idempotently complete outbox event
		if payment.Status != "processing" {
			return txQueries.MarkOutboxProcessed(ctx, event.ID)
		}

		if chargeRes != nil && chargeRes.Success {
			// Update Payment to Succeeded
			_, err = txQueries.UpdatePaymentStatus(ctx, db.UpdatePaymentStatusParams{
				ID:           payment.ID,
				FromStatus:   "processing",
				ToStatus:     "succeeded",
				ProcessorRef: pgtype.Text{String: chargeRes.ProcessorRef, Valid: true},
			})
			if err != nil {
				return fmt.Errorf("failed to transition payment to succeeded: %w", err)
			}

			// Append Payment Event
			_, err = txQueries.InsertPaymentEvent(ctx, db.InsertPaymentEventParams{
				PaymentID:  payment.ID,
				FromStatus: pgtype.Text{String: "processing", Valid: true},
				ToStatus:   "succeeded",
				Reason:     pgtype.Text{String: "charge_succeeded", Valid: true},
			})
			if err != nil {
				return fmt.Errorf("failed to record payment event: %w", err)
			}

			// Execute Double-Entry Ledger Posting
			err = w.ledgerService.PostCapture(ctx, txQueries, payment.ID, payment.MerchantID, payment.Currency, payment.Amount)
			if err != nil {
				return fmt.Errorf("failed to post ledger capture: %w", err)
			}

			// Write payment.succeeded outbox event
			eventPayload, _ := json.Marshal(map[string]any{
				"payment_id":    payment.ID,
				"amount":        payment.Amount,
				"currency":      payment.Currency,
				"status":        "succeeded",
				"processor_ref": chargeRes.ProcessorRef,
			})
			_, err = txQueries.CreateOutboxEvent(ctx, db.CreateOutboxEventParams{
				MerchantID:  payment.MerchantID,
				AggregateID: payment.ID,
				Type:        "payment.succeeded",
				Payload:     eventPayload,
			})
			if err != nil {
				return fmt.Errorf("failed to enqueue payment.succeeded outbox event: %w", err)
			}

		} else {
			// Failed / Declined
			failureCode := "generic_decline"
			if chargeRes != nil && chargeRes.FailureCode != "" {
				failureCode = chargeRes.FailureCode
			}

			_, err = txQueries.UpdatePaymentStatus(ctx, db.UpdatePaymentStatusParams{
				ID:          payment.ID,
				FromStatus:  "processing",
				ToStatus:    "failed",
				FailureCode: pgtype.Text{String: failureCode, Valid: true},
			})
			if err != nil {
				return fmt.Errorf("failed to transition payment to failed: %w", err)
			}

			_, err = txQueries.InsertPaymentEvent(ctx, db.InsertPaymentEventParams{
				PaymentID:  payment.ID,
				FromStatus: pgtype.Text{String: "processing", Valid: true},
				ToStatus:   "failed",
				Reason:     pgtype.Text{String: failureCode, Valid: true},
			})
			if err != nil {
				return fmt.Errorf("failed to record payment failed event: %w", err)
			}

			eventPayload, _ := json.Marshal(map[string]any{
				"payment_id":   payment.ID,
				"amount":       payment.Amount,
				"currency":     payment.Currency,
				"status":       "failed",
				"failure_code": failureCode,
			})
			_, err = txQueries.CreateOutboxEvent(ctx, db.CreateOutboxEventParams{
				MerchantID:  payment.MerchantID,
				AggregateID: payment.ID,
				Type:        "payment.failed",
				Payload:     eventPayload,
			})
			if err != nil {
				return fmt.Errorf("failed to enqueue payment.failed outbox event: %w", err)
			}
		}

		// Mark current outbox event processed
		return txQueries.MarkOutboxProcessed(ctx, event.ID)
	})
}
