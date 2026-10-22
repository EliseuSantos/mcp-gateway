package policy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/EliseuSantos/mcp-gateway/internal/proxy"
)

func TestAllowCallAndVisibleTools(t *testing.T) {
	t.Parallel()

	p := mustPolicy(t, map[string][]string{
		"demo":  {"echo", "time"},
		"admin": {"echo", "time", "delete_everything"},
		"none":  {},
	})
	cases := []struct {
		client string
		tool   string
		allow  bool
	}{
		{client: "demo", tool: "echo", allow: true},
		{client: "demo", tool: "time", allow: true},
		{client: "demo", tool: "delete_everything", allow: false},
		{client: "admin", tool: "delete_everything", allow: true},
		{client: "none", tool: "echo", allow: false},
		{client: "unknown", tool: "echo", allow: false},
		{client: "demo", tool: "", allow: false},
		{client: "", tool: "echo", allow: false},
	}
	for _, tc := range cases {
		t.Run(tc.client+"/"+tc.tool, func(t *testing.T) {
			t.Parallel()
			got, err := p.AllowCall(tc.client, tc.tool)
			if err != nil {
				t.Fatal(err)
			}
			if got.Allowed != tc.allow {
				t.Fatalf("Allowed = %v, reason %q", got.Allowed, got.Reason)
			}
			if !tc.allow && !strings.Contains(got.Reason, "not allowed") {
				t.Fatalf("reason = %q", got.Reason)
			}
		})
	}

	visible, err := p.VisibleTools("demo", []string{"delete_everything", "echo", "other", "time"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(visible, ",") != "echo,time" {
		t.Fatalf("visible = %#v", visible)
	}
	unknown, err := p.VisibleTools("unknown", []string{"echo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(unknown) != 0 {
		t.Fatalf("unknown visible = %#v", unknown)
	}
}

func TestMiddlewareFiltersAndRejects(t *testing.T) {
	t.Parallel()

	p := mustPolicy(t, map[string][]string{"demo": {"echo", "time"}})
	up := &recorder{}
	upstream := httptest.NewServer(up)
	t.Cleanup(upstream.Close)
	gwProxy, err := proxy.New(proxy.Config{
		UpstreamURL: upstream.URL,
		Middleware:  []proxy.Middleware{p.Middleware()},
	})
	if err != nil {
		t.Fatal(err)
	}
	// httptest builds a fresh request context, so the principal is copied
	// from a test header onto the context in front of the proxy.
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := proxy.WithPrincipal(r.Context(), r.Header.Get("X-Test-Client"))
		gwProxy.Handler().ServeHTTP(w, r.WithContext(ctx))
	}))
	t.Cleanup(direct.Close)

	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	listedBody := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
	cases := []struct {
		name       string
		client     string
		body       string
		session    bool
		wantStatus int
		wantCode   int64
		wantTools  []string
		wantText   string
		upstream   bool
	}{
		{name: "initialize", client: "demo", body: initBody, wantStatus: http.StatusOK, upstream: true},
		{name: "list", client: "demo", body: listedBody, session: true, wantStatus: http.StatusOK, wantTools: []string{"echo", "time"}, upstream: true},
		{name: "allow call", client: "demo", body: callBody(3, "echo"), session: true, wantStatus: http.StatusOK, wantText: "ping", upstream: true},
		{name: "deny call", client: "demo", body: callBody(4, "delete_everything"), session: true, wantStatus: http.StatusOK, wantCode: CodeToolNotAllowed, upstream: false},
		{name: "string id", client: "demo", body: `{"jsonrpc":"2.0","id":"abc","method":"tools/call","params":{"name":"delete_everything"}}`, session: true, wantStatus: http.StatusOK, wantCode: CodeToolNotAllowed, upstream: false},
		{name: "unknown client", client: "other", body: callBody(5, "echo"), session: true, wantStatus: http.StatusOK, wantCode: CodeToolNotAllowed, upstream: false},
	}

	var session string
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := up.count()
			body := tc.body
			req, err := http.NewRequest(http.MethodPost, direct.URL+"/mcp", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			req.Header.Set("X-Test-Client", tc.client)
			if tc.session {
				if session == "" {
					t.Fatal("session was not established")
				}
				req.Header.Set("Mcp-Session-Id", session)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			raw := readBody(t, res)
			if res.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, body %s", res.StatusCode, raw)
			}
			if !tc.session && tc.name == "initialize" {
				session = res.Header.Get("Mcp-Session-Id")
				if session == "" {
					t.Fatal("missing session")
				}
			}
			if tc.wantCode != 0 {
				code, id := rpcError(t, raw)
				if code != tc.wantCode {
					t.Fatalf("code = %d, body %s", code, raw)
				}
				if tc.name == "string id" && id != `"abc"` {
					t.Fatalf("id = %s", id)
				}
				if tc.name == "deny call" && id != "4" {
					t.Fatalf("id = %s", id)
				}
			}
			if tc.wantTools != nil {
				names := toolNames(t, raw)
				if strings.Join(names, ",") != strings.Join(tc.wantTools, ",") {
					t.Fatalf("tools = %#v, body %s", names, raw)
				}
				if !strings.Contains(string(raw), `a & b`) {
					t.Fatalf("description was re-encoded: %s", raw)
				}
				if strings.Contains(string(raw), "delete_everything") {
					t.Fatalf("denied tool leaked: %s", raw)
				}
				if !strings.Contains(string(raw), "nextCursor") {
					t.Fatalf("cursor dropped: %s", raw)
				}
			}
			if tc.wantText != "" && !strings.Contains(string(raw), tc.wantText) {
				t.Fatalf("body = %s", raw)
			}
			if got := up.count() - before; (got > 0) != tc.upstream {
				t.Fatalf("upstream calls delta = %d, want contacted %v", got, tc.upstream)
			}
		})
	}
}

func TestNewRejects(t *testing.T) {
	t.Parallel()
	if _, err := New(map[string][]string{"": {"echo"}}); err == nil {
		t.Fatal("empty client id")
	}
	if _, err := New(map[string][]string{"demo": {""}}); err == nil {
		t.Fatal("empty tool")
	}
}

type recorder struct {
	mu sync.Mutex
	n  int
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	r.n++
	r.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Mcp-Session-Id", "upstream-session")
	switch {
	case strings.Contains(string(body), `"method":"initialize"`):
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{},"serverInfo":{"name":"up","version":"0"}}}`)
	case strings.Contains(string(body), `"method":"tools/list"`):
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{"nextCursor":"page-2","tools":[{"name":"delete_everything","description":"nope"},{"name":"echo","description":"a & b"},{"name":"time","description":"now"}]}}`)
	default:
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"ping"}]}}`)
	}
}

func mustPolicy(t *testing.T, clients map[string][]string) *Policy {
	t.Helper()
	p, err := New(clients)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func callBody(id int, tool string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"%s"}}`, id, tool)
}

func readBody(t *testing.T, res *http.Response) []byte {
	t.Helper()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Body.Close(); err != nil {
		t.Fatal(err)
	}
	return body
}

func rpcError(t *testing.T, body []byte) (int64, string) {
	t.Helper()
	var msg struct {
		ID    json.RawMessage `json:"id"`
		Error struct {
			Code int64 `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("decode: %v body %s", err, body)
	}
	return msg.Error.Code, string(msg.ID)
}

func toolNames(t *testing.T, body []byte) []string {
	t.Helper()
	var msg struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("decode: %v body %s", err, body)
	}
	names := make([]string, 0, len(msg.Result.Tools))
	for _, tool := range msg.Result.Tools {
		names = append(names, tool.Name)
	}
	return names
}
