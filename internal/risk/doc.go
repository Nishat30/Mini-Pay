// Package risk implements payment risk evaluation rules. Each rule
// produces an allow, review, or block decision. The worst outcome wins.
// Rules are feature-flagged via the RISK_RULES environment variable.
package risk

