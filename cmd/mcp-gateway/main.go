// Command mcp-gateway is the process entrypoint.
//
// The proxy is not implemented. This command logs the planned wiring and
// exits. Issue #4 replaces the body of main with a blocking serve.
// Auth, policy, and audit are package stubs; the assignments below only
// keep this file as the map of the process.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

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
	listen := flag.String("listen", ":8080", "downstream listen address (unused until issue #4)")
	upstream := flag.String("upstream", "http://127.0.0.1:8081/mcp", "upstream MCP endpoint (unused until issue #4)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx := context.Background()

	shutdown, telErr := telemetry.Setup(ctx, telemetry.Config{
		ServiceName: "mcp-gateway",
		Endpoint:    "127.0.0.1:4318",
	})
	defer func() {
		if err := shutdown(ctx); err != nil {
			logger.Error("telemetry shutdown", "err", err)
		}
	}()

	p := proxy.New(proxy.Config{ListenAddr: *listen, UpstreamURL: *upstream})
	logger.Info("mcp-gateway scaffold; proxy is not implemented",
		"listen", *listen,
		"upstream", *upstream,
		"proxy_err", p.Run(ctx),
		"telemetry_err", telErr,
	)
	logger.Info("next: issue #4 implements the pass-through; auth, policy, and audit stay stubs until then")
	logger.Info("example upstream", "command", "go run ./examples/echo-server")
}
