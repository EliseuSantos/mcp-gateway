// Package audit writes one structured JSON record per gateway decision.
//
// Not implemented. It will be a proxy.Middleware around the upstream forward
// so one record can include the decision, the latency, and the upstream
// status. The intended record is a single JSON object so a denied
// delete_everything call can be shown in the video.
//
// The event shape is a sketch. Change it when the logger lands.
package audit

import (
	"context"
	"errors"
	"time"
)

// ErrNotImplemented is returned by [Unimplemented].
var ErrNotImplemented = errors.New("audit: not implemented")

// Event is one audit record.
// Allowed has no omitempty: a denied call must show false, not disappear.
type Event struct {
	Time     time.Time `json:"time"`
	ClientID string    `json:"client_id,omitempty"`
	Method   string    `json:"method"`
	Tool     string    `json:"tool,omitempty"`
	Allowed  bool      `json:"allowed"`
	Error    string    `json:"error,omitempty"`
}

// Logger records a decision.
type Logger interface {
	Log(ctx context.Context, event Event) error
}

// Unimplemented is the stub wired into the binary today.
type Unimplemented struct{}

// Log reports that no audit logger is installed.
func (Unimplemented) Log(context.Context, Event) error {
	return ErrNotImplemented
}
