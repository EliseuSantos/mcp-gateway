# ADR-001: Runtime and transport

## Status

Accepted (2026-10-22)

## Context

mcp-gateway sits between MCP clients and MCP servers. v0.1 has to authenticate a client, filter `tools/list`, reject a disallowed `tools/call`, write an audit record, and export a trace. The project is shown with a recorded local run (`docker compose up`), not a hosted demo, so the runtime is a technical and portfolio choice rather than a hosting constraint.

The example upstream (`examples/echo-server`) already speaks Streamable HTTP with the official Go SDK and returns JSON when the client accepts it.

## Options

### Option A — TypeScript

The MCP reference implementation and many servers are TypeScript. Choosing it would make protocol samples easy to copy and would duplicate a stack that is already common in public portfolios.

Cost: the gateway is a long-running network proxy. Go's standard library already has the HTTP server, context cancellation, and a race detector, and a single static binary is the artifact `docker compose` and a release both want. TypeScript would add a runtime and a package manager for a process whose job is to move HTTP messages.

### Option B — Go, Streamable HTTP, terminate the session

The gateway is an MCP endpoint for the client and a separate MCP client of the upstream. It mints its own `Mcp-Session-Id`. The upstream session id never leaves the process. Each JSON-RPC message is parsed, passed through a middleware chain, and forwarded. The original bytes are forwarded when middleware does not rewrite them.

Cost: the gateway must forward methods it does not interpret (`ping`, resources, prompts, notifications, and client replies to server requests). It also has to relay the standalone SSE stream (`GET`) and session close (`DELETE`), because a client that negotiated a protocol older than `2026-07-28` opens that stream after `initialize`.

### Option C — Go, relay the bytes

`httputil.ReverseProxy` (or an equivalent pipe) would copy the HTTP request, including the upstream `Mcp-Session-Id`, and would satisfy a pure pass-through in fewer lines.

Cost: the session id is a bearer credential. A client that learns it, or an upstream that is reachable from the client, bypasses the gateway. Filtering `tools/list` and rejecting `tools/call` need a JSON-RPC parser anyway, so the relay stops being a relay in the next phase and the session model has to be replaced under the hooks.

### Option D — also speak stdio

stdio is how local MCP servers are spawned. Supporting it means the gateway owns a child process per session: lifecycle, stdout framing, and cancellation.

Cost: v0.1 is a network service in Compose. stdio does not cross a container boundary cleanly, and it is a second transport to test before the HTTP path is done. A client that only speaks stdio can use a small local adapter in front of the gateway.

## Decision

Go, Streamable HTTP only, and a session-terminating proxy (option B).

The gateway terminates the downstream session and opens its own upstream session. Middleware is the extension point, in two layers:

- HTTP middleware runs before the body is parsed. Authentication belongs here, so a missing credential never becomes an upstream call. `/healthz` stays outside that wrapper.
- JSON-RPC middleware runs per message, outermost first, around the upstream forward. The allowlist belongs here: it can return a JSON-RPC error for `tools/call` without contacting the upstream, and it can rewrite a buffered `application/json` `tools/list` on the way back. The audit log belongs here too, because it can see the decision, the latency, and the upstream status.

Pass-through is the empty chain. The forwarder sends the original message bytes and relays the upstream status and body. `text/event-stream` responses are streamed and are not rewritten. JSON-RPC batches are rejected: the allowlist decides per message, and a batch would let a denied call travel with an allowed one.

Upstream failures that produce no HTTP response become a JSON-RPC error (`-32603`) on HTTP 200. An HTTP 502 is a transport failure; the official client treats it as transient and may not surface a body. A 200 with an `error` object is a completed call the client reports. Upstream responses that do arrive, including their JSON-RPC errors, are relayed unchanged.

Client credentials (`Authorization`, `Cookie`, and anything else outside `Accept`, `Content-Type`, `Last-Event-Id`, and `Mcp-*`) are not forwarded. The gateway is the trust boundary. The upstream session id is not copied to the client.

## Consequences

- Auth, policy, and audit can be added without changing the transport. They are middleware the process wires in `cmd/mcp-gateway`.
- Unknown methods and extra JSON fields survive, because the gateway does not re-encode a message it does not rewrite. That is deliberate: re-building messages with the SDK's tool registry would drop fields the SDK does not model, and that registry is process-wide rather than per session.
- SSE `GET` is a byte pipe. Policy cannot rewrite an event stream in v0.1. The example server answers `POST` with `application/json`, which is the response policy will filter.
- Idle sessions are not expired in v0.1. A later change can add a timeout without changing the middleware types.
- stdio, and spawning upstream processes, stay out of scope.
- A pure byte relay is not a fallback we keep behind a flag. One session model is enough to operate and to explain.
