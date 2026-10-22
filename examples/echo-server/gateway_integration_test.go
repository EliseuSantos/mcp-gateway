package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EliseuSantos/mcp-gateway/internal/proxy"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestGatewayProxiesEchoServer is the client → gateway → echo-server path.
// The gateway is the pass-through: nothing is filtered yet.
func TestGatewayProxiesEchoServer(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(NewHandler(Options{}))
	t.Cleanup(upstream.Close)

	gwProxy, err := proxy.New(proxy.Config{UpstreamURL: upstream.URL + "/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(gwProxy.Handler())
	t.Cleanup(gateway.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)

	client := mcp.NewClient(&mcp.Implementation{Name: "gateway-integration", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: gateway.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("initialize through gateway: %v", err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	})
	if session.ID() == "" {
		t.Fatal("gateway did not assign a session id")
	}

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	got := map[string]bool{}
	for _, tool := range listed.Tools {
		got[tool.Name] = true
	}
	for _, name := range []string{toolEcho, toolTime, toolDeleteEverything} {
		if !got[name] {
			t.Fatalf("tools/list missing %s; got %#v", name, got)
		}
	}
	if len(got) != 3 {
		t.Fatalf("tools/list = %#v, want 3 tools", got)
	}

	echo := callThroughGateway(t, ctx, session, toolEcho, map[string]any{"message": "ping"})
	if echo != "ping" {
		t.Fatalf("echo = %q, want ping", echo)
	}

	deleted := callThroughGateway(t, ctx, session, toolDeleteEverything, map[string]any{})
	if !strings.Contains(deleted, "deleted nothing") {
		t.Fatalf("delete_everything = %q", deleted)
	}
}

func callThroughGateway(t *testing.T, ctx context.Context, session *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
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
			t.Fatalf("tools/call %s content type %T", name, content)
		}
		buf.WriteString(text.Text)
	}
	return buf.String()
}
