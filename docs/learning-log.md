# Learning log

v0.1.0, October 2026. The decisions live in the ADRs. This note is the trade-off I would want in front of me before changing one of them.

## Phase 1 — proxy

Streamable HTTP is the only transport. stdio would have made the demo a subprocess, and the thing I wanted to show is a network boundary with a session id on it.

The gateway terminates the client session and opens a different one upstream. Relaying bytes with `httputil.ReverseProxy` was the smaller program. It was the wrong one, because the upstream `Mcp-Session-Id` would have been a credential in the client's hands, and a later allowlist would have had to parse a stream it did not own. Re-encoding messages with the official server SDK was the other temptation. The SDK's tool registry is process-global and drops fields it does not know, so a filtered `tools/list` would have silently rewritten the upstream. The forwarder sends the original bytes unless a middleware replaces them.

The official client opens a GET SSE stream after `initialize`. A proxy that only forwarded POST failed that client. GET is relayed. JSON-RPC batches are rejected: one HTTP request with two calls could hide a denied `tools/call` next to an allowed one. When the upstream cannot be reached, the gateway answers HTTP 200 with JSON-RPC `-32603`. A 502 is a transport error, and the official client treats it as transient and may not show the body.

Idle session expiry is out of scope. Sessions live until DELETE or process shutdown.

## Phase 2 — auth and policy

The allowlist is a map from client id to tool names, deny by default. Roles would have been a second table with two clients in the demo. Rego or Cedar would have been a second language for a list of strings. Enforcing only on `tools/call`, and leaving `tools/list` alone, would have handed the model the name and schema of a tool it must not call.

`tools/list` is filtered to the intersection and the JSON of the remaining tools is preserved, including fields this process does not interpret. An empty intersection is an empty array. A `tools/call` that is not allowed is JSON-RPC `-32001` on HTTP 200, and the upstream is not contacted. A `tools/list` this process cannot filter, including an SSE body, fails closed.

Auth and policy are both present or both absent. An authenticated client with no allowlist would have been allowed to call everything, which is the outcome the file would not have said out loud. With neither section, the binary stays the pass-through from phase 1.

An API key is hashed and compared in constant time. A bearer token with two dots is a JWT when a verifier is configured, and a failed JWT is not tried again as a key. `exp` is required. `alg: none` is rejected. JWKS is cached and refreshed when the `kid` is missing. Client credentials are not forwarded upstream. The session id is not a credential, so GET and DELETE on `/mcp` are authenticated too. `/healthz` is not.

Resources, prompts, and `destructiveHint` are not policy. The list of names is the policy. The dev keys in the example file are the demo, not a secret store.

## Phase 3 — audit and traces

One JSON object per JSON-RPC request, on stdout. The text log stays on stderr so a collector can take the audit stream. `docker compose logs` mixes them; the audit line is the line that parses as JSON.

Argument keys are kept and values are `[redacted]`, unless the field name is on `audit.argument_fields`. Hashing the values would still have been a dictionary for short strings, and the demo could not have shown the one field an operator chose to keep. Logging the whole argument would have copied the payload into the log. The allowlist is the top-level name only. An allowlisted object is logged whole, which is the sharp edge: the way to keep a nested field out is to not list the parent.

Spans and metrics follow the same boundary. Attributes are method, tool, client, decision, and upstream status. They are not the arguments, the credential, or the upstream session id. `GET /metrics` has no client label, because that label grows with the number of clients. The audit line is where the client id lives. A 401 is not an audit event: the body has not been parsed and there is no client id. GET and DELETE of the session are not JSON-RPC, so they do not get a line either. They do get an HTTP span when tracing is on.

Tracing is off when no OTLP endpoint is set, so `make run` does not need Jaeger. Compose sets the endpoint on both the gateway and the echo server, and the W3C `traceparent` is what puts them on one trace. A denied call has no echo-server span, which is the picture that matches `upstream_status: 0`.

## Phase 4 — release

The demo is a compose file and a script, not a hosted URL. The VHS tape runs that script so the recording and the quickstart are the same commands. The Dockerfile used by Compose stayed the one under `cmd/mcp-gateway`. Goreleaser builds the release binaries and does not push an image.

What I would revisit after v0.1.0: idle session expiry, an allowlist for resources and prompts, and a real secret store in place of the development keys. I would not revisit the session boundary or the decision to fail closed on a `tools/list` the gateway cannot filter.
