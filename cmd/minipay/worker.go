// worker.go wires up and starts the background worker process.
// It owns worker lifecycle: polling loops, graceful shutdown on SIGTERM.
// It must not contain business logic — that belongs in internal packages.
// It relies on the same database as the API but shares no memory with it.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/minipay/minipay/internal/ledger"
	"github.com/minipay/minipay/internal/outbox"
	"github.com/minipay/minipay/internal/platform"
	"github.com/minipay/minipay/internal/platform/db"
	"github.com/minipay/minipay/internal/processor"
	"github.com/minipay/minipay/internal/webhooks"
)

func runWorker(cfg *platform.Config, logger *slog.Logger) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger.Info("connecting worker to database", "url", platform.RedactDSN(cfg.DatabaseURL))
	pool, err := platform.ConnectDB(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	queries := db.New(pool)
	ledgerService := ledger.NewService(queries)
	psp := processor.NewSimulator()
	clock := platform.RealClock{}

	// 1. Payment Outbox Worker
	paymentWorker := outbox.NewWorker(pool, queries, ledgerService, psp, clock, logger)

	// 2. Webhook Dispatcher
	webhookDispatcher := webhooks.NewDispatcher(pool, queries, clock, logger, webhooks.Config{
		Policy: webhooks.SecurityPolicy{
			AllowPrivate:  cfg.AllowPrivateWebhooks,
			AllowInsecure: cfg.AllowInsecureWebhooks,
		},
		PollInterval: cfg.OutboxPollInterval,
	})

	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	// Start payment worker
	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.Info("payment outbox worker started")
		if err := paymentWorker.Run(ctx); err != nil && err != context.Canceled {
			errCh <- fmt.Errorf("payment worker error: %w", err)
		}
	}()

	// Start webhook dispatcher
	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.Info("webhook dispatcher started")
		if err := webhookDispatcher.Run(ctx); err != nil && err != context.Canceled {
			errCh <- fmt.Errorf("webhook dispatcher error: %w", err)
		}
	}()

	// Wait for context cancellation or fatal worker error
	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received, stopping workers")
	case err := <-errCh:
		cancel()
		return err
	}

	wg.Wait()
	logger.Info("all workers stopped cleanly")
	return nil
}
