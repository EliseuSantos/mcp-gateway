package auth

import (
	"encoding/json"
	"net/http"

	"github.com/EliseuSantos/mcp-gateway/internal/proxy"
)

// Middleware returns the proxy HTTP middleware for a.
// It rejects a request before the JSON-RPC body is read.
func Middleware(a Authenticator) proxy.HTTPMiddleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, err := a.Authenticate(r.Context(), r.Header)
			if err != nil || principal.ID == "" {
				writeUnauthenticated(w)
				return
			}
			ctx := proxy.WithPrincipal(r.Context(), principal.ID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeUnauthenticated(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="mcp-gateway"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	body, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      any    `json:"id"`
		Error   struct {
			Code    int64  `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{
		JSONRPC: "2.0",
		ID:      nil,
		Error: struct {
			Code    int64  `json:"code"`
			Message string `json:"message"`
		}{Code: CodeUnauthenticated, Message: "authentication required"},
	})
	if err != nil {
		return
	}
	_, _ = w.Write(body)
}
