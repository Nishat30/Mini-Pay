// Package ratelimit implements per-merchant token bucket rate limiting.
// It provides a Limiter interface with Redis and in-memory implementations.
// It must fail open on errors: allow the request and log the failure.
package ratelimit

