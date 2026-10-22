// Package telemetry will export OpenTelemetry traces from the gateway.
//
// docker-compose.yml already runs Jaeger (OTLP gRPC on :4317, OTLP HTTP on
// :4318, UI on :16686). No exporter is installed yet.
package telemetry

import (
	"context"
	"errors"
)

// ErrNotImplemented is returned by [Setup] until the exporter is wired.
var ErrNotImplemented = errors.New("telemetry: not implemented")

// Config is the provisional exporter configuration.
// Endpoint is host:port of the OTLP HTTP collector, without a scheme,
// for example "127.0.0.1:4318".
type Config struct {
	ServiceName string
	Endpoint    string
}

// Setup will install a tracer provider. Today it reports that tracing is
// not wired and returns a shutdown function that is safe to call, including
// when err is [ErrNotImplemented].
func Setup(context.Context, Config) (shutdown func(context.Context) error, err error) {
	return func(context.Context) error { return nil }, ErrNotImplemented
}
