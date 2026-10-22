package telemetry

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Histogram buckets for JSON-RPC handling, in seconds.
var latencyBounds = []float64{0.005, 0.01, 0.05, 0.1, 0.5, 1, 5}

type seriesKey struct {
	method   string
	decision string
}

type latencyStat struct {
	buckets []uint64
	sum     float64
	count   uint64
}

// Metrics counts JSON-RPC requests. The handler is the Prometheus text
// exposition on /metrics. Labels are the method and the decision. Client
// ids and argument values are not labels.
type Metrics struct {
	mu      sync.Mutex
	counts  map[seriesKey]uint64
	latency map[string]*latencyStat
}

// NewMetrics returns an empty collector.
func NewMetrics() *Metrics {
	return &Metrics{
		counts:  map[seriesKey]uint64{},
		latency: map[string]*latencyStat{},
	}
}

// Observe records one finished JSON-RPC request.
func (m *Metrics) Observe(method, decision string, elapsed time.Duration) {
	if m == nil {
		return
	}
	method = labelOr(method, "jsonrpc")
	decision = labelOr(decision, "forward")
	seconds := elapsed.Seconds()
	if seconds < 0 {
		seconds = 0
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts[seriesKey{method: method, decision: decision}]++
	stat := m.latency[method]
	if stat == nil {
		stat = &latencyStat{buckets: make([]uint64, len(latencyBounds))}
		m.latency[method] = stat
	}
	stat.count++
	stat.sum += seconds
	// Stored counts are cumulative: bucket i includes every observation at or below that bound.
	for i, bound := range latencyBounds {
		if seconds <= bound {
			stat.buckets[i]++
		}
	}
}

// Handler serves the current counters. It is meant to be mounted at /metrics.
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(m.serveHTTP)
}

func (m *Metrics) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body := m.expose()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		return
	}
	_, _ = w.Write(body)
}

func (m *Metrics) expose() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()

	var buf strings.Builder
	buf.WriteString("# HELP mcp_gateway_requests_total JSON-RPC requests handled by the gateway.\n")
	buf.WriteString("# TYPE mcp_gateway_requests_total counter\n")
	keys := slices.SortedFunc(maps.Keys(m.counts), func(a, b seriesKey) int {
		if a.method != b.method {
			return strings.Compare(a.method, b.method)
		}
		return strings.Compare(a.decision, b.decision)
	})
	for _, key := range keys {
		fmt.Fprintf(&buf, "mcp_gateway_requests_total{method=\"%s\",decision=\"%s\"} %d\n", escapeLabel(key.method), escapeLabel(key.decision), m.counts[key])
	}

	buf.WriteString("# HELP mcp_gateway_request_duration_seconds Time spent handling one JSON-RPC request.\n")
	buf.WriteString("# TYPE mcp_gateway_request_duration_seconds histogram\n")
	for _, method := range slices.Sorted(maps.Keys(m.latency)) {
		stat := m.latency[method]
		for i, bound := range latencyBounds {
			fmt.Fprintf(&buf, "mcp_gateway_request_duration_seconds_bucket{method=\"%s\",le=\"%s\"} %d\n", escapeLabel(method), strconv.FormatFloat(bound, 'f', -1, 64), stat.buckets[i])
		}
		fmt.Fprintf(&buf, "mcp_gateway_request_duration_seconds_bucket{method=\"%s\",le=\"+Inf\"} %d\n", escapeLabel(method), stat.count)
		fmt.Fprintf(&buf, "mcp_gateway_request_duration_seconds_sum{method=\"%s\"} %s\n", escapeLabel(method), strconv.FormatFloat(stat.sum, 'f', -1, 64))
		fmt.Fprintf(&buf, "mcp_gateway_request_duration_seconds_count{method=\"%s\"} %d\n", escapeLabel(method), stat.count)
	}
	return []byte(buf.String())
}

func labelOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func escapeLabel(value string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`).Replace(value)
}
