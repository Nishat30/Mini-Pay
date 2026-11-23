// api.go wires up and starts the HTTP API server.
// It owns server lifecycle: listening, graceful shutdown on SIGTERM.
// It must not contain handler logic — that belongs in internal/api.
// It relies on platform.Config for all tunables.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/minipay/minipay/internal/api"
	"github.com/minipay/minipay/internal/platform"
	"github.com/minipay/minipay/internal/webhooks"
)

func runAPI(cfg *platform.Config, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Connect to Postgres.
	db, err := platform.ConnectDB(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	defer db.Close()

	// Connect to Redis (nil if no URL configured).
	rdb, err := platform.ConnectRedis(ctx, cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis: %w", err)
	}

	policy := webhooks.SecurityPolicy{
		AllowPrivate:  cfg.AllowPrivateWebhooks,
		AllowInsecure: cfg.AllowInsecureWebhooks,
	}
	router := api.NewRouter(logger, db, rdb, policy)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.APIPort),
		Handler:      router,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in a goroutine.
	errCh := make(chan error, 1)
	go func() {
		logger.Info("api server starting", "port", cfg.APIPort)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	// Wait for shutdown signal or server error.
	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		return err
	}

	// Graceful shutdown: wait up to 30 seconds for in-flight requests.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server shutdown: %w", err)
	}

	logger.Info("api server stopped")
	return nil
}
