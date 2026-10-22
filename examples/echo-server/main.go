// Command echo-server is the example MCP server used by tests and by the
// recorded demo. It speaks Streamable HTTP and exposes echo, time, and a
// fake delete_everything tool.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/EliseuSantos/mcp-gateway/internal/telemetry"
)

func main() {
	addr := flag.String("addr", ":8081", "listen address")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	handler := http.Handler(NewHandler(Options{}))
	if endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")); endpoint != "" {
		name := strings.TrimSpace(os.Getenv("OTEL_SERVICE_NAME"))
		if name == "" {
			name = serverName
		}
		shutdown, err := telemetry.Setup(ctx, telemetry.Config{ServiceName: name, Endpoint: endpoint})
		if err != nil {
			logger.Error("telemetry", "err", err)
			os.Exit(1)
		}
		defer func() {
			if err := shutdown(context.Background()); err != nil {
				logger.Error("telemetry shutdown", "err", err)
			}
		}()
		handler = telemetry.WrapHandler(handler)
		logger.Info("tracing", "endpoint", endpoint, "service", name)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", srv.Addr, "mcp", "/mcp", "health", "/healthz")
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown", "err", err)
			os.Exit(1)
		}
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("listen", "err", err)
			os.Exit(1)
		}
	}
}
