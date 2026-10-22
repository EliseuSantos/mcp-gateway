#!/usr/bin/env bash
# Walk through the local demo: an allowed tools/list, a blocked tools/call,
# the audit line, and the Jaeger search URL.
# Run it from anywhere. Compose must already be up, from the repo root.
set -euo pipefail

root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"

base=${MCP_GATEWAY_URL:-http://127.0.0.1:8080}
key=${MCP_GATEWAY_DEMO_KEY:-dev-demo-key}

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

hdr=$(mktemp)
body=$(mktemp)
trap 'rm -f "$hdr" "$body"' EXIT

echo "initialize as demo"
curl -sS -D "$hdr" -o "$body" "${base}/mcp" \
	-H 'Content-Type: application/json' \
	-H 'Accept: application/json, text/event-stream' \
	-H "Authorization: Bearer ${key}" \
	-d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"demo","version":"0"}}}'
cat "$body"
echo
sid=$(awk 'tolower($1)=="mcp-session-id:" {print $2}' "$hdr" | tr -d '\r')
if [ -z "$sid" ]; then
	echo "no Mcp-Session-Id in the initialize response" >&2
	exit 1
fi
echo "session ${sid}"

call() {
	curl -sS "${base}/mcp" \
		-H 'Content-Type: application/json' \
		-H 'Accept: application/json, text/event-stream' \
		-H "Authorization: Bearer ${key}" \
		-H "Mcp-Session-Id: ${sid}" \
		-d "$1"
	echo
}

echo
echo "notifications/initialized"
call '{"jsonrpc":"2.0","method":"notifications/initialized"}'

echo "tools/list — delete_everything is not on the demo allowlist"
call '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'

echo "tools/call delete_everything — blocked, upstream is not called"
call '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"delete_everything","arguments":{"confirm":"yes-really"}}}'

echo "audit line"
docker compose logs gateway --no-color --no-log-prefix 2>&1 | grep '"decision":"deny"' | tail -n 1

echo
echo "Jaeger: http://127.0.0.1:16686/search?service=mcp-gateway"
