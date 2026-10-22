package policy

import (
	"errors"
	"testing"
)

// Remove once the allowlist exists.
func TestUnimplemented(t *testing.T) {
	t.Parallel()

	var allow Allowlist = Unimplemented{}

	if _, err := allow.VisibleTools("client", []string{"echo", "delete_everything"}); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("VisibleTools() error = %v, want ErrNotImplemented", err)
	}
	if _, err := allow.AllowCall("client", "delete_everything"); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("AllowCall() error = %v, want ErrNotImplemented", err)
	}
}
