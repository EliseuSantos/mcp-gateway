package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// Handler handles one JSON-RPC message.
//
// next is the upstream forwarder, or the next middleware inward. A handler
// may return a response without calling next; that is how a disallowed
// tools/call is rejected before the upstream is contacted.
type Handler func(ctx context.Context, call *Call) (*Response, error)

// Middleware wraps a [Handler]. Slice order is outermost first: the first
// middleware sees the call before the others and sees the response after them.
type Middleware func(next Handler) Handler

// HTTPMiddleware wraps the Streamable HTTP handler on /mcp.
//
// Authentication belongs here, before the body is parsed. /healthz is not
// wrapped, so a liveness probe does not need a credential.
type HTTPMiddleware func(http.Handler) http.Handler

// Call is one downstream JSON-RPC message.
type Call struct {
	// Body is the raw message. The forwarder sends it unchanged.
	// Middleware may replace it before calling next; the replacement is what
	// the upstream receives.
	Body []byte

	// Method is empty when the message is a JSON-RPC response (a client
	// reply to a server-initiated request) rather than a request.
	Method string

	// ID is the raw JSON-RPC id. Nil means the field was absent.
	ID json.RawMessage

	// Params is the raw params object, if any.
	Params json.RawMessage

	// Tool is set for tools/call when params.name is a string.
	Tool string

	// Session is the downstream session. It is nil on the first request of
	// a session, which is the request that elicits the upstream session id.
	Session *Session

	// Header is a clone of the incoming HTTP headers.
	Header http.Header

	// Decision is an annotation for later middleware. The proxy does not
	// branch on it. Policy sets it; audit reads it.
	Decision string
}

// Response is what the gateway writes to the client.
type Response struct {
	// Status is the HTTP status written to the client. Zero means 200.
	Status int

	// UpstreamStatus is the HTTP status from the upstream. Zero means the
	// upstream was not called, or the call failed before a response.
	UpstreamStatus int

	// Header is copied onto the client response. Mcp-Session-Id, if it
	// carries the upstream id, is replaced with the downstream id before
	// the response is written.
	Header http.Header

	// Body is the buffered payload. It is ignored when Stream is set.
	Body []byte

	// Stream is a text/event-stream body. The caller owns it and must close
	// it. Middleware that rewrites Body must set Stream to nil.
	Stream io.ReadCloser
}

type principalKey struct{}

// WithPrincipal returns a context that carries the authenticated client id.
// Auth middleware sets it. Policy and audit read it with [PrincipalFromContext].
func WithPrincipal(ctx context.Context, clientID string) context.Context {
	return context.WithValue(ctx, principalKey{}, clientID)
}

// PrincipalFromContext reports the client id set by [WithPrincipal].
func PrincipalFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(principalKey{}).(string)
	return id, ok
}

// JSONRPCError builds a JSON-RPC error response on HTTP 200.
//
// HTTP 200 is intentional. A synthesized failure has no upstream response to
// relay, and a non-2xx status is a transport error: the official client
// treats 502 as transient and may not surface the body. An error object on
// 200 is a completed call. id may be nil, in which case the id is JSON null.
func JSONRPCError(id json.RawMessage, code int64, message string) *Response {
	if len(id) == 0 {
		id = []byte("null")
	}
	payload := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   struct {
			Code    int64  `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{
		JSONRPC: "2.0",
		ID:      id,
	}
	payload.Error.Code = code
	payload.Error.Message = message

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(&payload); err != nil {
		buf.Reset()
		buf.WriteString(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"upstream request failed"}}`)
		buf.WriteByte('\n')
	}
	return &Response{
		Status: http.StatusOK,
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Body:   bytes.TrimRight(buf.Bytes(), "\n"),
	}
}

func chain(final Handler, middleware []Middleware) Handler {
	h := final
	for i := len(middleware) - 1; i >= 0; i-- {
		h = middleware[i](h)
	}
	return h
}

// CodeParseError, CodeInvalidRequest, and CodeInternalError are the JSON-RPC
// codes this proxy synthesizes. Upstream errors that arrive as a response are
// relayed with whatever code the upstream chose.
const (
	CodeParseError     int64 = jsonrpc.CodeParseError
	CodeInvalidRequest int64 = jsonrpc.CodeInvalidRequest
	CodeInternalError  int64 = jsonrpc.CodeInternalError
)
