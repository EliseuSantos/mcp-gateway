// Package audit writes one JSON object per JSON-RPC request.
//
// The object goes to stdout (or whatever writer the process passed in).
// Argument values are the string "[redacted]" unless the field name is on
// the allowlist. ADR-003 records what is kept and what is left out.
package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EliseuSantos/mcp-gateway/internal/proxy"
)

const redacted = `"[redacted]"`

// Logger records one audit event.
type Logger interface {
	Log(ctx context.Context, event Event) error
}

// Event is one audit record. LatencyMS is milliseconds with three decimal places.
type Event struct {
	Time           time.Time       `json:"time"`
	Client         string          `json:"client,omitempty"`
	Method         string          `json:"method"`
	Tool           string          `json:"tool,omitempty"`
	Decision       string          `json:"decision"`
	LatencyMS      millis          `json:"latency_ms"`
	UpstreamStatus int             `json:"upstream_status"`
	Arguments      json.RawMessage `json:"arguments,omitempty"`
}

// millis renders a duration as a JSON number with three decimal places.
type millis float64

func (m millis) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("%.3f", float64(m))), nil
}

// Writer is a JSON line logger.
type Writer struct {
	out    io.Writer
	allow  map[string]struct{}
	logger *slog.Logger
	mu     sync.Mutex
}

// New writes audit lines to out. fields are the argument names whose values
// are copied into the line. Nil out discards. A nil logger discards write errors.
func New(out io.Writer, fields []string, logger *slog.Logger) (*Writer, error) {
	if out == nil {
		out = io.Discard
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	allow := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if strings.TrimSpace(field) == "" {
			return nil, errors.New("audit: argument field name is empty")
		}
		if _, ok := allow[field]; ok {
			return nil, fmt.Errorf("audit: argument field %s is listed more than once", field)
		}
		allow[field] = struct{}{}
	}
	return &Writer{out: out, allow: allow, logger: logger}, nil
}

// Log writes event as one JSON object followed by a newline.
// The write is serialized so two lines cannot interleave.
func (w *Writer) Log(_ context.Context, event Event) error {
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	enc := json.NewEncoder(w.out)
	enc.SetEscapeHTML(false)
	return enc.Encode(event)
}

// Middleware records the call after next returns. A write error is logged
// and does not fail the RPC. The decision annotation left by an inner
// middleware is kept; an empty decision becomes "error" or "forward".
func (w *Writer) Middleware() proxy.Middleware {
	return func(next proxy.Handler) proxy.Handler {
		return func(ctx context.Context, call *proxy.Call) (*proxy.Response, error) {
			started := time.Now()
			resp, err := next(ctx, call)
			decision := call.Decision
			if decision == "" {
				if err != nil {
					decision = "error"
				} else {
					decision = "forward"
				}
				call.Decision = decision
			}
			event := Event{
				Time:      time.Now().UTC(),
				Method:    call.Method,
				Tool:      call.Tool,
				Decision:  decision,
				LatencyMS: millis(float64(time.Since(started).Microseconds()) / 1000),
			}
			if id, ok := proxy.PrincipalFromContext(ctx); ok && id != "" {
				event.Client = id
			}
			if resp != nil {
				event.UpstreamStatus = resp.UpstreamStatus
			}
			if call.Method == "tools/call" {
				event.Arguments = redactArguments(call.Params, w.allow)
			}
			if logErr := w.Log(ctx, event); logErr != nil {
				w.logger.Error("audit", "err", logErr, "method", call.Method, "tool", call.Tool)
			}
			return resp, err
		}
	}
}

// redactArguments returns the arguments object with values replaced.
// A nil result means there were no arguments and the field should be omitted.
// Anything that is not a JSON object is the string "[redacted]".
func redactArguments(params json.RawMessage, allow map[string]struct{}) json.RawMessage {
	if len(bytes.TrimSpace(params)) == 0 {
		return nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(params, &envelope); err != nil {
		return json.RawMessage(redacted)
	}
	raw, ok := envelope["arguments"]
	if !ok || len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return json.RawMessage(redacted)
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		encoded, err := marshalString(key)
		if err != nil {
			return json.RawMessage(redacted)
		}
		buf.Write(encoded)
		buf.WriteByte(':')
		if _, ok := allow[key]; ok {
			buf.Write(bytes.TrimSpace(fields[key]))
			continue
		}
		buf.WriteString(redacted)
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

func marshalString(s string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
