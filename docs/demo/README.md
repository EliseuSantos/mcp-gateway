# Demo

The recording is a local `docker compose up`. Nothing in this repo is hosted.

From the repository root, with ports 8080, 8081, and 16686 free:

```bash
docker compose build
vhs docs/demo/demo.tape
```

The tape runs `docker compose up --build -d` and then [`show.sh`](show.sh). The script waits until the gateway answers `/healthz`, then it:

1. initializes a session with `Authorization: Bearer dev-demo-key`
2. lists tools and shows `echo` and `time` (`delete_everything` is filtered out)
3. calls `delete_everything` and prints the JSON-RPC error
4. prints the audit line (`"decision":"deny"`, `"upstream_status":0`)
5. prints the Jaeger search URL for the trace: <http://127.0.0.1:16686/search?service=mcp-gateway>

`show.sh` can also be run on its own after `docker compose up --build -d`.

## Rendering

[VHS](https://github.com/charmbracelet/vhs) 0.10 needs `ffmpeg` and [`ttyd`](https://github.com/tsl0922/ttyd) on `PATH`. Install them, then run the tape from the repository root:

```bash
go install github.com/charmbracelet/vhs@latest
vhs docs/demo/demo.tape
```

The mp4 is `docs/demo/demo.mp4`. VHS treats a leading `/` as a regex, so the `Output` path in the tape is relative. If the mp4 is small and a GIF is useful:

```bash
ffmpeg -y -i docs/demo/demo.mp4 docs/demo/demo.gif
```

Build the images once before recording so the tape spends its time on the calls, not on the compiler. The tape sleeps 35 seconds, which covers a warm `up -d` plus the script. A cold image build needs a longer `Sleep` in the tape.

## Why this checkout has no mp4

VHS records on this machine once `ttyd` is installed. The demo tape was not rendered here because `docker compose up` publishes host port 8080, and that port was already taken by another container. The tape and `show.sh` are the source of the recording.
