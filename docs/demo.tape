# Records docs/demo.gif. Start a server first: go run ./cmd/server -addr 127.0.0.1:2296 -fill rl,baseline
# Then: vhs docs/demo.tape
Output docs/demo.gif
Set FontSize 13
Set Width 1000
Set Height 880
Set Padding 10
Set Framerate 12
Set TypingSpeed 40ms
Type "ssh -p 2296 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o PubkeyAuthentication=no neo@localhost"
Enter
Sleep 4s
Type@120ms "dddd"
Right
Sleep 400ms
Type@120ms "ssss"
Down
Sleep 400ms
Type "D"
Sleep 600ms
Type@120ms "aaaa"
Left
Up
Sleep 500ms
Type@120ms "wwwdd"
Right
Sleep 400ms
Type "S"
Sleep 1s
Type@120ms "ddss"
Down
Right
Sleep 3s
Type "q"
Sleep 500ms
