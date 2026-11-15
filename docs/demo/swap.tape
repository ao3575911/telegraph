# Atomic handle swap: offer, accept, claim, claim.
Output docs/demo/swap.gif
Source docs/demo/_settings.tape
Set Height 1000
Source docs/demo/_setup.tape
Type "for n in alice bob carol; do $n register --handle $n@nym --did did:key:$n; done >/dev/null; sync-all >/dev/null; clear"
Enter
Sleep 1.5s
Show
Type "alice swap offer --gives alice@nym --wants bob@nym"
Enter
Sleep 1.2s
Type "SID=$(sid-of alice); sync-all"
Enter
Sleep 900ms
Type "bob swap accept --sid $SID"
Enter
Sleep 1s
Type "sync-all >/dev/null; alice swap claim --sid $SID"
Enter
Sleep 900ms
Type "sync-all >/dev/null; bob swap claim --sid $SID"
Enter
Sleep 900ms
Type "sync-all >/dev/null; carol swap status --sid $SID"
Enter
Sleep 3s
