# The gate, recorded end to end. Regenerate with:
#
#     go build -o /tmp/enola-demo/bin/enola ./cmd/enola
#     vhs docs/images/layers-gate.tape
#
# It drives examples/layers-gate through the same three runs run.sh does, so the GIF
# and the README snippets cannot drift apart: both come from this fixture.
#
# The tape assumes /tmp/enola-demo has been prepared by the block below — the setup is
# hidden rather than typed, because a viewer is here for the verdicts, not for cp.

Output docs/images/layers-gate.gif

Set Shell "bash"
Set FontSize 18
Set Width 1200
Set Height 800
Set Padding 20
Set TypingSpeed 25ms

Hide
Type "cd /tmp/enola-demo && export PATH=/tmp/enola-demo/bin:$PATH && clear" Enter
Sleep 1s
Show

Type "# storage now emails the buyer a receipt. One import. Nothing fails yet:" Enter
Sleep 800ms
Type "enola check layers-gate 2>/dev/null" Enter
Sleep 6s

Type "clear" Enter
Sleep 500ms
Type "# The layer order is declared. Enforce it:" Enter
Sleep 800ms
Type "enola check --fail-on=layers layers-gate 2>/dev/null && echo 'SHIP IT'" Enter
Sleep 7s

Type "clear" Enter
Sleep 500ms
Type "# Fix it: storage returns the receipt, delivery sends it." Enter
Sleep 800ms
Type "cp fixed/storage.go layers-gate/storage/storage.go" Enter
Sleep 500ms
Type "enola check --fail-on=layers layers-gate 2>/dev/null && echo 'SHIP IT'" Enter
Sleep 8s
