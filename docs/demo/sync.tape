# Three nodes register independently and converge over gossip.
Output docs/demo/sync.gif
Source docs/demo/_settings.tape
Set Height 700
Source docs/demo/_setup.tape
Show
Type "alice register --handle alice@nym --did did:key:alice"
Enter
Sleep 900ms
Type "bob register --handle bob@nym --did did:key:bob"
Enter
Sleep 900ms
Type "carol register --handle carol@nym --did did:key:carol >/dev/null"
Enter
Sleep 500ms
Type "sync-all"
Enter
Sleep 1s
Type "bob resolve alice@nym"
Enter
Sleep 3s
