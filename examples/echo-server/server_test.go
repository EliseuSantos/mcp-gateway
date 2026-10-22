package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestHealthz(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(NewHandler(Options{}))
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	body, err := io.ReadAll(res.Body)
	if closeErr := res.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if strings.TrimSpace(string(body)) != `{"status":"ok"}` {
		t.Fatalf("body = %q", body)
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	post, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /healthz: %v", err)
	}
	if closeErr := post.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}
	if post.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", post.StatusCode)
	}
}

func TestStreamableHTTPInitializeListAndCall(t *testing.T) {
	t.Parallel()

	fixed := time.Date(2026, 10, 22, 0, 34, 0, 0, time.UTC)
	session := connect(t, Options{Now: func() time.Time { return fixed }})
	ctx := session.ctx

	listed, err := session.client.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	got := map[string]string{}
	for _, tool := range listed.Tools {
		got[tool.Name] = tool.Description
	}
	for _, name := range []string{toolEcho, toolTime, toolDeleteEverything} {
		if got[name] == "" {
			t.Fatalf("tools/list missing %s; got %#v", name, got)
		}
	}
	if len(got) != 3 {
		t.Fatalf("tools/list = %#v, want 3 tools", got)
	}

	echo := callTool(t, session, toolEcho, map[string]any{"message": "ping"})
	if echo != "ping" {
		t.Fatalf("echo = %q, want ping", echo)
	}

	stamp := callTool(t, session, toolTime, map[string]any{})
	if stamp != fixed.Format(time.RFC3339) {
		t.Fatalf("time = %q, want %s", stamp, fixed.Format(time.RFC3339))
	}

	deleted := callTool(t, session, toolDeleteEverything, map[string]any{})
	if deleted != deleteNothingText {
		t.Fatalf("delete_everything = %q", deleted)
	}
	if !strings.Contains(deleted, "deleted nothing") {
		t.Fatalf("delete_everything = %q, want it to say nothing was deleted", deleted)
	}
}

func TestInitializeToolsListAndCallOnTheWire(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(NewHandler(Options{}))
	t.Cleanup(srv.Close)
	endpoint := srv.URL + "/mcp"

	initRes := postJSON(t, endpoint, "", `{
		"jsonrpc":"2.0","id":1,"method":"initialize",
		"params":{
			"protocolVersion":"2025-11-25",
			"capabilities":{},
			"clientInfo":{"name":"curl","version":"0"}
		}
	}`)
	if initRes.StatusCode != http.StatusOK {
		t.Fatalf("initialize status = %d, body %s", initRes.StatusCode, initRes.Body)
	}
	if ct := initRes.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("initialize content-type = %q, want application/json", ct)
	}
	sessionID := initRes.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatal("initialize response missing Mcp-Session-Id")
	}
	var initMsg struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(initRes.Body, &initMsg); err != nil {
		t.Fatalf("decode initialize: %v body %s", err, initRes.Body)
	}
	if len(initMsg.Error) > 0 && string(initMsg.Error) != "null" {
		t.Fatalf("initialize error = %s", initMsg.Error)
	}
	if initMsg.Result.ServerInfo.Name != serverName {
		t.Fatalf("serverInfo.name = %q", initMsg.Result.ServerInfo.Name)
	}
	if initMsg.Result.ProtocolVersion == "" {
		t.Fatal("initialize result missing protocolVersion")
	}

	// The SDK answers a notification with 202 Accepted.
	note := postJSON(t, endpoint, sessionID, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if note.StatusCode != http.StatusAccepted && note.StatusCode != http.StatusOK {
		t.Fatalf("initialized status = %d, body %s", note.StatusCode, note.Body)
	}

	list := postJSON(t, endpoint, sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	if list.StatusCode != http.StatusOK {
		t.Fatalf("tools/list status = %d, body %s", list.StatusCode, list.Body)
	}
	for _, name := range []string{toolEcho, toolTime, toolDeleteEverything} {
		if !strings.Contains(string(list.Body), `"`+name+`"`) {
			t.Fatalf("tools/list body missing %s: %s", name, list.Body)
		}
	}

	call := postJSON(t, endpoint, sessionID, `{
		"jsonrpc":"2.0","id":3,"method":"tools/call",
		"params":{"name":"echo","arguments":{"message":"ping"}}
	}`)
	if call.StatusCode != http.StatusOK {
		t.Fatalf("tools/call status = %d, body %s", call.StatusCode, call.Body)
	}
	if !strings.Contains(string(call.Body), "ping") {
		t.Fatalf("tools/call body = %s", call.Body)
	}
}

type connected struct {
	ctx    context.Context
	client *mcp.ClientSession
}

func connect(t *testing.T, opts Options) connected {
	t.Helper()

	srv := httptest.NewServer(NewHandler(opts))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)

	client := mcp.NewClient(&mcp.Implementation{Name: "echo-server-test", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	})
	return connected{ctx: ctx, client: session}
}

func callTool(t *testing.T, session connected, name string, args map[string]any) string {
	t.Helper()

	res, err := session.client.CallTool(session.ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
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

type httpResult struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

func postJSON(t *testing.T, endpoint, sessionID, body string) httpResult {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", endpoint, err)
	}
	raw, err := io.ReadAll(res.Body)
	if closeErr := res.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return httpResult{StatusCode: res.StatusCode, Header: res.Header.Clone(), Body: raw}
}
