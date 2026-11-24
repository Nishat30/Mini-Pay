// Package idempotency implements single-transaction request deduplication and response replay.
package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/minipay/minipay/internal/metrics"
	"github.com/minipay/minipay/internal/platform/db"
)

var (
	ErrKeyReusedDifferentPayload = errors.New("idempotency key was previously used with a different request payload")
	ErrConcurrentRequest         = errors.New("a concurrent request with this idempotency key is already in progress")
	ErrKeyExpired                = errors.New("idempotency key has expired")
	ErrInvalidKey                = errors.New("idempotency key must be between 1 and 255 characters")
)

// Record holds the result of inspecting an idempotency key.
type Record struct {
	Replayed       bool
	ResponseStatus int
	ResponseBody   []byte
}

// ComputeHash calculates SHA-256(method + "\n" + path + "\n" + body).
func ComputeHash(method, path string, body []byte) []byte {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte("\n"))
	h.Write([]byte(path))
	h.Write([]byte("\n"))
	h.Write(body)
	return h.Sum(nil)
}

// ValidateKey checks that the key length is within acceptable bounds.
func ValidateKey(key string) error {
	if len(key) < 1 || len(key) > 255 {
		return ErrInvalidKey
	}
	return nil
}

// CheckOrLock inspects the idempotency key within a transaction.
// If the key has already completed successfully with the same hash, it returns the cached response (Replayed: true)
// and atomically increments the replay counter.
// If the key was used with a different hash, it returns ErrKeyReusedDifferentPayload and records a conflict.
// If the key is currently locked by a live in-flight request, it returns ErrConcurrentRequest.
// Otherwise, it locks the key for processing in this transaction.
func CheckOrLock(ctx context.Context, txQueries *db.Queries, merchantID, key string, requestHash []byte, endpoint ...string) (*Record, error) {
	existing, err := txQueries.GetIdempotencyKey(ctx, db.GetIdempotencyKeyParams{
		MerchantID: merchantID,
		Key:        key,
	})
	if err == nil {
		// Key exists
		now := time.Now()
		expiresAt := existing.ExpiresAt.Time
		if now.After(expiresAt) {
			// Key expired, overwrite it
		} else {
			// Check if payload matches
			if !bytes.Equal(existing.RequestHash, requestHash) {
				_ = txQueries.IncrementIdempotencyConflict(ctx, db.IncrementIdempotencyConflictParams{
					MerchantID: merchantID,
					Key:        key,
				})
				metrics.RecordIdempotencyConflict()
				return nil, ErrKeyReusedDifferentPayload
			}

			if existing.State == "completed" {
				_ = txQueries.IncrementIdempotencyReplay(ctx, db.IncrementIdempotencyReplayParams{
					MerchantID: merchantID,
					Key:        key,
				})
				metrics.RecordIdempotencyReplay()
				return &Record{
					Replayed:       true,
					ResponseStatus: int(existing.ResponseStatus),
					ResponseBody:   existing.ResponseBody,
				}, nil
			}

			// In progress
			if existing.State == "started" {
				// If locked recently (within 60 seconds), it's concurrent
				if existing.LockedAt.Valid && now.Sub(existing.LockedAt.Time) < 60*time.Second {
					return nil, ErrConcurrentRequest
				}
			}
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("failed to query idempotency key: %w", err)
	}

	epText := pgtype.Text{Valid: false}
	if len(endpoint) > 0 && endpoint[0] != "" {
		epText = pgtype.Text{String: endpoint[0], Valid: true}
	}

	// Insert or refresh key in 'started' state
	_, err = txQueries.InsertIdempotencyKey(ctx, db.InsertIdempotencyKeyParams{
		MerchantID:  merchantID,
		Key:         key,
		RequestHash: requestHash,
		Endpoint:    epText,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// A concurrent transaction completed immediately before us. Re-check state.
			rechecked, recheckErr := txQueries.GetIdempotencyKey(ctx, db.GetIdempotencyKeyParams{
				MerchantID: merchantID,
				Key:        key,
			})
			if recheckErr == nil {
				if !bytes.Equal(rechecked.RequestHash, requestHash) {
					_ = txQueries.IncrementIdempotencyConflict(ctx, db.IncrementIdempotencyConflictParams{
						MerchantID: merchantID,
						Key:        key,
					})
					metrics.RecordIdempotencyConflict()
					return nil, ErrKeyReusedDifferentPayload
				}
				if rechecked.State == "completed" {
					_ = txQueries.IncrementIdempotencyReplay(ctx, db.IncrementIdempotencyReplayParams{
						MerchantID: merchantID,
						Key:        key,
					})
					metrics.RecordIdempotencyReplay()
					return &Record{
						Replayed:       true,
						ResponseStatus: int(rechecked.ResponseStatus),
						ResponseBody:   rechecked.ResponseBody,
					}, nil
				}
			}
			return nil, ErrConcurrentRequest
		}
		return nil, fmt.Errorf("failed to insert idempotency key: %w", err)
	}

	return &Record{Replayed: false}, nil
}

// SaveResponse marks the idempotency key completed and stores the response status & body.
func SaveResponse(ctx context.Context, txQueries *db.Queries, merchantID, key string, status int, body []byte) error {
	return txQueries.UpdateIdempotencyKeyCompleted(ctx, db.UpdateIdempotencyKeyCompletedParams{
		MerchantID:     merchantID,
		Key:            key,
		ResponseStatus: int32(status),
		ResponseBody:   body,
	})
}
