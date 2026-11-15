// worker.go wires up and starts the background worker process.
// It owns worker lifecycle: polling loops, graceful shutdown on SIGTERM.
// It must not contain business logic — that belongs in internal packages.
// It relies on the same database as the API but shares no memory with it.
package main

import (
	"fmt"
	"log/slog"

	"github.com/minipay/minipay/internal/platform"
)

func runWorker(cfg *platform.Config, logger *slog.Logger) error {
	// TODO(phase-1): implement outbox relay, webhook dispatcher, cleanup loops.
	logger.Info("worker starting (not yet implemented)")
	return fmt.Errorf("worker not yet implemented")
}
