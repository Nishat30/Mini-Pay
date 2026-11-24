// Package ratelimit implements token-bucket rate limiting with Redis and in-memory fallback.
package ratelimit

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Result represents the evaluation of a rate limit check.
type Result struct {
	Allowed   bool
	Limit     int
	Remaining int
	ResetSec  int
}

// Limiter provides rate limiting evaluations.
type Limiter interface {
	Allow(ctx context.Context, key string, capacity int, refillPerSec int) (*Result, error)
}

// MemoryLimiter provides a local, in-memory token bucket implementation.
type MemoryLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens     float64
	lastRefill time.Time
}

// NewMemoryLimiter creates a new in-memory rate limiter.
func NewMemoryLimiter() *MemoryLimiter {
	return &MemoryLimiter{
		buckets: make(map[string]*bucket),
	}
}

// Allow evaluates a token bucket request in memory.
func (m *MemoryLimiter) Allow(ctx context.Context, key string, capacity int, refillPerSec int) (*Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	b, exists := m.buckets[key]
	if !exists {
		b = &bucket{
			tokens:     float64(capacity),
			lastRefill: now,
		}
		m.buckets[key] = b
	} else {
		// Refill tokens based on elapsed time
		elapsed := now.Sub(b.lastRefill).Seconds()
		b.tokens = math.Min(float64(capacity), b.tokens+(elapsed*float64(refillPerSec)))
		b.lastRefill = now
	}

	allowed := false
	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		allowed = true
	}

	remaining := int(math.Floor(b.tokens))
	if remaining < 0 {
		remaining = 0
	}

	resetSec := 0
	if remaining < capacity && refillPerSec > 0 {
		needed := float64(capacity) - b.tokens
		resetSec = int(math.Ceil(needed / float64(refillPerSec)))
	}

	return &Result{
		Allowed:   allowed,
		Limit:     capacity,
		Remaining: remaining,
		ResetSec:  resetSec,
	}, nil
}

// RedisLimiter provides a Redis-backed rate limiter using token buckets with automatic in-memory fallback.
type RedisLimiter struct {
	client  *redis.Client
	fallback *MemoryLimiter
}

// NewRedisLimiter creates a new RedisLimiter. If client is nil, it operates entirely in memory.
func NewRedisLimiter(client *redis.Client) *RedisLimiter {
	return &RedisLimiter{
		client:   client,
		fallback: NewMemoryLimiter(),
	}
}

// Lua script for atomic token bucket in Redis:
// KEYS[1] = bucket key
// ARGV[1] = capacity
// ARGV[2] = refill rate per second
// ARGV[3] = current unix timestamp (seconds)
const tokenBucketScript = `
local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local refill_rate = tonumber(ARGV[2])
local now = tonumber(ARGV[3])

local data = redis.call('HMGET', key, 'tokens', 'last_refill')
local tokens = tonumber(data[1])
local last_refill = tonumber(data[2])

if not tokens then
    tokens = capacity
    last_refill = now
else
    local elapsed = math.max(0, now - last_refill)
    tokens = math.min(capacity, tokens + (elapsed * refill_rate))
    last_refill = now
end

local allowed = 0
if tokens >= 1 then
    tokens = tokens - 1
    allowed = 1
end

redis.call('HMSET', key, 'tokens', tokens, 'last_refill', last_refill)
redis.call('EXPIRE', key, math.ceil(capacity / math.max(1, refill_rate)) + 60)

return {allowed, math.floor(tokens)}
`

// Allow evaluates a token bucket check in Redis (or falls back to memory if Redis is unavailable).
func (r *RedisLimiter) Allow(ctx context.Context, key string, capacity int, refillPerSec int) (*Result, error) {
	if r.client == nil {
		return r.fallback.Allow(ctx, key, capacity, refillPerSec)
	}

	redisKey := fmt.Sprintf("minipay:ratelimit:%s", key)
	now := time.Now().Unix()

	res, err := r.client.Eval(ctx, tokenBucketScript, []string{redisKey}, capacity, refillPerSec, now).Result()
	if err != nil {
		// Redis error, fallback gracefully to in-memory limiter to maintain service availability
		return r.fallback.Allow(ctx, key, capacity, refillPerSec)
	}

	vals, ok := res.([]any)
	if !ok || len(vals) < 2 {
		return r.fallback.Allow(ctx, key, capacity, refillPerSec)
	}

	allowedInt, _ := vals[0].(int64)
	remainingInt, _ := vals[1].(int64)

	allowed := allowedInt == 1
	remaining := int(remainingInt)
	resetSec := 0
	if remaining < capacity && refillPerSec > 0 {
		resetSec = (capacity - remaining) / refillPerSec
		if resetSec <= 0 {
			resetSec = 1
		}
	}

	return &Result{
		Allowed:   allowed,
		Limit:     capacity,
		Remaining: remaining,
		ResetSec:  resetSec,
	}, nil
}

// Middleware creates an HTTP handler that enforces rate limits.
func Middleware(limiter Limiter, capacity, refillPerSec int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract rate limit key (prefer Authorization token/merchant ID, fallback to RemoteAddr)
			key := r.Header.Get("Authorization")
			if key == "" {
				key = r.RemoteAddr
			}

			res, err := limiter.Allow(r.Context(), key, capacity, refillPerSec)
			if err != nil || res == nil {
				// On internal rate limit evaluation error, fail open to avoid service outage
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(res.Limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
			w.Header().Set("X-RateLimit-Reset", strconv.Itoa(res.ResetSec))

			if !res.Allowed {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{
						"code":    "rate_limit_exceeded",
						"message": "Too many requests. Please slow down and try again later.",
					},
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

