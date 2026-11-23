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
	"syscall"

	"github.com/minipay/minipay/internal/ledger"
	"github.com/minipay/minipay/internal/outbox"
	"github.com/minipay/minipay/internal/platform"
	"github.com/minipay/minipay/internal/platform/db"
	"github.com/minipay/minipay/internal/processor"
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

	w := outbox.NewWorker(pool, queries, ledgerService, psp, clock, logger)

	logger.Info("worker started, listening for outbox events")
	if err := w.Run(ctx); err != nil && err != context.Canceled {
		return fmt.Errorf("worker run error: %w", err)
	}

	logger.Info("worker stopped cleanly")
	return nil
}
