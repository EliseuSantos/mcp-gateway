package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`

func TestNewRejectsBadConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  Config
	}{
		{name: "empty"},
		{name: "relative", cfg: Config{UpstreamURL: "/mcp"}},
		{name: "ftp", cfg: Config{UpstreamURL: "ftp://example.test/mcp"}},
		{name: "nil middleware", cfg: Config{UpstreamURL: "http://example.test/mcp", Middleware: []Middleware{nil}}},
		{name: "nil http middleware", cfg: Config{UpstreamURL: "http://example.test/mcp", HTTPMiddleware: []HTTPMiddleware{nil}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := New(tc.cfg); err == nil {
				t.Fatal("New() error = nil")
			}
		})
	}
}

func TestRunCanceled(t *testing.T) {
	t.Parallel()

	p := mustProxy(t, Config{ListenAddr: "127.0.0.1:0", UpstreamURL: "http://127.0.0.1:9/mcp"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}

func TestRunShutdown(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}

	p := mustProxy(t, Config{ListenAddr: addr, UpstreamURL: "http://127.0.0.1:9/mcp"})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- p.Run(ctx) }()

	waitHealth(t, addr)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
}

func TestHealthzAndMethod(t *testing.T) {
	t.Parallel()

	gw, _ := gateway(t, scriptedUpstream(t))
	res, err := http.Get(gw.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body %s", res.StatusCode, body)
	}
	if string(body) != healthzBody {
		t.Fatalf("body = %q", body)
	}

	req, err := http.NewRequest(http.MethodPut, gw.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	put, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = readAll(t, put)
	if put.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("PUT status = %d", put.StatusCode)
	}
	if !strings.Contains(put.Header.Get("Allow"), "POST") {
		t.Fatalf("Allow = %q", put.Header.Get("Allow"))
	}
}

func TestHTTPMiddlewareSkipsHealthz(t *testing.T) {
	t.Parallel()

	var hits int
	gw, _ := gateway(t, scriptedUpstream(t), func(cfg *Config) {
		cfg.HTTPMiddleware = []HTTPMiddleware{
			func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits++
					next.ServeHTTP(w, r)
				})
			},
		}
	})

	res, err := http.Get(gw.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = readAll(t, res)
	if hits != 0 {
		t.Fatalf("healthz hits = %d, want 0", hits)
	}

	posted := postMCP(t, gw.URL+"/mcp", "", initializeBody)
	if posted.status != http.StatusOK {
		t.Fatalf("initialize status = %d, body %s", posted.status, posted.body)
	}
	if hits != 1 {
		t.Fatalf("mcp hits = %d, want 1", hits)
	}
}

func TestPassThroughKeepsBytesAndHidesUpstreamSession(t *testing.T) {
	t.Parallel()

	up := scriptedUpstream(t)
	gw, _ := gateway(t, up)

	const protocol = "2025-11-25"
	posted := postMCP(t, gw.URL+"/mcp", "", initializeBody, func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("Cookie", "sid=secret")
		req.Header.Set("X-Api-Key", "secret")
		req.Header.Set("Mcp-Protocol-Version", protocol)
	})
	if posted.status != http.StatusOK {
		t.Fatalf("status = %d, body %s", posted.status, posted.body)
	}

	up.mu.Lock()
	if len(up.requests) != 1 {
		t.Fatalf("upstream requests = %d", len(up.requests))
	}
	got := up.requests[0]
	up.mu.Unlock()

	if got.body != initializeBody {
		t.Fatalf("upstream body = %s, want the original message", got.body)
	}
	if string(posted.body) != up.initializeResult {
		t.Fatalf("client body = %s, want upstream body %s", posted.body, up.initializeResult)
	}
	if got.authorization != "" || got.cookie != "" || got.apiKey != "" {
		t.Fatalf("upstream saw client credentials: %+v", got)
	}
	if got.protocol != protocol {
		t.Fatalf("protocol header = %q", got.protocol)
	}
	if got.session != "" {
		t.Fatalf("initialize forwarded a session id %q", got.session)
	}

	downstream := posted.header.Get("Mcp-Session-Id")
	if downstream == "" || downstream == "upstream-session" {
		t.Fatalf("downstream session = %q", downstream)
	}

	next := postMCP(t, gw.URL+"/mcp", downstream, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	if next.status != http.StatusOK {
		t.Fatalf("tools/list status = %d, body %s", next.status, next.body)
	}
	if next.header.Get("Mcp-Session-Id") != downstream {
		t.Fatalf("session changed from %q to %q", downstream, next.header.Get("Mcp-Session-Id"))
	}

	up.mu.Lock()
	defer up.mu.Unlock()
	if len(up.requests) != 2 {
		t.Fatalf("upstream requests = %d", len(up.requests))
	}
	if up.requests[1].session != "upstream-session" {
		t.Fatalf("upstream session = %q, want upstream-session", up.requests[1].session)
	}
}

func TestUpstreamFailureIsJSONRPC(t *testing.T) {
	t.Parallel()

	gw, _ := gateway(t, nil, func(cfg *Config) {
		cfg.UpstreamURL = "http://127.0.0.1:1/mcp"
	})
	posted := postMCP(t, gw.URL+"/mcp", "", initializeBody)
	if posted.status != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a JSON-RPC error, body %s", posted.status, posted.body)
	}
	code, message := rpcError(t, posted.body)
	if code != CodeInternalError {
		t.Fatalf("code = %d, message %q", code, message)
	}
	if !strings.Contains(message, "upstream") {
		t.Fatalf("message = %q", message)
	}
}

func TestRejectsMalformedMessages(t *testing.T) {
	t.Parallel()

	gw, up := gateway(t, scriptedUpstream(t))
	cases := []struct {
		name    string
		body    string
		status  int
		code    int64
		message string
	}{
		{name: "parse", body: "{", status: http.StatusOK, code: CodeParseError, message: "parse error"},
		{name: "batch", body: `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, status: http.StatusOK, code: CodeInvalidRequest, message: "JSON-RPC batches are not supported"},
		{name: "invalid", body: `{"jsonrpc":"2.0"}`, status: http.StatusOK, code: CodeInvalidRequest, message: "invalid request"},
		{name: "content type", body: initializeBody, status: http.StatusUnsupportedMediaType},
		{name: "too large", body: `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"blob":"` + strings.Repeat("a", int(maxBodyBytes)) + `"}}`, status: http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, gw.URL+"/mcp", strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Accept", "application/json, text/event-stream")
			if tc.name != "content type" {
				req.Header.Set("Content-Type", "application/json")
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body := readAll(t, res)
			if res.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d, body %s", res.StatusCode, tc.status, body)
			}
			if tc.code == 0 {
				return
			}
			code, message := rpcError(t, body)
			if code != tc.code || message != tc.message {
				t.Fatalf("error = %d %q, want %d %q", code, message, tc.code, tc.message)
			}
		})
	}

	up.mu.Lock()
	defer up.mu.Unlock()
	if len(up.requests) != 0 {
		t.Fatalf("malformed messages reached upstream: %+v", up.requests)
	}
}

