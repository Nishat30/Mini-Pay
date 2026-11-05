// Package payments implements the payment service, state machine, and
// repository. It owns payment creation, status transitions, and the
// payment event log. It must never import the api package or know about HTTP.
package payments

