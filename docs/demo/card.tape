# A bio card is verified only once its owner has signed it.
Output docs/demo/card.gif
Source docs/demo/_settings.tape
Set Height 600
Source docs/demo/_setup.tape
Type "for n in alice bob carol; do $n register --handle $n@nym --did did:key:$n; done >/dev/null; sync-all >/dev/null; clear"
Enter
Sleep 1.5s
Show
Type "LINE=$(alice card --handle alice@nym | head -1); echo $LINE"
Enter
Sleep 900ms
Type `carol scan "$LINE"`
Enter
Sleep 1.2s
Type `alice attest --line "$LINE"`
Enter
Sleep 900ms
Type "sync-all"
Enter
Sleep 700ms
Type `carol scan "$LINE"`
Enter
Sleep 3s
