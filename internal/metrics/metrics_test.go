package metrics_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/minipay/minipay/internal/metrics"
)

func TestMetricsRegistry(t *testing.T) {
	reg := metrics.NewRegistry()

	// 1. Counter
	reg.CounterInc("test_counter", "Test counter help", map[string]string{"foo": "bar"})
	reg.CounterAdd("test_counter", "Test counter help", map[string]string{"foo": "bar"}, 4)

	// 2. Gauge
	reg.GaugeSet("test_gauge", "Test gauge help", map[string]string{"env": "test"}, 42)

	// 3. Histogram
	reg.HistogramObserve("test_latency", "Test latency help", []float64{0.1, 0.5, 1.0}, map[string]string{"route": "/pay"}, 0.25)

	var buf bytes.Buffer
	reg.WritePrometheus(&buf)
	out := buf.String()

	if !strings.Contains(out, `# TYPE test_counter counter`) {
		t.Errorf("missing counter type header in: %s", out)
	}
	if !strings.Contains(out, `test_counter{foo="bar"} 5`) {
		t.Errorf("missing counter value 5 in: %s", out)
	}
	if !strings.Contains(out, `test_gauge{env="test"} 42`) {
		t.Errorf("missing gauge value 42 in: %s", out)
	}
	if !strings.Contains(out, `test_latency_bucket{route="/pay",le="0.5"} 1`) {
		t.Errorf("missing histogram bucket observation in: %s", out)
	}
}

func TestConvenienceAndHandler(t *testing.T) {
	metrics.RecordHTTPRequest("POST", "/v1/payments", http.StatusCreated, 15*time.Millisecond)
	metrics.RecordPayment("succeeded", "usd")
	metrics.SetLedgerViolations(0)

	req := httptest.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()
	metrics.DefaultRegistry.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, `http_requests_total{method="POST",path="/v1/payments",status="201"} 1`) {
		t.Errorf("expected http_requests_total in output: %s", body)
	}
	if !strings.Contains(body, `payments_total{currency="usd",status="succeeded"} 1`) {
		t.Errorf("expected payments_total in output: %s", body)
	}
	if !strings.Contains(body, `ledger_invariants_violations 0`) {
		t.Errorf("expected ledger_invariants_violations in output: %s", body)
	}
}

