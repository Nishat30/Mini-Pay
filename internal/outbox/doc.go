// Package outbox implements the transactional outbox pattern: claim with
// lease, relay to processors, and delivery tracking. It owns the guarantee
// that every committed outbox event is eventually processed at least once.
package outbox

