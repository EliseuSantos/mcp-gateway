# ADR-002: Policy model

## Status

Accepted (2026-10-22)

## Context

ADR-001 left two hook points: HTTP middleware before the body is parsed, and JSON-RPC middleware around the upstream forward. This ADR decides how a caller becomes a client id, and how that id becomes a yes or no on a tool.

The example server exposes `echo`, `time`, and `delete_everything`. The last one exists so a demo can show a denied call. The gateway is the enforcement point. The upstream is not.

## Options

### Option A — per-client allowlist, deny by default

A YAML file maps client id to the tool names that client may see and call. Missing client, missing tool, and empty list all deny. `tools/list` is filtered to the intersection with the upstream list. `tools/call` outside the list returns a JSON-RPC error and the upstream is not called.

Cost: every new tool is invisible until someone edits the file. That is the cost we want.

### Option B — roles

Clients map to roles, roles map to tools. The demo only needs "demo" and "admin", and those are already client ids. A role table would be a second indirection with nothing using it yet.

### Option C — a policy language

Rego or Cedar can express "deny destructive tools" without naming each tool. That is a second language, a dependency, and a debugger, for a list of strings.

### Option D — enforce only on `tools/call`

Leave `tools/list` untouched. Less code, and the client still learns the name, description, and input schema of a tool it must not call. The denial then happens after the model has already been offered the tool.

### Option E — forward the caller's credential upstream

The upstream would decide. The example server has no auth, and a later upstream should not have to know the gateway's clients. The gateway is the trust boundary (ADR-001 already refuses to forward `Authorization`).

## Decision

Option A.

Identity is a client id string.

- An API key is a secret mapped to that id. The lookup hashes the presented secret and compares every configured hash with `subtle.ConstantTimeCompare`. When `Authorization` is present it is the credential that is checked. `Bearer` is a key unless the token has two dots and a JWT verifier is configured, in which case it is only a JWT. A failed JWT is not tried again as a key. `X-Api-Key` is used only when `Authorization` is absent, and it is always a key.
- A JWT is HS256 with a configured HMAC secret, or RS256 with one RSA source: a PEM public key, a PEM file, or a JWKS URL. `alg` `none`, and any alg other than the one the verifier has a key for, is rejected. The client id is the configured claim, `sub` by default. `exp` is required. `iss` and `aud` are checked when configured. JWKS is fetched by the operator's URL, cached, and refreshed when the `kid` is missing.

When the YAML has a `policy` section it must also have `auth`, and the reverse. The process refuses to start otherwise. With neither section, the gateway stays the pass-through from ADR-001, so a local run without a file still works.

Every `/mcp` request is authenticated, including `GET` and `DELETE`. The downstream session id is not a credential. `/healthz` stays unauthenticated.

The allowlist applies to tools only. `initialize`, notifications, and methods that are not `tools/list` or `tools/call` are forwarded for an authenticated client. `tools/list` drops tools the client may not call and preserves the JSON of the tools that remain, including fields this gateway does not interpret. An empty intersection is an empty `tools` array, not an error. `tools/call` that is not allowed returns HTTP 200 with a JSON-RPC error, code `-32001`, message `tool "<name>" is not allowed`, and the request id copied through. A `tools/list` body this process cannot filter, including `text/event-stream`, becomes an internal JSON-RPC error instead of being passed on. Failing open would publish the denied tool.

An authenticated failure is HTTP 401 with a JSON-RPC error body, code `-32000`. The status is what an MCP client treats as an auth failure. The body is there so a person reading the response can see why.

## Consequences

- Adding a tool upstream does not expose it. The YAML has to name it.
- Destructive tools are denied by omission. `delete_everything` is absent from the demo client's list and the call never reaches the example server.
- JWT callers must set `exp`. A token without an expiry is rejected, including ones an operator might mint by hand.
- API keys and HMAC secrets in `examples/config/gateway.yaml` are development values. They are the demo, not a production secret store.
- Resources, prompts, and other methods are not covered by the allowlist. A later ADR can extend the same middleware if an upstream exposes something that must be hidden.
- The allowlist does not understand tool annotations. `destructiveHint` is not a policy. The list of names is the policy.