func TestUnknownSession(t *testing.T) {
	t.Parallel()

	gw, _ := gateway(t, scriptedUpstream(t))
	posted := postMCP(t, gw.URL+"/mcp", "missing", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if posted.status != http.StatusNotFound {
		t.Fatalf("status = %d, body %s", posted.status, posted.body)
	}
}

func TestNotificationIsAccepted(t *testing.T) {
	t.Parallel()

	gw, _ := gateway(t, scriptedUpstream(t))
	init := postMCP(t, gw.URL+"/mcp", "", initializeBody)
	note := postMCP(t, gw.URL+"/mcp", init.header.Get("Mcp-Session-Id"), `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if note.status != http.StatusAccepted {
		t.Fatalf("status = %d, body %s", note.status, note.body)
	}
	if len(bytes.TrimSpace(note.body)) != 0 {
		t.Fatalf("notification body = %s", note.body)
	}
}

func TestMiddlewareOrderRewriteAndShortCircuit(t *testing.T) {
	t.Parallel()

	var order []string
	up := scriptedUpstream(t)
	gw, _ := gateway(t, up, func(cfg *Config) {
		cfg.Middleware = []Middleware{
			func(next Handler) Handler {
				return func(ctx context.Context, call *Call) (*Response, error) {
					order = append(order, "outer-before")
					resp, err := next(ctx, call)
					order = append(order, "outer-after")
					return resp, err
				}
			},
			func(next Handler) Handler {
				return func(ctx context.Context, call *Call) (*Response, error) {
					order = append(order, "inner-before")
					if call.Tool == "delete_everything" {
						call.Decision = "deny"
						return JSONRPCError(call.ID, -32001, "tool not allowed"), nil
					}
					resp, err := next(ctx, call)
					if err != nil || resp == nil || call.Method != "tools/list" {
						return resp, err
					}
					resp.Body = []byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"echo"}]}}`)
					resp.Stream = nil
					return resp, nil
				}
			},
		}
	})

	init := postMCP(t, gw.URL+"/mcp", "", initializeBody)
	sid := init.header.Get("Mcp-Session-Id")
	listed := postMCP(t, gw.URL+"/mcp", sid, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	if string(listed.body) != `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"echo"}]}}` {
		t.Fatalf("rewritten list = %s", listed.body)
	}
	denied := postMCP(t, gw.URL+"/mcp", sid, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"delete_everything","arguments":{}}}`)
	code, message := rpcError(t, denied.body)
	if denied.status != http.StatusOK || code != -32001 || message != "tool not allowed" {
		t.Fatalf("denied = %d %d %q body %s", denied.status, code, message, denied.body)
	}

	up.mu.Lock()
	calls := 0
	for _, req := range up.requests {
		if strings.Contains(req.body, "delete_everything") {
			calls++
		}
	}
	up.mu.Unlock()
	if calls != 0 {
		t.Fatal("denied tools/call was forwarded")
	}

	// Each call enters the outer middleware, then the inner one.
	// delete_everything returns before the forwarder; the outer middleware
	// still observes the response.
	want := []string{
		"outer-before", "inner-before", "outer-after",
		"outer-before", "inner-before", "outer-after",
		"outer-before", "inner-before", "outer-after",
	}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestGETRelaysEventStream(t *testing.T) {
	t.Parallel()

	up := scriptedUpstream(t)
	gw, _ := gateway(t, up)
	init := postMCP(t, gw.URL+"/mcp", "", initializeBody)
	sid := init.header.Get("Mcp-Session-Id")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gw.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", sid)
	req.Header.Set("Last-Event-Id", "1")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
	}()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if mediaType(res.Header.Get("Content-Type")) != "text/event-stream" {
		t.Fatalf("content-type = %q", res.Header.Get("Content-Type"))
	}
	if res.Header.Get("Mcp-Session-Id") != sid {
		t.Fatalf("session header = %q", res.Header.Get("Mcp-Session-Id"))
	}
	buf := make([]byte, 32)
	n, err := res.Body.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if !bytes.Contains(buf[:n], []byte("data: hi")) {
		t.Fatalf("stream = %q", buf[:n])
	}

	up.mu.Lock()
	defer up.mu.Unlock()
	var get recorded
	for _, req := range up.requests {
		if req.method == http.MethodGet {
			get = req
		}
	}
	if get.session != "upstream-session" {
		t.Fatalf("upstream GET session = %q", get.session)
	}
	if get.lastEventID != "1" {
		t.Fatalf("Last-Event-Id = %q", get.lastEventID)
	}
}

func TestDELETEForwardsAndForgets(t *testing.T) {
	t.Parallel()

	up := scriptedUpstream(t)
	gw, _ := gateway(t, up)
	init := postMCP(t, gw.URL+"/mcp", "", initializeBody)
	sid := init.header.Get("Mcp-Session-Id")

	req, err := http.NewRequest(http.MethodDelete, gw.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Mcp-Session-Id", sid)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = readAll(t, res)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE status = %d", res.StatusCode)
	}

	again := postMCP(t, gw.URL+"/mcp", sid, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if again.status != http.StatusNotFound {
		t.Fatalf("after DELETE status = %d", again.status)
	}

	up.mu.Lock()
	defer up.mu.Unlock()
	var deleted bool
	for _, req := range up.requests {
		if req.method == http.MethodDelete && req.session == "upstream-session" {
			deleted = true
		}
	}
	if !deleted {
		t.Fatalf("upstream DELETE missing: %+v", up.requests)
	}
}

func TestPrincipalContext(t *testing.T) {
	t.Parallel()

	ctx := WithPrincipal(context.Background(), "demo")
	got, ok := PrincipalFromContext(ctx)
	if !ok || got != "demo" {
		t.Fatalf("principal = %q %v", got, ok)
	}
	if _, ok := PrincipalFromContext(context.Background()); ok {
		t.Fatal("missing principal reported present")
	}
}

type recorded struct {
	method        string
	session       string
	authorization string
	cookie        string
	apiKey        string
	protocol      string
	lastEventID   string
	body          string
}

type scripted struct {
	mu               sync.Mutex
	requests         []recorded
	initializeResult string
}

func scriptedUpstream(t *testing.T) *scripted {
	t.Helper()
	return &scripted{
		initializeResult: `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{},"serverInfo":{"name":"up","version":"0"}}}`,
	}
}

func (s *scripted) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.requests = append(s.requests, recorded{
		method:        r.Method,
		session:       r.Header.Get("Mcp-Session-Id"),
		authorization: r.Header.Get("Authorization"),
		cookie:        r.Header.Get("Cookie"),
		apiKey:        r.Header.Get("X-Api-Key"),
		protocol:      r.Header.Get("Mcp-Protocol-Version"),
		lastEventID:   r.Header.Get("Last-Event-Id"),
		body:          string(body),
	})
	s.mu.Unlock()

	switch r.Method {
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
		return
	case http.MethodGet:
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Mcp-Session-Id", "upstream-session")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: hi\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Mcp-Session-Id", "upstream-session")
	if !bytes.Contains(body, []byte(`"id"`)) {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if bytes.Contains(body, []byte(`"method":"initialize"`)) {
		_, _ = io.WriteString(w, s.initializeResult)
		return
	}
	_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"echo"},{"name":"delete_everything"}]}}`)
}

