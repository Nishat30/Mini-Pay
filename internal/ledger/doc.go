// Package ledger implements double-entry accounting: posting rules,
// entry generation, and invariant checking. It owns the guarantee that
// every ledger transaction balances and that entries are append-only.
// It must never import the api package or know about HTTP.
package ledger

