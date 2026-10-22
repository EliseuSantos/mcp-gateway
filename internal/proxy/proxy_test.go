package proxy

import (
	"context"
	"errors"
	"testing"
)

// Remove once issue #4 has a real proxy.
func TestRunNotImplemented(t *testing.T) {
	t.Parallel()

	err := New(Config{ListenAddr: ":8080", UpstreamURL: "http://127.0.0.1:8081/mcp"}).Run(context.Background())
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("Run() error = %v, want ErrNotImplemented", err)
	}
}

func TestRunCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := New(Config{}).Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}
