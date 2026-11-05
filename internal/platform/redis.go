// redis.go manages Redis connectivity for rate limiting.
// It owns Redis connection lifecycle and health checking.
// It must never be used for data that requires durability — use Postgres.
// It relies on the go-redis/v9 client library.
package platform

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// ConnectRedis creates a Redis client from a URL.
// Returns nil client and nil error if the URL is empty (Redis is optional).
func ConnectRedis(ctx context.Context, redisURL string) (*redis.Client, error) {
	if redisURL == "" {
		return nil, nil
	}

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}

	client := redis.NewClient(opts)

	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return client, nil
}
