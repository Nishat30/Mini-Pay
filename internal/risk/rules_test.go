package risk_test

import (
	"context"
	"testing"
	"time"

	"github.com/minipay/minipay/internal/risk"
)

func TestAmountCapRule(t *testing.T) {
	rule := risk.NewAmountCapRule(50000, 500000) // $500 review, $5000 block
	ctx := context.Background()

	// 1. Below review threshold -> allow
	dec, err := rule.Evaluate(ctx, risk.Context{Amount: 10000})
	if err != nil || dec.Outcome != risk.OutcomeAllow {
		t.Fatalf("expected allow for $100, got %s (err: %v)", dec.Outcome, err)
	}

	// 2. Above review threshold, below block threshold -> review
	dec, err = rule.Evaluate(ctx, risk.Context{Amount: 60000})
	if err != nil || dec.Outcome != risk.OutcomeReview {
		t.Fatalf("expected review for $600, got %s (err: %v)", dec.Outcome, err)
	}

	// 3. Above block threshold -> block
	dec, err = rule.Evaluate(ctx, risk.Context{Amount: 600000})
	if err != nil || dec.Outcome != risk.OutcomeBlock {
		t.Fatalf("expected block for $6000, got %s (err: %v)", dec.Outcome, err)
	}
}

func TestVelocityRule(t *testing.T) {
	rule := risk.NewVelocityRule(3, 1*time.Minute)
	ctx := context.Background()
	req := risk.Context{MerchantID: "mer_test_velocity"}

	// 1st request -> allow
	dec, err := rule.Evaluate(ctx, req)
	if err != nil || dec.Outcome != risk.OutcomeAllow {
		t.Fatalf("req 1: expected allow, got %s", dec.Outcome)
	}

	// 2nd request -> allow
	dec, err = rule.Evaluate(ctx, req)
	if err != nil || dec.Outcome != risk.OutcomeAllow {
		t.Fatalf("req 2: expected allow, got %s", dec.Outcome)
	}

	// 3rd request -> allow (reaches limit)
	dec, err = rule.Evaluate(ctx, req)
	if err != nil || dec.Outcome != risk.OutcomeAllow {
		t.Fatalf("req 3: expected allow, got %s", dec.Outcome)
	}

	// 4th request -> block
	dec, err = rule.Evaluate(ctx, req)
	if err != nil || dec.Outcome != risk.OutcomeBlock {
		t.Fatalf("req 4: expected block, got %s", dec.Outcome)
	}
}

func TestEngineAggregate(t *testing.T) {
	amountCap := risk.NewAmountCapRule(50000, 500000)
	velocity := risk.NewVelocityRule(10, 1*time.Minute)
	engine := risk.NewEngine(amountCap, velocity)

	ctx := context.Background()

	// Normal amount -> allow
	agg, decs, err := engine.Evaluate(ctx, risk.Context{MerchantID: "m1", Amount: 2000})
	if err != nil || agg.Outcome != risk.OutcomeAllow || len(decs) != 2 {
		t.Fatalf("expected aggregate allow, got %s (err: %v)", agg.Outcome, err)
	}

	// Large amount -> review
	agg, decs, err = engine.Evaluate(ctx, risk.Context{MerchantID: "m1", Amount: 75000})
	if err != nil || agg.Outcome != risk.OutcomeReview || len(decs) != 2 {
		t.Fatalf("expected aggregate review, got %s", agg.Outcome)
	}

	// Enormous amount -> block
	agg, decs, err = engine.Evaluate(ctx, risk.Context{MerchantID: "m1", Amount: 1000000})
	if err != nil || agg.Outcome != risk.OutcomeBlock || len(decs) != 2 {
		t.Fatalf("expected aggregate block, got %s", agg.Outcome)
	}
}

