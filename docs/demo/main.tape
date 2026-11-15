# Overview: register → gossip → checkpoint → verify. Run from repo root:
#   vhs docs/demo/main.tape
Output docs/demo/main.gif
Output docs/demo/main.mp4
Source docs/demo/_settings.tape
Set Height 880
Source docs/demo/_setup.tape
Type "bob register --handle bob@nym --did did:key:bob >/dev/null && carol register --handle carol@nym --did did:key:carol >/dev/null && clear"
Enter
Sleep 500ms
Show
Type "alice register --handle alice@nym --did did:key:alice"
Enter
Sleep 1.2s
Type "sync-all"
Enter
Sleep 1.2s
Type "checkpoint-all"
Enter
Sleep 1.5s
Type "sync-all"
Enter
Sleep 800ms
Type "carol verify alice@nym"
Enter
Sleep 1.5s
Type "carol audit"
Enter
Sleep 3.5s
