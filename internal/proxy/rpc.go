package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

type statusError struct {
	code    int64
	message string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("jsonrpc %d: %s", e.code, e.message)
}

func parseCall(body []byte) (*Call, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || !json.Valid(trimmed) {
		return nil, &statusError{code: CodeParseError, message: "parse error"}
	}
	// A batch is one HTTP request carrying several calls. The allowlist
	// decides per message, so a batch could hide a denied tools/call next
	// to an allowed one. Reject the shape until we have a reason to fan out.
	if trimmed[0] == '[' {
		return nil, &statusError{code: CodeInvalidRequest, message: "JSON-RPC batches are not supported"}
	}

	msg, err := jsonrpc.DecodeMessage(trimmed)
	if err != nil {
		return nil, &statusError{code: CodeInvalidRequest, message: "invalid request"}
	}

	call := &Call{Body: trimmed, ID: rawID(trimmed)}
	switch msg := msg.(type) {
	case *jsonrpc.Request:
		call.Method = msg.Method
		call.Params = msg.Params
		if msg.Method == "tools/call" {
			call.Tool = toolName(msg.Params)
		}
	case *jsonrpc.Response:
	default:
		return nil, &statusError{code: CodeInvalidRequest, message: "invalid request"}
	}
	return call, nil
}

func rawID(body []byte) json.RawMessage {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil
	}
	id, ok := envelope["id"]
	if !ok || len(id) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), id...)
}

func toolName(params json.RawMessage) string {
	if len(params) == 0 {
		return ""
	}
	var payload struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(params, &payload); err != nil {
		return ""
	}
	return payload.Name
}
