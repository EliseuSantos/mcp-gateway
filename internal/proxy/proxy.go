// Package proxy is a Streamable HTTP MCP gateway.
//
// It terminates the downstream session and opens a separate upstream
// session. JSON-RPC messages are forwarded unchanged unless middleware
// short-circuits or rewrites them. ADR-001 records why this is not a byte
// relay: the upstream session id must not become a credential the client
// holds, and auth, the tool allowlist, and the audit log all need a
// message boundary.
//
// Two hook points are intentional:
//
//   - [HTTPMiddleware] runs on /mcp before the body is parsed.
//     Authentication belongs here. /healthz is left outside that wrapper.
//   - [Middleware] runs per JSON-RPC message, outermost first, around the
//     upstream forward. The allowlist belongs here (reject tools/call, rewrite
//     a buffered tools/list). The audit log belongs here too, around the
//     forward, where the decision, latency, and upstream status are visible.
//
// text/event-stream bodies are streamed and are not rewritten. The example
// server answers POST with application/json, which is the response a later
// allowlist filters.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"
)

const (
	defaultPath          = "/mcp"
	defaultHeaderTimeout = 60 * time.Second
	shutdownTimeout      = 5 * time.Second
	sessionHeader        = "Mcp-Session-Id"
	healthzBody          = "{\"status\":\"ok\"}\n"
)

// Config configures a [Proxy].
type Config struct {
	// ListenAddr is the downstream address, for example ":8080".
	// Required by [Proxy.Run]. [Proxy.Handler] does not use it.
	ListenAddr string

	// UpstreamURL is the upstream Streamable HTTP endpoint,
	// for example "http://127.0.0.1:8081/mcp".
	UpstreamURL string

	// Path is the downstream MCP path. Empty means "/mcp".
	Path string

	// Logger receives request logs. Nil discards them.
	Logger *slog.Logger

	// Client is the upstream HTTP client. Nil uses a client that does not
	// add Accept-Encoding, so a buffered JSON body stays the bytes the
	// upstream sent. Response headers must arrive within 60s. The body
	// itself is not on a deadline, so a long-lived event stream survives.
	Client *http.Client

	// Middleware is the JSON-RPC chain, outermost first.
	Middleware []Middleware

	// HTTPMiddleware wraps /mcp only, outermost first.
	HTTPMiddleware []HTTPMiddleware

	// Metrics is served at /metrics. It is not wrapped by HTTPMiddleware,
	// so a scrape does not need a credential. Nil leaves the route unmounted.
	Metrics http.Handler
}

// Proxy is the pass-through gateway.
type Proxy struct {
	listen   string
	upstream string
	path     string
	logger   *slog.Logger
	client   *http.Client
	sessions *sessionStore
	handle   Handler
	handler  http.Handler
}

// New validates cfg and builds the middleware chain.
func New(cfg Config) (*Proxy, error) {
	parsed, err := url.Parse(cfg.UpstreamURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("proxy: upstream URL %q is not an absolute http(s) URL", cfg.UpstreamURL)
	}
	for _, mw := range cfg.Middleware {
		if mw == nil {
			return nil, errors.New("proxy: nil middleware")
		}
	}
	for _, mw := range cfg.HTTPMiddleware {
		if mw == nil {
			return nil, errors.New("proxy: nil HTTP middleware")
		}
	}

	path := cfg.Path
	if path == "" {
		path = defaultPath
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Transport: newTransport()}
	}

	p := &Proxy{
		listen:   cfg.ListenAddr,
		upstream: cfg.UpstreamURL,
		path:     path,
		logger:   logger,
		client:   client,
		sessions: newSessionStore(),
	}
	p.handle = chain(p.forwardPOST, cfg.Middleware)
	p.handler = p.routes(cfg.HTTPMiddleware, cfg.Metrics)
	return p, nil
}

// Handler is the downstream HTTP handler. MCP traffic is on [Config.Path];
// liveness is on /healthz. The handler is safe for concurrent use.
func (p *Proxy) Handler() http.Handler { return p.handler }