func gateway(t *testing.T, upstream http.Handler, opts ...func(*Config)) (*httptest.Server, *scripted) {
	t.Helper()

	var recorded *scripted
	var srv *httptest.Server
	if upstream != nil {
		srv = httptest.NewServer(upstream)
		t.Cleanup(srv.Close)
		if s, ok := upstream.(*scripted); ok {
			recorded = s
		}
	}

	cfg := Config{}
	if srv != nil {
		cfg.UpstreamURL = srv.URL + "/mcp"
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	p := mustProxy(t, cfg)
	gw := httptest.NewServer(p.Handler())
	t.Cleanup(gw.Close)
	return gw, recorded
}

func mustProxy(t *testing.T, cfg Config) *Proxy {
	t.Helper()
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return p
}

type posted struct {
	status int
	header http.Header
	body   []byte
}

func postMCP(t *testing.T, endpoint, sessionID, body string, mutate ...func(*http.Request)) posted {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	for _, fn := range mutate {
		fn(req)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	return posted{status: res.StatusCode, header: res.Header.Clone(), body: readAll(t, res)}
}

func readAll(t *testing.T, res *http.Response) []byte {
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
		Error struct {
			Code    int64  `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("decode error: %v body %s", err, body)
	}
	return msg.Error.Code, msg.Error.Message
}

func waitHealth(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	endpoint := "http://" + addr + "/healthz"
	for time.Now().Before(deadline) {
		res, err := http.Get(endpoint)
		if err == nil {
			_, _ = io.Copy(io.Discard, res.Body)
			if closeErr := res.Body.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			if res.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("healthz on %s was not ready", addr)
}
