// Package payments implements the payment domain logic and lifecycle state machine for MiniPay.
package payments

import (
	"errors"
	"fmt"
)

// PaymentStatus represents the lifecycle state of a payment.
type PaymentStatus string

const (
	StatusProcessing     PaymentStatus = "processing"
	StatusRequiresReview PaymentStatus = "requires_review"
	StatusSucceeded      PaymentStatus = "succeeded"
	StatusFailed         PaymentStatus = "failed"
	StatusRefunded       PaymentStatus = "refunded"
)

// PaymentEvent represents an event triggering a state transition.
type PaymentEvent string

const (
	EventProcess       PaymentEvent = "process"
	EventRequireReview PaymentEvent = "require_review"
	EventSucceed       PaymentEvent = "succeed"
	EventFail          PaymentEvent = "fail"
	EventRefund        PaymentEvent = "refund"
)

var (
	// ErrInvalidTransition is returned when a requested transition is not permitted by the state machine.
	ErrInvalidTransition = errors.New("invalid payment state transition")
)

// Transition computes the next PaymentStatus given the current status and an incoming event.
// It returns an error if the transition is illegal or if attempting to transition from a terminal state.
func Transition(from PaymentStatus, event PaymentEvent) (PaymentStatus, error) {
	switch from {
	case "":
		// Initial creation
		if event == EventProcess {
			return StatusProcessing, nil
		}
	case StatusProcessing:
		switch event {
		case EventSucceed:
			return StatusSucceeded, nil
		case EventFail:
			return StatusFailed, nil
		case EventRequireReview:
			return StatusRequiresReview, nil
		}
	case StatusRequiresReview:
		switch event {
		case EventSucceed:
			return StatusSucceeded, nil
		case EventFail:
			return StatusFailed, nil
		}
	case StatusSucceeded:
		switch event {
		case EventRefund:
			return StatusRefunded, nil
		}
	case StatusFailed, StatusRefunded:
		return from, fmt.Errorf("%w: cannot transition from terminal state %q with event %q", ErrInvalidTransition, from, event)
	}

	return from, fmt.Errorf("%w: no transition from %q on event %q", ErrInvalidTransition, from, event)
}

// IsTerminal returns true if the status is a terminal state that accepts no further transitions.
func IsTerminal(status PaymentStatus) bool {
	return status == StatusFailed || status == StatusRefunded
}
