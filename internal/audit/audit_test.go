package audit

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Remove once the JSON logger exists.
func TestUnimplemented(t *testing.T) {
	t.Parallel()

	err := Unimplemented{}.Log(context.Background(), Event{
		Time:     time.Unix(0, 0).UTC(),
		ClientID: "client",
		Method:   "tools/call",
		Tool:     "delete_everything",
		Allowed:  false,
	})
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("Log() error = %v, want ErrNotImplemented", err)
	}
}
