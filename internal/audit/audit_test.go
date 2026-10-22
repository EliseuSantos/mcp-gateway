package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/EliseuSantos/mcp-gateway/internal/proxy"
)

func TestRedactArguments(t *testing.T) {
	t.Parallel()

	allow := map[string]struct{}{"message": {}}
	cases := []struct {
		name   string
		params string
		allow  map[string]struct{}
		want   string
	}{
		{
			name:   "allowlisted field kept, other redacted",
			params: `{"name":"echo","arguments":{"token":"secret","message":"a & b"}}`,
			allow:  allow,
			want:   `{"message":"a & b","token":"[redacted]"}`,
		},
		{
			name:   "empty allowlist redacts every value",
			params: `{"name":"echo","arguments":{"message":"ping"}}`,
			want:   `{"message":"[redacted]"}`,
		},
		{
			name:   "no arguments omits the field",
			params: `{"name":"delete_everything"}`,
			allow:  allow,
			want:   "",
		},
		{
			name:   "null arguments omits the field",
			params: `{"name":"echo","arguments":null}`,
			allow:  allow,
			want:   "",
		},
		{
			name:   "empty object stays empty",
			params: `{"name":"echo","arguments":{}}`,
			allow:  allow,
			want:   `{}`,
		},
		{
			name:   "non object is fully redacted",
			params: `{"name":"echo","arguments":["secret"]}`,
			allow:  allow,
			want:   `"[redacted]"`,
		},
		{
			name:   "unparseable params are fully redacted",
			params: `not-json`,
			allow:  allow,
			want:   `"[redacted]"`,
		},
		{
			name:   "number stays a number",
			params: `{"name":"echo","arguments":{"message":"ping","n":3}}`,
			allow:  map[string]struct{}{"n": {}},
			want:   `{"message":"[redacted]","n":3}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := string(redactArguments(json.RawMessage(tc.params), tc.allow))
			if got != tc.want {
				t.Fatalf("redact = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestMiddlewareRecordsDenyWithoutSecret(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	w, err := New(&buf, []string{"message"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "super-secret-value"
	handler := w.Middleware()(func(context.Context, *proxy.Call) (*proxy.Response, error) {
		return &proxy.Response{}, nil
	})
	ctx := proxy.WithPrincipal(context.Background(), "demo")
	call := &proxy.Call{
		Method:   "tools/call",
		Tool:     "delete_everything",
		Decision: "deny",
		Params:   json.RawMessage(`{"name":"delete_everything","arguments":{"message":"ping","token":"` + secret + `"}}`),
	}
	resp, err := handler(ctx, call)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("response is nil")
	}

	line := strings.TrimSpace(buf.String())
	if strings.Contains(line, secret) {
		t.Fatalf("audit line contains the secret: %s", line)
	}
	var event Event
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		t.Fatal(err)
	}
	if event.Client != "demo" || event.Tool != "delete_everything" || event.Decision != "deny" || event.UpstreamStatus != 0 {
		t.Fatalf("event = %+v", event)
	}
	if event.LatencyMS < 0 {
		t.Fatalf("latency = %v", event.LatencyMS)
	}
	if string(event.Arguments) != `{"message":"ping","token":"[redacted]"}` {
		t.Fatalf("arguments = %s", event.Arguments)
	}
}

func TestMiddlewareRecordsUpstreamStatus(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	w, err := New(&buf, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := w.Middleware()(func(_ context.Context, call *proxy.Call) (*proxy.Response, error) {
		call.Decision = "allow"
		return &proxy.Response{UpstreamStatus: 200}, nil
	})
	if _, err := handler(context.Background(), &proxy.Call{Method: "initialize"}); err != nil {
		t.Fatal(err)
	}
	var event Event
	if err := json.Unmarshal(buf.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event.Decision != "allow" || event.UpstreamStatus != 200 || event.Client != "" || len(event.Arguments) != 0 {
		t.Fatalf("event = %+v args=%s", event, event.Arguments)
	}
}

func TestLogLinesDoNotInterleave(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	w, err := New(&buf, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			if err := w.Log(context.Background(), Event{Method: "tools/call", Tool: "echo", Decision: "allow"}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 32 {
		t.Fatalf("lines = %d", len(lines))
	}
	for _, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Fatalf("broken line: %s", line)
		}
	}
}

func TestNewRejectsBadFields(t *testing.T) {
	t.Parallel()

	if _, err := New(nil, []string{"message", "message"}, nil); err == nil {
		t.Fatal("duplicate field error = nil")
	}
	if _, err := New(nil, []string{" "}, nil); err == nil {
		t.Fatal("empty field error = nil")
	}
}
