// Command mcp-gateway terminates downstream MCP sessions and forwards
// JSON-RPC to an upstream Streamable HTTP server.
//
// With a config file that sets auth and policy, every /mcp request must
// authenticate and tools are limited to that client's allowlist. Without
// that file the process is the pass-through proxy.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
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

var (
	_ auth.Authenticator = (*auth.Credentials)(nil)
	_ policy.Allowlist   = (*policy.Policy)(nil)
	_ audit.Logger       = audit.Unimplemented{}
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

	gw, err := proxy.New(proxy.Config{
		ListenAddr:     listen,
		UpstreamURL:    upstream,
		Logger:         logger,
		Middleware:     rpcMW,
		HTTPMiddleware: httpMW,
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
