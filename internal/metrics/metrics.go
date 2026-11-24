// Package metrics provides a lightweight, thread-safe Prometheus exposition format collector
// for HTTP traffic, payment lifecycle events, and ledger invariants.
package metrics

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MetricType denotes Prometheus metric types.
type MetricType string

const (
	TypeCounter   MetricType = "counter"
	TypeGauge     MetricType = "gauge"
	TypeHistogram MetricType = "histogram"
)

// Registry manages and formats Prometheus metrics.
type Registry struct {
	mu           sync.RWMutex
	counters     map[string]*counterMetric
	gauges       map[string]*gaugeMetric
	histograms   map[string]*histogramMetric
}

type counterMetric struct {
	help   string
	values map[string]*atomic.Uint64
}

type gaugeMetric struct {
	help   string
	values map[string]*atomic.Int64
}

type histogramMetric struct {
	help    string
	buckets []float64
	counts  map[string][]uint64
	sums    map[string]*float64
	totals  map[string]uint64
}

// NewRegistry creates an empty metrics registry.
func NewRegistry() *Registry {
	return &Registry{
		counters:   make(map[string]*counterMetric),
		gauges:     make(map[string]*gaugeMetric),
		histograms: make(map[string]*histogramMetric),
	}
}

// DefaultRegistry is the global application registry.
var DefaultRegistry = NewRegistry()

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(fmt.Sprintf(`%s="%s"`, k, labels[k]))
	}
	b.WriteString("}")
	return b.String()
}

// CounterInc increments a counter by 1.
func (r *Registry) CounterInc(name, help string, labels map[string]string) {
	r.CounterAdd(name, help, labels, 1)
}

// CounterAdd increments a counter by delta.
func (r *Registry) CounterAdd(name, help string, labels map[string]string, delta uint64) {
	lblStr := formatLabels(labels)
	r.mu.Lock()
	c, ok := r.counters[name]
	if !ok {
		c = &counterMetric{help: help, values: make(map[string]*atomic.Uint64)}
		r.counters[name] = c
	}
	val, ok := c.values[lblStr]
	if !ok {
		val = &atomic.Uint64{}
		c.values[lblStr] = val
	}
	r.mu.Unlock()

	val.Add(delta)
}

// GaugeSet sets a gauge to a specific value.
func (r *Registry) GaugeSet(name, help string, labels map[string]string, value int64) {
	lblStr := formatLabels(labels)
	r.mu.Lock()
	g, ok := r.gauges[name]
	if !ok {
		g = &gaugeMetric{help: help, values: make(map[string]*atomic.Int64)}
		r.gauges[name] = g
	}
	val, ok := g.values[lblStr]
	if !ok {
		val = &atomic.Int64{}
		g.values[lblStr] = val
	}
	r.mu.Unlock()

	val.Store(value)
}

// HistogramObserve records an observation in a histogram.
func (r *Registry) HistogramObserve(name, help string, buckets []float64, labels map[string]string, value float64) {
	lblStr := formatLabels(labels)
	r.mu.Lock()
	defer r.mu.Unlock()

	h, ok := r.histograms[name]
	if !ok {
		if len(buckets) == 0 {
			buckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
		}
		sort.Float64s(buckets)
		h = &histogramMetric{
			help:    help,
			buckets: buckets,
			counts:  make(map[string][]uint64),
			sums:    make(map[string]*float64),
			totals:  make(map[string]uint64),
		}
		r.histograms[name] = h
	}

	counts, ok := h.counts[lblStr]
	if !ok {
		counts = make([]uint64, len(h.buckets))
		h.counts[lblStr] = counts
		zero := 0.0
		h.sums[lblStr] = &zero
	}

	for i, b := range h.buckets {
		if value <= b {
			counts[i]++
		}
	}
	*h.sums[lblStr] += value
	h.totals[lblStr]++
}

