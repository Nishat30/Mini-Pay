package ratelimit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/minipay/minipay/internal/ratelimit"
)

func TestMemoryLimiter_BurstAndThrottle(t *testing.T) {
	ctx := context.Background()
	limiter := ratelimit.NewMemoryLimiter()

	// Capacity: 3 requests, Refill: 1 per second
	for i := 0; i < 3; i++ {
		res, err := limiter.Allow(ctx, "test_client", 3, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Allowed {
			t.Errorf("expected request %d to be allowed", i+1)
		}
	}

	// 4th request must be throttled
	res, err := limiter.Allow(ctx, "test_client", 3, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Allowed {
		t.Errorf("expected 4th request to be throttled")
	}
	if res.Remaining != 0 {
		t.Errorf("expected remaining to be 0, got %d", res.Remaining)
	}
}

func TestRateLimitMiddleware_HeadersAnd429(t *testing.T) {
	limiter := ratelimit.NewMemoryLimiter()
	mw := ratelimit.Middleware(limiter, 2, 1)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	// Request 1: 200
	req1 := httptest.NewRequest("GET", "/test", nil)
	req1.Header.Set("Authorization", "Bearer test_token")
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w1.Code)
	}
	if w1.Header().Get("X-RateLimit-Limit") != "2" {
		t.Errorf("missing or invalid X-RateLimit-Limit header")
	}

	// Request 2: 200
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.Header.Set("Authorization", "Bearer test_token")
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w2.Code)
	}

	// Request 3: 429 Too Many Requests
	req3 := httptest.NewRequest("GET", "/test", nil)
	req3.Header.Set("Authorization", "Bearer test_token")
	w3 := httptest.NewRecorder()
	handler.ServeHTTP(w3, req3)
	if w3.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 Too Many Requests, got %d", w3.Code)
	}
}

