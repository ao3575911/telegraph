# The committee leader checkpoints, the others co-sign, every node audits.
Output docs/demo/checkpoint.gif
Source docs/demo/_settings.tape
Set Height 700
Source docs/demo/_setup.tape
Type "for n in alice bob carol; do $n register --handle $n@nym --did did:key:$n; done >/dev/null; sync-all >/dev/null; sleep 1; clear"
Enter
Sleep 2s
Show
Type "checkpoint-all"
Enter
Sleep 1.5s
Type "sync-all"
Enter
Sleep 800ms
Type "bob audit"
Enter
Sleep 2s
Type "alice pin"
Enter
Sleep 3s
