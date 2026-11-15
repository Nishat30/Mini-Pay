// migrate.go runs database migrations using golang-migrate.
// It owns the migration lifecycle: up, down, and status.
// It must never modify migration files at runtime.
// It relies on db/migrations/ containing numbered .sql files.
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/minipay/minipay/internal/platform"
)

func runMigrate(cfg *platform.Config, logger *slog.Logger) error {
	direction := "up"
	if len(os.Args) > 2 {
		direction = os.Args[2]
	}

	logger.Info("running migrations", "direction", direction, "database", platform.RedactDSN(cfg.DatabaseURL))

	n, err := platform.RunMigrations(cfg.DatabaseURL, direction)
	if err != nil {
		return fmt.Errorf("migrate %s: %w", direction, err)
	}

	logger.Info("migrations complete", "applied", n)
	return nil
}
