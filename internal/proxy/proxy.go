// Package proxy sits between an MCP client and an upstream MCP server.
//
// Not implemented. ADR-001 (issue #1) still chooses the proxy model:
// terminate the downstream session and open a new one upstream, or forward
// JSON-RPC while inspecting tools/list and tools/call. Issue #4 is the
// pass-through, including the hooks where auth, policy, and audit attach.
//
// The types below are a sketch so the binary has a place to grow. Change
// them in the implementation pull request; they are not a contract yet.
package proxy

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotImplemented is returned until issue #4 lands.
var ErrNotImplemented = errors.New("proxy: not implemented (issue #4)")

// Config is the provisional set of knobs the process already accepts.
type Config struct {
	// ListenAddr is the downstream address, for example ":8080".
	ListenAddr string
	// UpstreamURL is the upstream Streamable HTTP endpoint,
	// for example "http://127.0.0.1:8081/mcp".
	UpstreamURL string
}

// Proxy is the process-level stand-in for the pass-through.
type Proxy struct {
	Config Config
}

// New returns a proxy that does not serve traffic yet.
func New(cfg Config) *Proxy {
	return &Proxy{Config: cfg}
}

// Run blocks until ctx is canceled once the proxy is implemented.
// Today it returns [ErrNotImplemented] unless ctx is already canceled.
// The wrapped error names the listen address and upstream so the stub
// log shows the configuration issue #4 will use.
func (p *Proxy) Run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("%w: listen %s, upstream %s", ErrNotImplemented, p.Config.ListenAddr, p.Config.UpstreamURL)
}
