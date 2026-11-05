// Package api implements the HTTP API layer for MiniPay.
// It owns routing, middleware, request/response encoding, and HTTP-level
// concerns. It delegates all business logic to service packages.
// It must never access the database directly — use service interfaces.
package api
