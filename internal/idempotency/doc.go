// Package idempotency implements the idempotency key store and replay logic.
// It owns the guarantee that a request with the same key and body produces
// the same response, and that concurrent duplicates are serialised.
// It must never contain business logic beyond key management.
package idempotency

