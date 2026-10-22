package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	serverName    = "echo-server"
	serverVersion = "0.1.0"

	toolEcho             = "echo"
	toolTime             = "time"
	toolDeleteEverything = "delete_everything"

	// deleteNothingText is the whole body of delete_everything.
	// The tool is marked destructive so a later allowlist demo has something
	// to block. It never deletes anything.
	deleteNothingText = "deleted nothing: delete_everything is a fake tool and did not change any data"
)

// Options configures the example server. The zero value is the production server.
type Options struct {
	// Now supplies the clock for the time tool. Nil means time.Now.
	Now func() time.Time
}

type echoInput struct {
	Message string `json:"message" jsonschema:"text to return unchanged"`
}

type timeInput struct{}

type deleteInput struct{}

// NewServer builds the MCP server with echo, time, and delete_everything.
func NewServer(opts Options) *mcp.Server {
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Version: serverVersion,
	}, nil)

	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	destructive := true

	mcp.AddTool(server, &mcp.Tool{
		Name:        toolEcho,
		Description: "Return the given text unchanged.",
		Annotations: readOnly,
	}, echoTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        toolTime,
		Description: "Return the current UTC time in RFC3339.",
		Annotations: readOnly,
	}, func(ctx context.Context, req *mcp.CallToolRequest, in timeInput) (*mcp.CallToolResult, any, error) {
		return timeTool(ctx, req, in, now)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        toolDeleteEverything,
		Description: "Pretend to delete all data. This example never deletes anything; the gateway demo uses it to show a denied tools/call.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Delete everything (fake)",
			DestructiveHint: &destructive,
		},
	}, deleteEverythingTool)

	return server
}

// NewHandler serves Streamable HTTP on /mcp and a liveness check on /healthz.
//
// JSONResponse is on so a single JSON-RPC message comes back as
// application/json. That is still Streamable HTTP: the spec allows JSON
// when the client accepts it. This server never streams server-initiated
// messages, so SSE framing would only make the video harder to read.
func NewHandler(opts Options) http.Handler {
	server := NewServer(opts)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{JSONResponse: true})

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpHandler)
	mux.HandleFunc("/healthz", healthz)
	return mux
}

func echoTool(_ context.Context, _ *mcp.CallToolRequest, in echoInput) (*mcp.CallToolResult, any, error) {
	return textResult(in.Message), nil, nil
}

func timeTool(_ context.Context, _ *mcp.CallToolRequest, _ timeInput, now func() time.Time) (*mcp.CallToolResult, any, error) {
	return textResult(now().UTC().Format(time.RFC3339)), nil, nil
}

func deleteEverythingTool(context.Context, *mcp.CallToolRequest, deleteInput) (*mcp.CallToolResult, any, error) {
	return textResult(deleteNothingText), nil, nil
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

func healthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintln(w, `{"status":"ok"}`)
}
