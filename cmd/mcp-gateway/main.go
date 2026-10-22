// Command mcp-gateway terminates downstream MCP sessions and forwards
// JSON-RPC to an upstream Streamable HTTP server.
//
// Auth, policy, and audit are still stubs. Their hook points are the HTTP
// middleware and the JSON-RPC middleware on [proxy.Config]. Wire them there
// when those packages stop returning ErrNotImplemented.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/EliseuSantos/mcp-gateway/internal/audit"
	"github.com/EliseuSantos/mcp-gateway/internal/auth"
	"github.com/EliseuSantos/mcp-gateway/internal/policy"
	"github.com/EliseuSantos/mcp-gateway/internal/proxy"
	"github.com/EliseuSantos/mcp-gateway/internal/telemetry"
)

// Compile-time checks that the stub types still satisfy the sketched interfaces.
var (
	_ auth.Authenticator = auth.Unimplemented{}
	_ policy.Allowlist   = policy.Unimplemented{}
	_ audit.Logger       = audit.Unimplemented{}
)

func main() {
	cfg, err := parseArgs(os.Args[1:], os.Getenv, os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		os.Exit(2)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdown, telErr := telemetry.Setup(ctx, telemetry.Config{
		ServiceName: "mcp-gateway",
		Endpoint:    "127.0.0.1:4318",
	})
	defer func() {
		if err := shutdown(context.Background()); err != nil {
			logger.Error("telemetry shutdown", "err", err)
		}
	}()
	if telErr != nil {
		logger.Warn("telemetry is not wired yet", "err", telErr)
	}

	// Extension points, left empty until the packages are real:
	//   HTTPMiddleware: auth, before the JSON-RPC body is parsed.
	//   Middleware:     policy (outer) then audit (around the forward).
	gw, err := proxy.New(proxy.Config{
		ListenAddr:  cfg.Listen,
		UpstreamURL: cfg.Upstream,
		Logger:      logger,
	})
	if err != nil {
		logger.Error("config", "err", err)
		os.Exit(1)
	}
	if err := gw.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("serve", "err", err)
		os.Exit(1)
	}
}
