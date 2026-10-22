# Demo

The recording is a local `docker compose up`. Nothing in this repo is hosted.

| File | What it is |
| --- | --- |
| [demo.mp4](demo.mp4) | 16:9, for the README and the site |
| [demo-square.mp4](demo-square.mp4) | 1080×1080, larger type, for LinkedIn |
| [demo.gif](demo.gif) | short GIF of the same run, embedded at the top of the README |

Host ports default to 8080 (gateway), 8081 (echo-server), and 16686 (Jaeger UI). The published recording sets `GATEWAY_PORT=18080` so it does not bind 8080. Override `ECHO_PORT`, `JAEGER_UI_PORT`, `JAEGER_OTLP_GRPC_PORT`, or `JAEGER_OTLP_HTTP_PORT` the same way when those ports are taken. Container ports stay 8080, 8081, 4317, and 4318.

From the repository root:

```bash
docker compose build
vhs docs/demo/demo.tape
vhs docs/demo/demo-square.tape
```

Each tape exports `GATEWAY_PORT=18080` and `DEMO_PACE_SECONDS=7`, starts the stack, and runs [`show.sh`](show.sh). The script:

1. waits until the gateway answers `/healthz`
2. initializes a session with `Authorization: Bearer dev-demo-key`
3. lists tools and shows `echo` and `time` (`delete_everything` is filtered out)
4. calls `delete_everything` and prints the JSON-RPC error
5. prints the audit line (`"decision":"deny"`, `"upstream_status":0`)
6. prints the Jaeger search URL: <http://127.0.0.1:16686/search?service=mcp-gateway>

`show.sh` can also be run on its own after `docker compose up -d`. Leave `DEMO_PACE_SECONDS` unset for that.

## Rendering

[VHS](https://github.com/charmbracelet/vhs) 0.10 needs `ffmpeg` and [`ttyd`](https://github.com/tsl0922/ttyd) on `PATH`. Install them, then run the tape from the repository root:

```bash
go install github.com/charmbracelet/vhs@latest
vhs docs/demo/demo.tape
```

VHS treats a leading `/` as a regex, so each `Output` path is relative. Build the images once before recording so the tape spends its time on the calls, not on the compiler.

The committed GIF is the full 16:9 recording at 1280×720, about 2 MB:

```bash
ffmpeg -y -i docs/demo/demo.mp4 \
  -vf "fps=10,scale=1280:-1:flags=lanczos,split[s0][s1];[s0]palettegen=stats_mode=diff:max_colors=128[p];[s1][p]paletteuse=dither=bayer:bayer_scale=2" \
  -loop 0 docs/demo/demo.gif
```
