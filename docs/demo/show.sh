#!/usr/bin/env bash
# Walk through the local demo: an allowed tools/list, a blocked tools/call,
# the audit line, and the Jaeger search URL.
# Run it from anywhere. Compose must already be up, from the repo root.
#
# GATEWAY_PORT (default 8080) and JAEGER_UI_PORT (default 16686) select the
# host ports. MCP_GATEWAY_URL overrides the gateway base URL entirely.
# DEMO_PACE_SECONDS holds each result on screen while a tape is recording.
set -euo pipefail

root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"

port=${GATEWAY_PORT:-8080}
base=${MCP_GATEWAY_URL:-http://127.0.0.1:${port}}
key=${MCP_GATEWAY_DEMO_KEY:-dev-demo-key}
jaeger_port=${JAEGER_UI_PORT:-16686}
pace=${DEMO_PACE_SECONDS:-0}

hold() {
	if [ "$pace" != "0" ]; then
		sleep "$pace"
	fi
}

say() {
	echo
	echo "$1"
}

print_json() {
	if [ -z "${1}" ]; then
		echo "(empty body)"
		return
	fi
	if command -v jq >/dev/null 2>&1; then
		if printf '%s\n' "$1" | jq . >/dev/null 2>&1; then
			printf '%s\n' "$1" | jq .
			return
		fi
	fi
	printf '%s\n' "$1"
	echo
}

hdr=$(mktemp)
body=$(mktemp)
trap 'rm -f "$hdr" "$body"' EXIT

say "1/6  gateway ${base}   (GATEWAY_PORT default 8080)"
echo "waiting for ${base}/healthz"
ready=0
for _ in $(seq 1 90); do
	if curl -sf "${base}/healthz" >/dev/null; then
		ready=1
		break
	fi
	sleep 1
done
if [ "$ready" -ne 1 ]; then
	echo "gateway did not become ready at ${base}/healthz" >&2
	exit 1
fi
curl -sS "${base}/healthz"
echo
hold

say "2/6  initialize as the demo key"
curl -sS -D "$hdr" -o "$body" "${base}/mcp" \
	-H 'Content-Type: application/json' \
	-H 'Accept: application/json, text/event-stream' \
	-H "Authorization: Bearer ${key}" \
	-d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"demo","version":"0"}}}'
sid=$(awk 'tolower($1)=="mcp-session-id:" {print $2}' "$hdr" | tr -d '\r')
if [ -z "$sid" ]; then
	echo "no Mcp-Session-Id in the initialize response" >&2
	cat "$body" >&2
	exit 1
fi
echo "session ${sid}"
if command -v jq >/dev/null 2>&1; then
	jq '{protocol: .result.protocolVersion, server: .result.serverInfo}' "$body"
else
	print_json "$(cat "$body")"
fi
echo "notifications/initialized"
print_json "$(
	curl -sS "${base}/mcp" \
		-H 'Content-Type: application/json' \
		-H 'Accept: application/json, text/event-stream' \
		-H "Authorization: Bearer ${key}" \
		-H "Mcp-Session-Id: ${sid}" \
		-d '{"jsonrpc":"2.0","method":"notifications/initialized"}'
)"
hold

call() {
	curl -sS "${base}/mcp" \
		-H 'Content-Type: application/json' \
		-H 'Accept: application/json, text/event-stream' \
		-H "Authorization: Bearer ${key}" \
		-H "Mcp-Session-Id: ${sid}" \
		-d "$1"
}

say "3/6  tools/list — delete_everything is not on the demo allowlist"
listed=$(call '{"jsonrpc":"2.0","id":2,"method":"tools/list"}')
if command -v jq >/dev/null 2>&1; then
	printf '%s\n' "$listed" | jq -r '.result.tools[] | "  \(.name) — \(.description)"'
else
	print_json "$listed"
fi
hold

say "4/6  tools/call delete_everything — blocked, upstream is not called"
print_json "$(call '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"delete_everything","arguments":{"confirm":"yes-really"}}}')"
hold

say "5/6  audit line for that deny (upstream_status 0)"
audit=""
for _ in $(seq 1 20); do
	audit=$(docker compose logs gateway --no-color --no-log-prefix 2>&1 | grep '"decision":"deny"' | tail -n 1 || true)
	if [ -n "$audit" ]; then
		break
	fi
	sleep 0.5
done
if [ -z "$audit" ]; then
	echo "no deny audit line in the gateway logs" >&2
	exit 1
fi
print_json "$audit"
hold

say "6/6  the trace is in Jaeger (the deny has no echo-server span)"
echo "http://127.0.0.1:${jaeger_port}/search?service=mcp-gateway"
hold
