package telemetry

import (
	"context"
	"errors"
	"testing"
)

// Remove once the OTLP exporter is wired.
func TestSetupNotImplemented(t *testing.T) {
	t.Parallel()

	shutdown, err := Setup(context.Background(), Config{ServiceName: "mcp-gateway", Endpoint: "127.0.0.1:4318"})
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("Setup() error = %v, want ErrNotImplemented", err)
	}
	if shutdown == nil {
		t.Fatal("shutdown func is nil")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown() error = %v", err)
	}
}
