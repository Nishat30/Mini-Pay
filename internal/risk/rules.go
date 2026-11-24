// Package risk provides fraud detection, amount threshold verification, and velocity rules.
package risk

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/minipay/minipay/internal/platform/db"
)

// Outcomes returned by risk evaluation.
const (
	OutcomeAllow  = "allow"
	OutcomeReview = "review"
	OutcomeBlock  = "block"
)

// Context contains information about the payment request to be evaluated.
type Context struct {
	MerchantID string
	PaymentID  string
	Amount     int64
	Currency   string
	Metadata   map[string]any
}

// Decision represents the result of a single rule or aggregate evaluation.
type Decision struct {
	Rule    string
	Outcome string // allow, review, block
	Detail  map[string]any
}

// Rule defines an evaluation rule.
type Rule interface {
	Name() string
	Evaluate(ctx context.Context, req Context) (Decision, error)
}

// AmountCapRule checks if the payment amount exceeds configured thresholds.
type AmountCapRule struct {
	ReviewThreshold int64 // Amounts above this require review (e.g. 50000 = $500.00)
	BlockThreshold  int64 // Amounts above this are blocked (e.g. 500000 = $5000.00)
}

// NewAmountCapRule creates an amount cap rule.
func NewAmountCapRule(reviewThreshold, blockThreshold int64) *AmountCapRule {
	if reviewThreshold <= 0 {
		reviewThreshold = 50000 // $500 default
	}
	if blockThreshold <= 0 {
		blockThreshold = 500000 // $5000 default
	}
	return &AmountCapRule{
		ReviewThreshold: reviewThreshold,
		BlockThreshold:  blockThreshold,
	}
}

func (r *AmountCapRule) Name() string {
	return "amount_cap"
}

func (r *AmountCapRule) Evaluate(ctx context.Context, req Context) (Decision, error) {
	if req.Amount >= r.BlockThreshold {
		return Decision{
			Rule:    r.Name(),
			Outcome: OutcomeBlock,
			Detail: map[string]any{
				"amount":          req.Amount,
				"block_threshold": r.BlockThreshold,
				"reason":          "amount exceeds maximum allowable threshold",
			},
		}, nil
	}

	if req.Amount >= r.ReviewThreshold {
		return Decision{
			Rule:    r.Name(),
			Outcome: OutcomeReview,
			Detail: map[string]any{
				"amount":           req.Amount,
				"review_threshold": r.ReviewThreshold,
				"reason":           "amount requires administrative review",
			},
		}, nil
	}

	return Decision{
		Rule:    r.Name(),
		Outcome: OutcomeAllow,
		Detail:  map[string]any{"amount": req.Amount},
	}, nil
}

// VelocityRule tracks request velocity within a rolling window.
type VelocityRule struct {
	mu          sync.Mutex
	history     map[string][]time.Time
	maxRequests int
	window      time.Duration
}

// NewVelocityRule creates a velocity rule.
func NewVelocityRule(maxRequests int, window time.Duration) *VelocityRule {
	if maxRequests <= 0 {
		maxRequests = 20
	}
	if window <= 0 {
		window = 1 * time.Minute
	}
	return &VelocityRule{
		history:     make(map[string][]time.Time),
		maxRequests: maxRequests,
		window:      window,
	}
}

func (r *VelocityRule) Name() string {
	return "velocity"
}

func (r *VelocityRule) Evaluate(ctx context.Context, req Context) (Decision, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-r.window)

	timestamps := r.history[req.MerchantID]
	valid := make([]time.Time, 0, len(timestamps))
	for _, t := range timestamps {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= r.maxRequests {
		r.history[req.MerchantID] = valid
		return Decision{
			Rule:    r.Name(),
			Outcome: OutcomeBlock,
			Detail: map[string]any{
				"count":        len(valid),
				"max_requests": r.maxRequests,
				"window":       r.window.String(),
				"reason":       "velocity threshold exceeded",
			},
		}, nil
	}

	valid = append(valid, now)
	r.history[req.MerchantID] = valid

	return Decision{
		Rule:    r.Name(),
		Outcome: OutcomeAllow,
		Detail:  map[string]any{"count": len(valid)},
	}, nil
}

// Engine evaluates incoming requests against registered risk rules.
type Engine struct {
	rules []Rule
}

// NewEngine creates a risk engine with configured rules.
func NewEngine(rules ...Rule) *Engine {
	return &Engine{rules: rules}
}

// Evaluate evaluates all rules and returns aggregate decision and individual decisions.
func (e *Engine) Evaluate(ctx context.Context, req Context) (Decision, []Decision, error) {
	aggregate := Decision{
		Rule:    "aggregate",
		Outcome: OutcomeAllow,
		Detail:  make(map[string]any),
	}

	decisions := make([]Decision, 0, len(e.rules))

	for _, rule := range e.rules {
		dec, err := rule.Evaluate(ctx, req)
		if err != nil {
			return aggregate, nil, fmt.Errorf("rule %s failed: %w", rule.Name(), err)
		}
		decisions = append(decisions, dec)

		if dec.Outcome == OutcomeBlock {
			aggregate.Outcome = OutcomeBlock
			aggregate.Detail["blocking_rule"] = rule.Name()
			aggregate.Detail[rule.Name()] = dec.Detail
		} else if dec.Outcome == OutcomeReview && aggregate.Outcome != OutcomeBlock {
			aggregate.Outcome = OutcomeReview
			aggregate.Detail["review_rule"] = rule.Name()
			aggregate.Detail[rule.Name()] = dec.Detail
		}
	}

	return aggregate, decisions, nil
}

// RecordDecisions stores risk decisions in the database.
func RecordDecisions(ctx context.Context, queries *db.Queries, paymentID string, decisions []Decision) error {
	for _, d := range decisions {
		detailJSON, err := json.Marshal(d.Detail)
		if err != nil {
			detailJSON = []byte("{}")
		}

		_, err = queries.InsertRiskDecision(ctx, db.InsertRiskDecisionParams{
			PaymentID: paymentID,
			Rule:      d.Rule,
			Outcome:   d.Outcome,
			Detail:    detailJSON,
		})
		if err != nil {
			return fmt.Errorf("failed to record risk decision for rule %s: %w", d.Rule, err)
		}
	}
	return nil
}
