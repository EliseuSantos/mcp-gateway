# 16:9 recording for the README and the site.
# Build the images first (`docker compose build`) so this tape does not
# spend its time on the compiler. Host port 8080 is left alone: the typed
# export publishes the gateway on 18080. Echo and Jaeger stay on 8081 and
# 16686 unless ECHO_PORT / JAEGER_UI_PORT are set in the environment.
#
#   vhs docs/demo/demo.tape
Output docs/demo/demo.mp4

Set Shell bash
Set FontSize 22
Set Width 1280
Set Height 720
Set Padding 24
Set Framerate 20
Set TypingSpeed 32ms

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
