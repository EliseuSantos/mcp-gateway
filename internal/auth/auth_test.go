package auth

import (
	"context"
	"errors"
	"testing"
)

// Remove once API key and JWT authenticators exist.
func TestUnimplemented(t *testing.T) {
	t.Parallel()

	_, err := Unimplemented{}.Authenticate(context.Background(), nil)
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("Authenticate() error = %v, want ErrNotImplemented", err)
	}
}
