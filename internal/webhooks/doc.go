// Package webhooks implements webhook signing, dispatching with exponential
// backoff, dead-letter handling, and SSRF protection. It owns the guarantee
// that webhook deliveries are signed, retried, and eventually marked dead
// if the endpoint never responds successfully.
package webhooks

