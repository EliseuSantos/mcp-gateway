// Package auth authenticates gateway clients with an API key or a JWT.
//
// Not implemented. It plugs into the proxy after issue #4. No ticket is
// filed for the authenticators yet.
//
// The interface is a sketch. Change it when the real middleware lands.
package auth

import (
	"context"
	"errors"
	"net/http"
)

// ErrNotImplemented is returned by [Unimplemented].
var ErrNotImplemented = errors.New("auth: not implemented")

// Principal is the caller the rest of the gateway will see.
type Principal struct {
	// ID is a stable client identifier used by the allowlist and the audit log.
	ID string
	// Method is "api_key" or "jwt" once those authenticators exist.
	Method string
}

// Authenticator checks request headers. Keeping the input as headers
// leaves the session carrier up to ADR-001.
type Authenticator interface {
	Authenticate(ctx context.Context, headers http.Header) (Principal, error)
}

// Unimplemented is the stub wired into the binary today.
type Unimplemented struct{}

// Authenticate reports that no authenticator is installed.
func (Unimplemented) Authenticate(context.Context, http.Header) (Principal, error) {
	return Principal{}, ErrNotImplemented
}
