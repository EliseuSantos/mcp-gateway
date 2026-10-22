// Package policy is the per-client tool allowlist.
//
// The list is deny-by-default. A client may see and call a tool only when
// the YAML names that tool for their client id. [Policy.Middleware] filters
// tools/list and rejects tools/call before the upstream forward.
package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/EliseuSantos/mcp-gateway/internal/proxy"
)

// CodeToolNotAllowed is the JSON-RPC code for a tools/call the allowlist
// rejected. The HTTP status stays 200 so the MCP client surfaces the error
// object instead of treating the response as a transport failure.
const CodeToolNotAllowed int64 = -32001

// Decision is the result of a tools/call check.
type Decision struct {
	Allowed bool
	Reason  string
}

// Allowlist decides which tools a client may see and call.
type Allowlist interface {
	VisibleTools(clientID string, tools []string) ([]string, error)
	AllowCall(clientID, tool string) (Decision, error)
}

// Policy is a static allowlist.
type Policy struct {
	clients map[string]map[string]struct{}
}

// New copies clients. A client with an empty tool list is present and may
// call nothing. A client id that is absent is also denied.
func New(clients map[string][]string) (*Policy, error) {
	copied := make(map[string]map[string]struct{}, len(clients))
	for id, tools := range clients {
		if strings.TrimSpace(id) == "" {
			return nil, errors.New("policy: client id is empty")
		}
		set := make(map[string]struct{}, len(tools))
		for _, tool := range tools {
			if strings.TrimSpace(tool) == "" {
				return nil, fmt.Errorf("policy: client %s has an empty tool name", id)
			}
			if _, ok := set[tool]; ok {
				return nil, fmt.Errorf("policy: client %s lists %s more than once", id, tool)
			}
			set[tool] = struct{}{}
		}
		copied[id] = set
	}
	return &Policy{clients: copied}, nil
}

// VisibleTools returns tools the client may see, in the input order.
func (p *Policy) VisibleTools(clientID string, tools []string) ([]string, error) {
	if p == nil {
		return nil, errors.New("policy: nil allowlist")
	}
	allowed := p.clients[clientID]
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		if _, ok := allowed[tool]; ok {
			out = append(out, tool)
		}
	}
	return out, nil
}

// AllowCall reports whether clientID may call tool.
func (p *Policy) AllowCall(clientID, tool string) (Decision, error) {
	if p == nil {
		return Decision{}, errors.New("policy: nil allowlist")
	}
	if _, ok := p.clients[clientID][tool]; ok && tool != "" {
		return Decision{Allowed: true, Reason: "allowed"}, nil
	}
	return Decision{Allowed: false, Reason: fmt.Sprintf("tool %q is not allowed", tool)}, nil
}

// Middleware filters tools/list and rejects a disallowed tools/call.
// Other methods are forwarded. The client id comes from [proxy.PrincipalFromContext].
func (p *Policy) Middleware() proxy.Middleware {
	return func(next proxy.Handler) proxy.Handler {
		return func(ctx context.Context, call *proxy.Call) (*proxy.Response, error) {
			clientID, _ := proxy.PrincipalFromContext(ctx)
			if call.Method == "tools/call" {
				decision, err := p.AllowCall(clientID, call.Tool)
				if err != nil {
					return nil, err
				}
				if !decision.Allowed {
					call.Decision = "deny"
					return proxy.JSONRPCError(call.ID, CodeToolNotAllowed, decision.Reason), nil
				}
				call.Decision = "allow"
			}
			resp, err := next(ctx, call)
			if err != nil || call.Method != "tools/list" || resp == nil {
				return resp, err
			}
			if resp.Stream != nil {
				_ = resp.Stream.Close()
				resp.Stream = nil
				call.Decision = "deny"
				return proxy.JSONRPCError(call.ID, proxy.CodeInternalError, "tools/list could not be filtered"), nil
			}
			if resp.UpstreamStatus >= 300 || (resp.UpstreamStatus != 0 && resp.UpstreamStatus < 200) {
				return resp, nil
			}
			filtered, err := filterToolsList(resp.Body, func(name string) bool {
				decision, err := p.AllowCall(clientID, name)
				return err == nil && decision.Allowed
			})
			if err != nil {
				call.Decision = "deny"
				return proxy.JSONRPCError(call.ID, proxy.CodeInternalError, "tools/list could not be filtered"), nil
			}
			resp.Body = filtered
			call.Decision = "allow"
			return resp, nil
		}
	}
}

func filterToolsList(body []byte, allow func(name string) bool) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	if raw, ok := envelope["error"]; ok && string(raw) != "null" {
		return body, nil
	}
	rawResult, ok := envelope["result"]
	if !ok {
		return nil, errors.New("policy: tools/list has no result")
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(rawResult, &result); err != nil {
		return nil, err
	}
	rawTools, ok := result["tools"]
	if !ok {
		return nil, errors.New("policy: tools/list has no tools")
	}
	var tools []json.RawMessage
	if err := json.Unmarshal(rawTools, &tools); err != nil {
		return nil, err
	}
	kept := make([]json.RawMessage, 0, len(tools))
	for _, raw := range tools {
		var tool struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &tool); err != nil {
			return nil, err
		}
		if allow(tool.Name) {
			kept = append(kept, raw)
		}
	}
	encoded, err := marshalRaw(kept)
	if err != nil {
		return nil, err
	}
	result["tools"] = encoded
	resultBytes, err := marshalRaw(result)
	if err != nil {
		return nil, err
	}
	envelope["result"] = resultBytes
	return marshalRaw(envelope)
}

// marshalRaw encodes without HTML escaping. The default encoder turns "&"
// into "\u0026" even inside a json.RawMessage, which would rewrite a tool
// description the gateway did not mean to edit.
func marshalRaw(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
