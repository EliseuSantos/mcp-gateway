# Reproducible demo. Run from the repo root, after `docker compose build`,
# with host ports 8080, 8081, and 16686 free:
#
#   vhs docs/demo/demo.tape
#
# Output lands at docs/demo/demo.mp4.
Output docs/demo/demo.mp4

Set Shell bash
Set FontSize 18
Set Width 1280
Set Height 800
Set Padding 20
Set TypingSpeed 18ms

Type "docker compose up --build -d && docs/demo/show.sh"
Enter
Sleep 35s
