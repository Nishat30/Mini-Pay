// database.go manages PostgreSQL connection pooling via pgx.
// It owns database connectivity and health checking.
// It must never run queries — that belongs in repository or sqlc layers.
// It relies on pgx/v5 for the connection pool.
package platform

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ConnectDB creates a connection pool to PostgreSQL.
// The caller must call pool.Close() when done.
func ConnectDB(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}

	// Sensible defaults for a payments service.
	config.MaxConns = 20
	config.MinConns = 2

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}
