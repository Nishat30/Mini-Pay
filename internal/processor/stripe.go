package processor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var (
	ErrLiveKeyRejected = errors.New("live Stripe API keys (sk_live_) are strictly forbidden in this environment")
)

// StripeAdapter communicates with the Stripe API using test mode credentials.
type StripeAdapter struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewStripeAdapter creates a new Stripe payment processor adapter.
func NewStripeAdapter(apiKey string, client *http.Client) (*StripeAdapter, error) {
	if strings.HasPrefix(apiKey, "sk_live_") {
		return nil, ErrLiveKeyRejected
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &StripeAdapter{
		apiKey:  apiKey,
		baseURL: "https://api.stripe.com/v1",
		client:  client,
	}, nil
}

// StripeErrorResponse represents an error response from the Stripe API.
type StripeErrorResponse struct {
	Error struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// StripeChargeResponse represents a successful charge response from Stripe.
type StripeChargeResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Paid   bool   `json:"paid"`
}

// SetBaseURL overrides the default Stripe API URL (used for local testing and mocking).
func (s *StripeAdapter) SetBaseURL(url string) {
	s.baseURL = url
}

// Charge submits an authorization request to Stripe's charges API.
func (s *StripeAdapter) Charge(ctx context.Context, req ChargeRequest) (*ChargeResult, error) {
	endpoint := fmt.Sprintf("%s/charges", s.baseURL)

	data := url.Values{}
	data.Set("amount", strconv.FormatInt(req.Amount, 10))
	data.Set("currency", strings.ToLower(req.Currency))
	data.Set("source", "tok_visa") // Test token
	data.Set("description", fmt.Sprintf("MiniPay charge for %s", req.PaymentID))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create Stripe request: %w", err)
	}

	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", s.apiKey))
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("Idempotency-Key", req.PaymentID)

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return &ChargeResult{
			Success:   false,
			Retryable: true,
		}, fmt.Errorf("network error contacting Stripe: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &ChargeResult{Success: false, Retryable: true}, err
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var charge StripeChargeResponse
		if err := json.Unmarshal(body, &charge); err != nil {
			return nil, fmt.Errorf("failed to parse Stripe response: %w", err)
		}
		return &ChargeResult{
			Success:      true,
			ProcessorRef: charge.ID,
		}, nil
	}

	var stripeErr StripeErrorResponse
	_ = json.Unmarshal(body, &stripeErr)

	code := stripeErr.Error.Code
	if code == "" {
		code = "card_declined"
	}

	retryable := resp.StatusCode >= 500
	return &ChargeResult{
		Success:     false,
		FailureCode: code,
		Retryable:   retryable,
	}, nil
}
