package telemetry

import (
	"context"
	"time"

	"github.com/EliseuSantos/mcp-gateway/internal/proxy"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Middleware starts a span for the JSON-RPC message and records it in m.
// The span is a child of the HTTP span when [HTTPMiddleware] wrapped the
// request. Attributes are the method, tool, client, decision, and upstream
// status. Argument values are not attributes.
func Middleware(m *Metrics) proxy.Middleware {
	tracer := otel.Tracer(tracerName)
	return func(next proxy.Handler) proxy.Handler {
		return func(ctx context.Context, call *proxy.Call) (*proxy.Response, error) {
			ctx, span := tracer.Start(ctx, spanName(call), trace.WithSpanKind(trace.SpanKindServer))
			defer span.End()

			started := time.Now()
			resp, err := next(ctx, call)
			decision := call.Decision
			if decision == "" {
				if err != nil {
					decision = "error"
				} else {
					decision = "forward"
				}
			}
			upstream := 0
			if resp != nil {
				upstream = resp.UpstreamStatus
			}
			span.SetAttributes(spanAttrs(ctx, call, decision, upstream)...)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			}
			if m != nil {
				m.Observe(call.Method, decision, time.Since(started))
			}
			return resp, err
		}
	}
}

func spanName(call *proxy.Call) string {
	if call.Method == "" {
		return "jsonrpc"
	}
	if call.Tool != "" {
		return call.Method + " " + call.Tool
	}
	return call.Method
}

func spanAttrs(ctx context.Context, call *proxy.Call, decision string, upstream int) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("mcp.method", call.Method),
		attribute.String("mcp.decision", decision),
		attribute.Int("mcp.upstream_status", upstream),
	}
	if call.Tool != "" {
		attrs = append(attrs, attribute.String("mcp.tool", call.Tool))
	}
	if id, ok := proxy.PrincipalFromContext(ctx); ok && id != "" {
		attrs = append(attrs, attribute.String("mcp.client", id))
	}
	return attrs
}
