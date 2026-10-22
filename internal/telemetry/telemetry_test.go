package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EliseuSantos/mcp-gateway/internal/proxy"
)

func TestSetupEmptyEndpoint(t *testing.T) {
	shutdown, err := Setup(context.Background(), Config{ServiceName: "mcp-gateway"})
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSetupRejectsBadEndpoint(t *testing.T) {
	_, err := Setup(context.Background(), Config{Endpoint: "ftp://collector"})
	if err == nil {
		t.Fatal("error = nil")
	}
}

func TestMetricsCountDecision(t *testing.T) {
	m := NewMetrics()
	m.Observe("tools/call", "deny", 200*time.Microsecond)
	m.Observe("tools/call", "allow", 2*time.Millisecond)
	body := string(m.expose())
	if !strings.Contains(body, `mcp_gateway_requests_total{method="tools/call",decision="allow"} 1`) {
		t.Fatalf("missing allow count:\n%s", body)
	}
	if !strings.Contains(body, `mcp_gateway_requests_total{method="tools/call",decision="deny"} 1`) {
		t.Fatalf("missing deny count:\n%s", body)
	}
	if !strings.Contains(body, `mcp_gateway_request_duration_seconds_count{method="tools/call"} 2`) {
		t.Fatalf("missing histogram count:\n%s", body)
	}
	if strings.Contains(body, "secret") {
		t.Fatalf("metrics body has unexpected text:\n%s", body)
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "mcp_gateway_requests_total") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	head := httptest.NewRequest(http.MethodHead, "/metrics", nil)
	rec = httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, head)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("HEAD status %d len %d", rec.Code, rec.Body.Len())
	}
}

func TestTraceparentReachesUpstream(t *testing.T) {
	var got atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.Header.Get("Traceparent"))
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		if !strings.Contains(string(body), `"method":"tools/call"`) {
			t.Errorf("upstream body = %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`))
	}))
	t.Cleanup(upstream.Close)

	var exports atomic.Int32
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		if r.URL.Path == "/v1/traces" {
			exports.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(collector.Close)

	shutdown, err := Setup(t.Context(), Config{ServiceName: "mcp-gateway", Endpoint: collector.URL})
	if err != nil {
		t.Fatal(err)
	}

	metrics := NewMetrics()
	gw, err := proxy.New(proxy.Config{
		UpstreamURL:    upstream.URL,
		Client:         &http.Client{Transport: WrapTransport(proxy.UpstreamTransport())},
		Middleware:     []proxy.Middleware{Middleware(metrics)},
		HTTPMiddleware: []proxy.HTTPMiddleware{HTTPMiddleware()},
		Metrics:        metrics.Handler(),
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(gw.Handler())
	t.Cleanup(server.Close)

	req, err := http.NewRequest(http.MethodPost, server.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"message":"ping","token":"secret"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}

	traceparent, _ := got.Load().(string)
	if !validTraceparent(traceparent) {
		t.Fatalf("traceparent = %q", traceparent)
	}

	metricsBody := string(metrics.expose())
	if !strings.Contains(metricsBody, `decision="forward"`) {
		t.Fatalf("metrics = %s", metricsBody)
	}
	if strings.Contains(metricsBody, "secret") || strings.Contains(metricsBody, "ping") {
		t.Fatalf("metrics leaked arguments: %s", metricsBody)
	}

	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if exports.Load() == 0 {
		t.Fatal("OTLP collector received no traces")
	}
}

func validTraceparent(value string) bool {
	parts := strings.Split(value, "-")
	if len(parts) != 4 || parts[0] != "00" || len(parts[1]) != 32 || len(parts[2]) != 16 {
		return false
	}
	if parts[1] == strings.Repeat("0", 32) || parts[2] == strings.Repeat("0", 16) {
		return false
	}
	return true
}
