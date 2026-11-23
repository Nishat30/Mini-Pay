// Package processor defines interfaces and simulators for payment service providers (PSP).
package processor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrNetworkTimeout   = errors.New("transient network timeout from payment processor")
	ErrCardDeclined     = errors.New("card declined by issuer")
	ErrInsufficientFund = errors.New("insufficient funds")
)

// ChargeRequest represents a charge attempt to a processor.
type ChargeRequest struct {
	PaymentID string
	Amount    int64
	Currency  string
	Simulate  string // "success", "decline", "insufficient_funds", "network_error"
}

// ChargeResult represents the processor's response.
type ChargeResult struct {
	Success      bool
	ProcessorRef string
	FailureCode  string
	Retryable    bool
}

// Processor defines the interface for charging customer payment methods.
type Processor interface {
	Charge(ctx context.Context, req ChargeRequest) (*ChargeResult, error)
}

// Simulator implements an in-memory PSP simulator with deterministic failure modes.
type Simulator struct{}

// NewSimulator creates a new PSP simulator.
func NewSimulator() *Simulator {
	return &Simulator{}
}

// Charge executes a simulated payment authorization.
func (s *Simulator) Charge(ctx context.Context, req ChargeRequest) (*ChargeResult, error) {
	mode := strings.ToLower(strings.TrimSpace(req.Simulate))

	switch mode {
	case "network_error":
		return &ChargeResult{
			Success:   false,
			Retryable: true,
		}, ErrNetworkTimeout

	case "decline":
		return &ChargeResult{
			Success:     false,
			FailureCode: "card_declined",
			Retryable:   false,
		}, nil

	case "insufficient_funds":
		return &ChargeResult{
			Success:     false,
			FailureCode: "insufficient_funds",
			Retryable:   false,
		}, nil

	default: // "success" or unset
		refBytes := make([]byte, 12)
		_, _ = rand.Read(refBytes)
		return &ChargeResult{
			Success:      true,
			ProcessorRef: fmt.Sprintf("sim_ch_%s", hex.EncodeToString(refBytes)),
			Retryable:    false,
		}, nil
	}
}
