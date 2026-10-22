// Command mcp-gateway terminates downstream MCP sessions and forwards
// JSON-RPC to an upstream Streamable HTTP server.
//
// With a config file that sets auth and policy, every /mcp request must
// authenticate and tools are limited to that client's allowlist. Without
// that file the process is the pass-through proxy. Either way, each
// JSON-RPC request writes an audit line to stdout. Traces are exported
// when an OTLP endpoint is configured.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/EliseuSantos/mcp-gateway/internal/audit"
	"github.com/EliseuSantos/mcp-gateway/internal/auth"
	"github.com/EliseuSantos/mcp-gateway/internal/config"
	"github.com/EliseuSantos/mcp-gateway/internal/policy"
	"github.com/EliseuSantos/mcp-gateway/internal/proxy"
	"github.com/EliseuSantos/mcp-gateway/internal/telemetry"
)

// version is the build version. Goreleaser sets it with -X main.version.
var version = "dev"

var (
	_ auth.Authenticator = (*auth.Credentials)(nil)
	_ policy.Allowlist   = (*policy.Policy)(nil)
	_ audit.Logger       = (*audit.Writer)(nil)
)

func main() {
	flags, err := parseArgs(os.Args[1:], os.Getenv, os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		os.Exit(2)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	logger.Info("starting", "version", version)
	file, err := config.Load(flags.ConfigPath)
	if err != nil {
		logger.Error("config", "err", err)
		os.Exit(1)
	}
	listen := config.Pick(flags.Listen, flags.ListenSet, os.Getenv("MCP_GATEWAY_LISTEN"), file.Listen, ":8080")
	upstream := config.Pick(flags.Upstream, flags.UpstreamSet, os.Getenv("MCP_GATEWAY_UPSTREAM"), file.Upstream, "http://127.0.0.1:8081/mcp")

	httpMW, rpcMW, err := wire(file)
	if err != nil {
		logger.Error("config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	endpoint := config.Pick("", false, os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), file.Telemetry.OTLPEndpoint, "")
	service := config.Pick("", false, os.Getenv("OTEL_SERVICE_NAME"), file.Telemetry.ServiceName, "mcp-gateway")
	shutdown, err := telemetry.Setup(ctx, telemetry.Config{
		ServiceName: service,
		Endpoint:    endpoint,
	})
	if err != nil {
		logger.Error("telemetry", "err", err)
		os.Exit(1)
	}
	defer func() {
		if err := shutdown(context.Background()); err != nil {
			logger.Error("telemetry shutdown", "err", err)
		}
	}()
	if endpoint != "" {
		logger.Info("tracing", "endpoint", endpoint, "service", service)
	}

	auditLog, err := audit.New(os.Stdout, file.Audit.ArgumentFields, logger)
	if err != nil {
		logger.Error("audit", "err", err)
		os.Exit(1)
	}
	logger.Info("audit", "argument_fields", file.Audit.ArgumentFields)
	metrics := telemetry.NewMetrics()
	rpcMW = append([]proxy.Middleware{telemetry.Middleware(metrics), auditLog.Middleware()}, rpcMW...)
	httpMW = append([]proxy.HTTPMiddleware{telemetry.HTTPMiddleware()}, httpMW...)

	gw, err := proxy.New(proxy.Config{
		ListenAddr:     listen,
		UpstreamURL:    upstream,
		Logger:         logger,
		Client:         &http.Client{Transport: telemetry.WrapTransport(proxy.UpstreamTransport())},
		Middleware:     rpcMW,
		HTTPMiddleware: httpMW,
		Metrics:        metrics.Handler(),
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

func wire(file config.File) ([]proxy.HTTPMiddleware, []proxy.Middleware, error) {
	if file.Auth == nil {
		return nil, nil, nil
	}
	creds, err := credentialsFrom(file.Auth)
	if err != nil {
		return nil, nil, err
	}
	allow, err := policyFrom(file.Policy)
	if err != nil {
		return nil, nil, err
	}
	return []proxy.HTTPMiddleware{auth.Middleware(creds)}, []proxy.Middleware{allow.Middleware()}, nil
}

func credentialsFrom(file *config.AuthFile) (*auth.Credentials, error) {
	keys := make([]auth.Key, 0, len(file.APIKeys))
	for _, key := range file.APIKeys {
		keys = append(keys, auth.Key{ID: key.ID, Secret: key.Key})
	}
	var opts *auth.JWTOptions
	if file.JWT != nil {
		built, err := jwtOptions(*file.JWT)
		if err != nil {
			return nil, err
		}
		opts = &built
	}
	return auth.NewCredentials(keys, opts)
}

func jwtOptions(file config.JWTFile) (auth.JWTOptions, error) {
	opts := auth.JWTOptions{
		JWKSURL:       strings.TrimSpace(file.JWKSURL),
		Issuer:        file.Issuer,
		Audience:      file.Audience,
		ClientIDClaim: file.ClientIDClaimOrDefault(),
	}
	if secret := strings.TrimSpace(file.HMACSecret); secret != "" {
		opts.HMACSecret = []byte(secret)
	}
	pemBytes := []byte(file.PublicKey)
	if path := strings.TrimSpace(file.PublicKeyFile); path != "" {
		body, err := os.ReadFile(path)
		if err != nil {
			return auth.JWTOptions{}, fmt.Errorf("config: read public key: %w", err)
		}
		pemBytes = body
	}
	if strings.TrimSpace(string(pemBytes)) != "" {
		key, err := auth.ParseRSAPublicKey(pemBytes)
		if err != nil {
			return auth.JWTOptions{}, err
		}
		opts.PublicKey = key
	}
	return opts, nil
}

func policyFrom(file *config.PolicyFile) (*policy.Policy, error) {
	clients := make(map[string][]string, len(file.Clients))
	for id, client := range file.Clients {
		clients[id] = append([]string(nil), client.Tools...)
	}
	return policy.New(clients)
}
