package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/EliseuSantos/mcp-gateway/internal/auth"
	"github.com/EliseuSantos/mcp-gateway/internal/policy"
	"github.com/EliseuSantos/mcp-gateway/internal/proxy"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAllowlistFiltersEchoServer(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(NewHandler(Options{}))
	t.Cleanup(upstream.Close)

	creds, err := auth.NewCredentials([]auth.Key{
		{ID: "demo", Secret: "dev-demo-key"},
		{ID: "admin", Secret: "dev-admin-key"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	allow, err := policy.New(map[string][]string{
		"demo":  {"echo", "time"},
		"admin": {"echo", "time", toolDeleteEverything},
	})
	if err != nil {
		t.Fatal(err)
	}
	gw, err := proxy.New(proxy.Config{
		UpstreamURL:    upstream.URL + "/mcp",
		HTTPMiddleware: []proxy.HTTPMiddleware{auth.Middleware(creds)},
		Middleware:     []proxy.Middleware{allow.Middleware()},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(gw.Handler())
	t.Cleanup(server.Close)

	denied, err := http.Post(server.URL+"/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(denied.Body)
	if closeErr := denied.Body.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if denied.StatusCode != http.StatusUnauthorized || !strings.Contains(string(body), "authentication required") {
		t.Fatalf("unauthenticated = %d %s", denied.StatusCode, body)
	}

	demo := connectAs(t, server.URL+"/mcp", "dev-demo-key")
	names := toolNames(t, demo)
	if sortedJoin(names) != "echo,time" {
		t.Fatalf("demo tools = %#v", names)
	}
	if got := callText(t, demo, toolEcho, map[string]any{"message": "ping"}); got != "ping" {
		t.Fatalf("echo = %q", got)
	}
	if _, err := demo.client.CallTool(demo.ctx, &mcp.CallToolParams{Name: toolDeleteEverything, Arguments: map[string]any{}}); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("delete_everything error = %v", err)
	}

	admin := connectAs(t, server.URL+"/mcp", "dev-admin-key")
	adminNames := toolNames(t, admin)
	if sortedJoin(adminNames) != "delete_everything,echo,time" {
		t.Fatalf("admin tools = %#v", adminNames)
	}
	deleted := callText(t, admin, toolDeleteEverything, map[string]any{})
	if !strings.Contains(deleted, "deleted nothing") {
		t.Fatalf("admin delete = %q", deleted)
	}
}

type authedSession struct {
	ctx    context.Context
	client *mcp.ClientSession
}

func connectAs(t *testing.T, endpoint, key string) authedSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)
	client := mcp.NewClient(&mcp.Implementation{Name: "allowlist-test", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Transport: bearerTransport{base: http.DefaultTransport, key: key}},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	})
	return authedSession{ctx: ctx, client: session}
}

func sortedJoin(names []string) string {
	copied := append([]string(nil), names...)
	sort.Strings(copied)
	return strings.Join(copied, ",")
}

func toolNames(t *testing.T, session authedSession) []string {
	t.Helper()
	listed, err := session.client.ListTools(session.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func callText(t *testing.T, session authedSession, name string, args map[string]any) string {
	t.Helper()
	res, err := session.client.CallTool(session.ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("tools/call %s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("tools/call %s isError: %+v", name, res)
	}
	var buf strings.Builder
	for _, content := range res.Content {
		text, ok := content.(*mcp.TextContent)
		if !ok {
			t.Fatalf("content %T", content)
		}
		buf.WriteString(text.Text)
	}
	return buf.String()
}

type bearerTransport struct {
	base http.RoundTripper
	key  string
}

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+b.key)
	return b.base.RoundTrip(clone)
}
