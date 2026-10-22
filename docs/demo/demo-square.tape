# 1080x1080 recording for LinkedIn. Same steps as demo.tape, larger type.
# Build images first. GATEWAY_PORT=18080 keeps host port 8080 untouched.
#
#   vhs docs/demo/demo-square.tape
Output docs/demo/demo-square.mp4

Set Shell bash
Set FontSize 34
Set Width 1080
Set Height 1080
Set Padding 36
Set Framerate 20
Set TypingSpeed 28ms

Hide
Type "export PS1='$ '"
Enter
Show

Type "echo '1. GATEWAY_PORT defaults to 8080. This run uses a free port.'"
Enter
Sleep 2s

Type "export GATEWAY_PORT=18080 DEMO_PACE_SECONDS=7"
Enter
Sleep 2s

Type "echo '2. start the stack'"
Enter
Sleep 1.5s

Type "docker compose up -d"
Enter
Sleep 6s

Type "echo '3. health, allowlist, blocked call, audit, Jaeger'"
Enter
Sleep 2.5s

Type "docs/demo/show.sh"
Enter
Sleep 52s
