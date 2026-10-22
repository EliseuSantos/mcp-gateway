package proxy

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
)

const maxBodyBytes = 4 << 20 // 4 MiB, same order of magnitude as the SDK default

// forwardPOST is the innermost handler. It sends call.Body to the upstream
// and relays the response. Middleware runs outside it.
func (p *Proxy) forwardPOST(ctx context.Context, call *Call) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.upstream, bytes.NewReader(call.Body))
	if err != nil {
		return JSONRPCError(call.ID, CodeInternalError, "upstream request failed"), nil
	}
	req.Header = upstreamHeaders(call.Header, sessionUpstreamID(call.Session))
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json, text/event-stream")
	}

	resp, err := p.client.Do(req)
	if err != nil {
		p.logger.Log(ctx, slog.LevelWarn, "upstream post", "method", call.Method, "err", err)
		return JSONRPCError(call.ID, CodeInternalError, "upstream request failed"), nil
	}

	out := &Response{
		Status:         resp.StatusCode,
		UpstreamStatus: resp.StatusCode,
		Header:         relayHeaders(resp.Header),
	}
	if mediaType(resp.Header.Get("Content-Type")) == "text/event-stream" {
		// Streamed responses are not buffered, so middleware cannot rewrite
		// them. The example server answers POST with application/json.
		out.Stream = resp.Body
		return out, nil
	}

	defer closeBody(resp.Body)
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		p.logger.Log(ctx, slog.LevelWarn, "upstream read", "method", call.Method, "err", err)
		return JSONRPCError(call.ID, CodeInternalError, "upstream request failed"), nil
	}
	if int64(len(body)) > maxBodyBytes {
		return JSONRPCError(call.ID, CodeInternalError, "upstream response too large"), nil
	}
	out.Body = body
	return out, nil
}

func (p *Proxy) openUpstreamStream(ctx context.Context, method string, sess *Session, in http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, p.upstream, nil)
	if err != nil {
		return nil, err
	}
	req.Header = upstreamHeaders(in, sessionUpstreamID(sess))
	return p.client.Do(req)
}

func sessionUpstreamID(sess *Session) string {
	if sess == nil {
		return ""
	}
	return sess.upstreamID
}

// upstreamHeaders copies the small allowlist the upstream needs.
// Authorization and Cookie stay on the gateway: client credentials are not
// an upstream credential. Mcp-Session-Id is replaced, not forwarded, so the
// client never holds the upstream session.
func upstreamHeaders(src http.Header, upstreamSession string) http.Header {
	dst := make(http.Header)
	for key, values := range src {
		if key == "Mcp-Session-Id" || !forwardHeader(key) {
			continue
		}
		dst[key] = append([]string(nil), values...)
	}
	if upstreamSession != "" {
		dst.Set("Mcp-Session-Id", upstreamSession)
	}
	return dst
}

func forwardHeader(canonical string) bool {
	switch canonical {
	case "Accept", "Content-Type", "Last-Event-Id":
		return true
	default:
		return strings.HasPrefix(canonical, "Mcp-")
	}
}

func relayHeaders(src http.Header) http.Header {
	dst := make(http.Header)
	for _, key := range []string{"Content-Type", "Cache-Control", "Mcp-Protocol-Version", "Mcp-Session-Id"} {
		if values := src.Values(key); len(values) > 0 {
			dst[key] = append([]string(nil), values...)
		}
	}
	return dst
}

func mediaType(value string) string {
	mt, _, err := mime.ParseMediaType(value)
	if err != nil {
		return ""
	}
	return mt
}

func accepts(header http.Header, media string) bool {
	for _, value := range header.Values("Accept") {
		for part := range strings.SplitSeq(value, ",") {
			if mediaType(strings.TrimSpace(part)) == media {
				return true
			}
		}
	}
	return false
}
