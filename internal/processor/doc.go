// Package processor defines the Processor interface and provides two
// implementations: a simulator for testing and a Stripe adapter for
// real payment processing in test mode. It must never access the database
// directly — the worker owns the transaction boundary.
package processor