// WritePrometheus writes the registered metrics in Prometheus exposition format.
func (r *Registry) WritePrometheus(w io.Writer) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Counters
	counterNames := make([]string, 0, len(r.counters))
	for name := range r.counters {
		counterNames = append(counterNames, name)
	}
	sort.Strings(counterNames)

	for _, name := range counterNames {
		c := r.counters[name]
		fmt.Fprintf(w, "# HELP %s %s\n", name, c.help)
		fmt.Fprintf(w, "# TYPE %s counter\n", name)

		labelKeys := make([]string, 0, len(c.values))
		for l := range c.values {
			labelKeys = append(labelKeys, l)
		}
		sort.Strings(labelKeys)

		for _, l := range labelKeys {
			fmt.Fprintf(w, "%s%s %d\n", name, l, c.values[l].Load())
		}
	}

	// Gauges
	gaugeNames := make([]string, 0, len(r.gauges))
	for name := range r.gauges {
		gaugeNames = append(gaugeNames, name)
	}
	sort.Strings(gaugeNames)

	for _, name := range gaugeNames {
		g := r.gauges[name]
		fmt.Fprintf(w, "# HELP %s %s\n", name, g.help)
		fmt.Fprintf(w, "# TYPE %s gauge\n", name)

		labelKeys := make([]string, 0, len(g.values))
		for l := range g.values {
			labelKeys = append(labelKeys, l)
		}
		sort.Strings(labelKeys)

		for _, l := range labelKeys {
			fmt.Fprintf(w, "%s%s %d\n", name, l, g.values[l].Load())
		}
	}

	// Histograms
	histNames := make([]string, 0, len(r.histograms))
	for name := range r.histograms {
		histNames = append(histNames, name)
	}
	sort.Strings(histNames)

	for _, name := range histNames {
		h := r.histograms[name]
		fmt.Fprintf(w, "# HELP %s %s\n", name, h.help)
		fmt.Fprintf(w, "# TYPE %s histogram\n", name)

		labelKeys := make([]string, 0, len(h.counts))
		for l := range h.counts {
			labelKeys = append(labelKeys, l)
		}
		sort.Strings(labelKeys)

		for _, l := range labelKeys {
			counts := h.counts[l]
			prefix := strings.TrimSuffix(l, "}")
			hasExisting := len(prefix) > 1

			for i, b := range h.buckets {
				leStr := fmt.Sprintf(`le="%v"`, b)
				if hasExisting {
					fmt.Fprintf(w, "%s_bucket{%s,%s} %d\n", name, prefix[1:], leStr, counts[i])
				} else {
					fmt.Fprintf(w, "%s_bucket{%s} %d\n", name, leStr, counts[i])
				}
			}

			infStr := `le="+Inf"`
			if hasExisting {
				fmt.Fprintf(w, "%s_bucket{%s,%s} %d\n", name, prefix[1:], infStr, h.totals[l])
			} else {
				fmt.Fprintf(w, "%s_bucket{%s} %d\n", name, infStr, h.totals[l])
			}

			fmt.Fprintf(w, "%s_sum%s %.6f\n", name, l, *h.sums[l])
			fmt.Fprintf(w, "%s_count%s %d\n", name, l, h.totals[l])
		}
	}
}

// Handler returns an HTTP handler func that writes metrics.
func (r *Registry) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		r.WritePrometheus(w)
	}
}

// Convenience application metrics
const (
	MetricHTTPRequestsTotal    = "http_requests_total"
	MetricHTTPRequestDuration  = "http_request_duration_seconds"
	MetricPaymentsTotal        = "payments_total"
	MetricLedgerViolations     = "ledger_invariants_violations"
	MetricIdempotencyReplays   = "idempotency_replays_total"
	MetricIdempotencyConflicts = "idempotency_conflicts_total"
)

// RecordIdempotencyReplay increments the idempotency replays counter.
func RecordIdempotencyReplay() {
	DefaultRegistry.CounterInc(MetricIdempotencyReplays, "Total count of idempotent responses replayed.", nil)
}

// RecordIdempotencyConflict increments the idempotency conflicts counter.
func RecordIdempotencyConflict() {
	DefaultRegistry.CounterInc(MetricIdempotencyConflicts, "Total count of idempotency key conflict rejections.", nil)
}

// InitIdempotencyMetrics initializes the counters to 0.
func InitIdempotencyMetrics() {
	DefaultRegistry.CounterAdd(MetricIdempotencyReplays, "Total count of idempotent responses replayed.", nil, 0)
	DefaultRegistry.CounterAdd(MetricIdempotencyConflicts, "Total count of idempotency key conflict rejections.", nil, 0)
}

// RecordHTTPRequest records an incoming HTTP request.
func RecordHTTPRequest(method, path string, status int, duration time.Duration) {
	labels := map[string]string{
		"method": method,
		"path":   path,
		"status": fmt.Sprintf("%d", status),
	}
	DefaultRegistry.CounterInc(MetricHTTPRequestsTotal, "Total number of HTTP requests processed.", labels)
	DefaultRegistry.HistogramObserve(
		MetricHTTPRequestDuration,
		"HTTP request latency in seconds.",
		[]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2},
		map[string]string{"method": method, "path": path},
		duration.Seconds(),
	)
}

// RecordPayment records a payment lifecycle state.
func RecordPayment(status, currency string) {
	DefaultRegistry.CounterInc(
		MetricPaymentsTotal,
		"Total count of payments processed by lifecycle status.",
		map[string]string{
			"status":   status,
			"currency": currency,
		},
	)
}

// SetLedgerViolations records the current number of detected ledger invariant violations.
func SetLedgerViolations(violations int64) {
	DefaultRegistry.GaugeSet(
		MetricLedgerViolations,
		"Current number of ledger integrity invariant violations.",
		nil,
		violations,
	)
}

