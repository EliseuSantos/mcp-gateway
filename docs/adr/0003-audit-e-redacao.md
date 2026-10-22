# ADR-003: What the audit log records

## Status

Accepted (2026-10-22)

## Context

ADR-002 gives every authenticated `/mcp` request a client id and a decision on tools. The demo needs one line a person can read after a denied `tools/call`: who called, which tool, what the gateway decided, how long it took, and whether the upstream was contacted.

The arguments of a tool call are the payload. They can be a prompt, a customer message, or a token a client stuffed into the JSON. The gateway is the trust boundary (ADR-001, ADR-002). The log has to be useful without becoming a second copy of that payload.

Traces have the same constraint. Jaeger is already in Compose. A span that includes the argument body would put the payload in the trace backend.

## Options

### Option A — log the call, redact argument values, allowlist the exceptions

One JSON object per JSON-RPC request, on stdout. The object carries the client, the method, the tool name, the decision, the latency, and the upstream HTTP status. For `tools/call`, every argument key is present and the value is the string `[redacted]`, unless the operator listed that field name. Listed fields are copied through unchanged.

Cost: an operator who wants a value in the log has to name the field. A nested secret inside an allowlisted object is logged, because the allowlist is the field name, not a walk of the tree.

### Option B — log arguments in full

The denied-call line would show exactly what was rejected. It would also store every allowed call. The example `echo` tool is the friendly case. A later upstream that takes a document is the case this option gets wrong.

### Option C — hash the arguments

A hash looks like redaction and is still a dictionary for short fields (`message` is often a word). The demo cannot show the one field the operator meant to keep, and the operator cannot read the log without the original value.

### Option D — log only the tool name

Less to get wrong. It also drops the latency, the upstream status, and the allowlist the operator configured. A deny and an allow of the same tool look the same except for one boolean, and there is no way to show that `message` was the field we chose to keep.

## Decision

Option A.

The audit record is one JSON object per line on stdout. Operational logs stay on stderr, so a collector can take stdout and leave the text logs alone. `docker logs` mixes the two; the audit line is the line that is a JSON object.

Fields:

| Field | Meaning |
| --- | --- |
| `time` | UTC timestamp |
| `client` | client id from auth; omitted when the process is the pass-through and nobody authenticated |
| `method` | JSON-RPC method |
| `tool` | `tools/call` name; omitted otherwise |
| `decision` | `allow`, `deny`, `forward`, or `error` |
| `latency_ms` | time inside the audit middleware, milliseconds |
| `upstream_status` | HTTP status from the upstream; `0` when the upstream was not called |
| `arguments` | only on `tools/call` |

`decision` is `allow` when the allowlist accepted a `tools/call` or returned a filtered `tools/list`. It is `deny` when a `tools/call` is not on the list, and when `tools/list` cannot be filtered (the fail-closed path from ADR-002). It is `forward` when no policy annotated the call, which includes every non-tool method and a gateway running without a policy file. It is `error` when the middleware chain returned an error.

Argument keys are kept so the line shows the shape of the call. Values that are not named in `audit.argument_fields` are the JSON string `[redacted]`. Named fields are copied as JSON, so a number stays a number. The allowlist is the top-level name only. `arguments` that are not a JSON object become `[redacted]` with no keys. `examples/config/gateway.yaml` names `message`, which is the echo tool's text, and nothing else.

These do not appear in the audit line, in span attributes, or in metric labels:

- `Authorization`, API keys, JWTs, `Cookie`, and any other client credential
- the upstream `Mcp-Session-Id` (ADR-001: it is a bearer credential)
- tool results and `tools/list` bodies
- argument values that are not on the allowlist

`GET` and `DELETE` are session transport, not a JSON-RPC request, so they do not get an audit line. A request that fails authentication is HTTP 401 before the body is parsed, so it does not get one either. There is no client id to attach.

Traces use the same boundary. The gateway exports OTLP HTTP when `OTEL_EXPORTER_OTLP_ENDPOINT` is set (the environment wins over `telemetry.otlp_endpoint` in the YAML). The echo server does the same, so Jaeger shows both services on one trace. The propagator is W3C `traceparent`. Span attributes are the method, the tool name, the client id, the decision, and the upstream status. They are not the arguments. With no endpoint the process still serves, and it does not open an exporter.

`GET /metrics` is unauthenticated, like `/healthz`. It counts JSON-RPC requests by method and decision and records a latency histogram by method. It has no client label and no argument label.

## Consequences

- A denied `delete_everything` is one line: `decision` `deny`, `upstream_status` `0`, argument values redacted. The video can show that line.
- The echo `message` is visible only because the example file names it. Copying that file into a deployment copies that choice.
- An allowlisted object is logged whole. The way to keep a nested field out is to not list the parent.
- Stdout is the audit stream. A process supervisor that drops stdout drops the audit.
- 401s and session `GET`/`DELETE` are visible as HTTP spans when tracing is on, and absent from the audit log.
- Metrics will not answer "which client was denied." The audit line will.