// Run serves until ctx is canceled, then drains in-flight requests and
// best-effort closes upstream sessions.
func (p *Proxy) Run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.listen == "" {
		return errors.New("proxy: listen address is required")
	}

	srv := &http.Server{
		Addr:              p.listen,
		Handler:           p.handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		p.logger.Info("listening", "addr", p.listen, "path", p.path, "upstream", p.upstream)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		p.closeUpstreamSessions(shutdownCtx)
		return ctx.Err()
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func (p *Proxy) routes(middleware []HTTPMiddleware, metrics http.Handler) http.Handler {
	var mcp http.Handler = http.HandlerFunc(p.serveMCP)
	for i := len(middleware) - 1; i >= 0; i-- {
		mcp = middleware[i](mcp)
	}

	mux := http.NewServeMux()
	mux.Handle(p.path, mcp)
	mux.HandleFunc("/healthz", healthz)
	if metrics != nil {
		mux.Handle("/metrics", metrics)
	}
	return mux
}

// UpstreamTransport is the HTTP transport used for upstream MCP calls.
// Compression is disabled so a buffered JSON body stays the bytes the
// upstream sent. The composition root may wrap the result.
func UpstreamTransport() *http.Transport { return newTransport() }

func (p *Proxy) serveMCP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		p.servePOST(w, r)
	case http.MethodGet:
		p.serveGET(w, r)
	case http.MethodDelete:
		p.serveDELETE(w, r)
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (p *Proxy) servePOST(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	if mediaType(r.Header.Get("Content-Type")) != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	if !accepts(r.Header, "application/json") || !accepts(r.Header, "text/event-stream") {
		http.Error(w, "Accept must contain application/json and text/event-stream", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	if int64(len(body)) > maxBodyBytes {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	call, err := parseCall(body)
	if err != nil {
		var status *statusError
		if errors.As(err, &status) && status.code != 0 {
			writeResponse(w, JSONRPCError(nil, status.code, status.message))
			return
		}
		writeResponse(w, JSONRPCError(nil, CodeParseError, "parse error"))
		return
	}
	call.Header = r.Header.Clone()

	if sid := r.Header.Get(sessionHeader); sid != "" {
		sess, ok := p.sessions.get(sid)
		if !ok {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		call.Session = sess
	}

	resp, err := p.handle(r.Context(), call)
	if err != nil {
		p.logger.Log(r.Context(), slog.LevelWarn, "middleware", "method", call.Method, "err", err)
		closeStream(resp)
		resp = JSONRPCError(call.ID, CodeInternalError, "upstream request failed")
	}
	if resp == nil {
		resp = JSONRPCError(call.ID, CodeInternalError, "upstream request failed")
	}
	if err := p.bindSession(call, resp); err != nil {
		p.logger.Log(r.Context(), slog.LevelWarn, "session", "err", err)
		closeStream(resp)
		writeResponse(w, JSONRPCError(call.ID, CodeInternalError, "upstream request failed"))
		return
	}

	attrs := []any{
		"method", call.Method,
		"status", statusOrOK(resp.Status),
		"upstream_status", resp.UpstreamStatus,
		"duration", time.Since(started),
	}
	if call.Tool != "" {
		attrs = append(attrs, "tool", call.Tool)
	}
	if id := sessionID(call.Session); id != "" {
		attrs = append(attrs, "session", id)
	}
	p.logger.Log(r.Context(), slog.LevelInfo, "rpc", attrs...)
	writeResponse(w, resp)
}

func (p *Proxy) serveGET(w http.ResponseWriter, r *http.Request) {
	if !accepts(r.Header, "text/event-stream") {
		http.Error(w, "Accept must contain text/event-stream", http.StatusBadRequest)
		return
	}
	sess, ok := p.lookup(w, r)
	if !ok {
		return
	}
	resp, err := p.openUpstreamStream(r.Context(), http.MethodGet, sess, r.Header)
	if err != nil {
		p.logger.Log(r.Context(), slog.LevelWarn, "upstream get", "err", err)
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer closeBody(resp.Body)

	copyRelayHeaders(w.Header(), resp.Header)
	w.Header().Set(sessionHeader, sess.id)
	w.WriteHeader(resp.StatusCode)
	if mediaType(resp.Header.Get("Content-Type")) == "text/event-stream" {
		streamCopy(w, resp.Body)
		return
	}
	_, _ = io.Copy(w, io.LimitReader(resp.Body, maxBodyBytes))
}

func (p *Proxy) serveDELETE(w http.ResponseWriter, r *http.Request) {
	sid := r.Header.Get(sessionHeader)
	if sid == "" {
		http.Error(w, "Bad Request: DELETE requires an Mcp-Session-Id header", http.StatusBadRequest)
		return
	}
	sess, ok := p.sessions.delete(sid)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	status := p.deleteUpstream(r.Context(), sess)
	if status == 0 {
		status = http.StatusNoContent
	}
	w.WriteHeader(status)
}

func (p *Proxy) lookup(w http.ResponseWriter, r *http.Request) (*Session, bool) {
	sid := r.Header.Get(sessionHeader)
	if sid == "" {
		http.Error(w, "Bad Request: GET requires an Mcp-Session-Id header", http.StatusBadRequest)
		return nil, false
	}
	sess, ok := p.sessions.get(sid)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return nil, false
	}
	return sess, true
}

// bindSession publishes a downstream session the first time the upstream
// assigns one, and always strips the upstream id from the client response.
func (p *Proxy) bindSession(call *Call, resp *Response) error {
	if resp.Header == nil {
		resp.Header = make(http.Header)
	}
	upstreamID := resp.Header.Get(sessionHeader)
	resp.Header.Del(sessionHeader)

	sess := call.Session
	if sess == nil && upstreamID != "" {
		created, err := p.sessions.create(upstreamID)
		if err != nil {
			return err
		}
		call.Session = created
		sess = created
	}
	if sess != nil {
		resp.Header.Set(sessionHeader, sess.id)
	}
	return nil
}

func (p *Proxy) deleteUpstream(ctx context.Context, sess *Session) int {
	if sess == nil || sess.upstreamID == "" {
		return http.StatusNoContent
	}
	resp, err := p.openUpstreamStream(ctx, http.MethodDelete, sess, nil)
	if err != nil {
		p.logger.Log(ctx, slog.LevelWarn, "upstream delete", "err", err)
		return http.StatusNoContent
	}
	defer closeBody(resp.Body)
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))
	if resp.StatusCode == 0 {
		return http.StatusNoContent
	}
	return resp.StatusCode
}

func (p *Proxy) closeUpstreamSessions(ctx context.Context) {
	for _, sess := range p.sessions.list() {
		p.sessions.delete(sess.id)
		p.deleteUpstream(ctx, sess)
	}
	if tr, ok := p.client.Transport.(interface{ CloseIdleConnections() }); ok {
		tr.CloseIdleConnections()
	}
}

func newTransport() *http.Transport {
	var tr *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		tr = base.Clone()
	} else {
		tr = &http.Transport{Proxy: http.ProxyFromEnvironment}
	}
	tr.DisableCompression = true
	tr.ResponseHeaderTimeout = defaultHeaderTimeout
	tr.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	return tr
}

func healthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, healthzBody)
}

func writeResponse(w http.ResponseWriter, resp *Response) {
	if resp.Stream != nil {
		defer closeBody(resp.Stream)
	}
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	status := statusOrOK(resp.Status)
	w.WriteHeader(status)
	if resp.Stream != nil {
		streamCopy(w, resp.Stream)
		return
	}
	if len(resp.Body) > 0 {
		_, _ = w.Write(resp.Body)
	}
}

func streamCopy(w http.ResponseWriter, src io.Reader) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

func copyRelayHeaders(dst, src http.Header) {
	for key, values := range relayHeaders(src) {
		if key == sessionHeader {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func closeStream(resp *Response) {
	if resp != nil && resp.Stream != nil {
		closeBody(resp.Stream)
		resp.Stream = nil
	}
}

func closeBody(c io.Closer) {
	if c == nil {
		return
	}
	_ = c.Close()
}

func statusOrOK(status int) int {
	if status == 0 {
		return http.StatusOK
	}
	return status
}

func sessionID(sess *Session) string {
	if sess == nil {
		return ""
	}
	return sess.id
}
