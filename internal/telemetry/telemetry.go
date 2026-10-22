// Package telemetry exports OpenTelemetry traces and serves /metrics.
//
// An empty endpoint installs the W3C propagator and does not open an
// exporter, so the process still runs without Jaeger. ADR-003 records which
// fields may appear on a span.
package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/EliseuSantos/mcp-gateway/internal/proxy"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const tracerName = "github.com/EliseuSantos/mcp-gateway/internal/telemetry"

// Config selects the OTLP HTTP exporter. Endpoint is a base URL such as
// "http://jaeger:4318", or host:port, which is treated as http. A URL with
// no path is sent to /v1/traces. A URL that already has a path is used as given.
type Config struct {
	ServiceName string
	Endpoint    string
}

// Setup installs the W3C trace-context propagator. When Endpoint is empty the
// returned shutdown function is a no-op and no exporter is started. When it
// is set, spans are batched and exported to that collector.
func Setup(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	noop := func(context.Context) error { return nil }
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return noop, nil
	}
	normalized, err := normalizeEndpoint(endpoint)
	if err != nil {
		return noop, err
	}
	exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(normalized))
	if err != nil {
		return noop, fmt.Errorf("telemetry: exporter: %w", err)
	}
	name := strings.TrimSpace(cfg.ServiceName)
	if name == "" {
		name = "mcp-gateway"
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", name))),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithBatcher(exp, sdktrace.WithBatchTimeout(time.Second)),
	)
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}

func normalizeEndpoint(raw string) (string, error) {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("telemetry: endpoint %q is not an http(s) URL", raw)
	}
	// otlptracehttp.WithEndpointURL uses the path as given and does not append
	// /v1/traces. OTEL_EXPORTER_OTLP_ENDPOINT is a base URL, so an empty path
	// means the traces signal path.
	if strings.Trim(parsed.Path, "/") == "" {
		parsed.Path = "/v1/traces"
		parsed.RawPath = ""
	}
	return parsed.String(), nil
}

// WrapTransport returns a transport that injects W3C traceparent on the
// outgoing request and records a client span.
func WrapTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return otelhttp.NewTransport(base)
}

// WrapHandler records a server span for every request except /healthz.
// The span name is the method and the path.
func WrapHandler(next http.Handler) http.Handler {
	return otelhttp.NewHandler(next, "mcp",
		otelhttp.WithFilter(func(r *http.Request) bool {
			return r.URL.Path != "/healthz"
		}),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.Method + " " + r.URL.Path
		}),
	)
}

// HTTPMiddleware is [WrapHandler] as a proxy HTTP middleware.
// It belongs on /mcp only; /healthz and /metrics stay outside it.
func HTTPMiddleware() proxy.HTTPMiddleware {
	return func(next http.Handler) http.Handler {
		return WrapHandler(next)
	}
}
