// Package policy is the per-client tool allowlist.
//
// Not implemented. It will be a proxy.Middleware: reject a disallowed
// tools/call before the upstream forward, and filter a buffered tools/list
// on the way back. The example server's delete_everything tool is the
// denial the demo will show.
//
// The interface is a sketch. Change it when the real allowlist lands.
package policy

import "errors"

// ErrNotImplemented is returned by [Unimplemented].
var ErrNotImplemented = errors.New("policy: not implemented")

// Decision is the result of a tools/call check.
type Decision struct {
	Allowed bool
	Reason  string
}

// Allowlist decides which tools a client may see and call.
type Allowlist interface {
	// VisibleTools drops tools that must not appear in tools/list.
	VisibleTools(clientID string, tools []string) ([]string, error)
	// AllowCall reports whether tools/call is allowed.
	AllowCall(clientID, tool string) (Decision, error)
}

// Unimplemented is the stub wired into the binary today.
type Unimplemented struct{}

// VisibleTools reports that filtering is not installed.
func (Unimplemented) VisibleTools(string, []string) ([]string, error) {
	return nil, ErrNotImplemented
}

// AllowCall reports that authorization is not installed.
func (Unimplemented) AllowCall(string, string) (Decision, error) {
	return Decision{}, ErrNotImplemented
}
