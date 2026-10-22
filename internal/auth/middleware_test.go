package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EliseuSantos/mcp-gateway/internal/proxy"
)

func TestMiddlewareRejectsBeforeNext(t *testing.T) {
	t.Parallel()

	creds := mustCreds(t, []Key{{ID: "demo", Secret: "dev-demo-key"}}, nil)
	var hits int
	var gotID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		gotID, _ = proxy.PrincipalFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	handler := Middleware(creds)(next)

	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if denied.Code != http.StatusUnauthorized || hits != 0 {
		t.Fatalf("denied status = %d hits = %d", denied.Code, hits)
	}
	if !strings.Contains(denied.Body.String(), "authentication required") {
		t.Fatalf("body = %s", denied.Body.String())
	}
	if denied.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("missing WWW-Authenticate")
	}

	allowed := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("X-Api-Key", "dev-demo-key")
	handler.ServeHTTP(allowed, req)
	if allowed.Code != http.StatusNoContent || hits != 1 || gotID != "demo" {
		t.Fatalf("allowed status = %d hits = %d id = %q", allowed.Code, hits, gotID)
	}
}
