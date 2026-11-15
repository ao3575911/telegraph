# FAQ

**Is this a blockchain?**
No. There is no global chain, mining or token. Each issuer has its own
hash-chained log; checkpoints are signed by small rotating committees.

**Do I need Telegram to try it?**
No. Set `TELEGRAPH_BUS` to a local file and run several nodes with
different `TELEGRAPH_HOME` directories. See [[Quickstart]].

**What stops one node from forging a checkpoint?**
The committee is derived from the checkpoint's own root and count, and a
checkpoint needs 2 of 3 committee signatures. Audits recompute the root and
reject mismatches, forks and gaps.

**How does a new node know which history is real?**
It is given a checkpoint hash (a pin) out of band and accepts a peer's dump
only if it contains that checkpoint. The check is mandatory: `sync`
refuses to merge gossip until the local log verifies against a pin, even
if you registered a handle first.

**How do I start a brand-new network?**
There is no pin until the first checkpoint, so the first nodes run
`telegraph sync --genesis`. Nodes on the first checkpoint's committee pin
it automatically; `telegraph pin` prints the pin to share. A node that
sees a checkpoint it is not on the committee of refuses `--genesis` and
needs the pin.

**What if the checkpoint leader goes offline?**
The second committee member issues after the leader is an hour overdue,
the third after two hours. The returning leader accepts their checkpoint.

**What can I ask the bot?**
Open the command menu, or send `/help`: `/resolve handle@nym`,
`/register name [did]`, `/revoke name`, `/status`, `/stats`, `/pin`, `/about`,
`/version`, `/help`, `/start`. You can also send a handle on its own.
Only `/register` and `/revoke` write to the log, signed by the node.

**Can I register from Telegram?**
Yes, in a private chat with a bot whose node runs with `--bot-register`:
`/register adz` gives `adz@nym`. It is custodial: the node's key owns the
handle and only your Telegram account can change or revoke it through that
node. With the CLI (`telegraph register --handle adz@nym --did did:key:adz`,
then `telegraph sync`) your own key owns it. Until
[#15](https://github.com/ao3575911/telegraph/issues/15) is fixed, any key
can overwrite a handle with a later register, whichever path you use.

**What are Founding 100 and Early adopter?**
The first 100 distinct handles ever registered on the network get
"🏅 Founding 100" on `/register`. Handles 101–1000 get "🎖 Early adopter".
`/stats` shows how many spots are left. After 1000 there is no badge.

**How large can a network get?**
A node ingesting 100k handles takes 4.6 s and about 257 MB of RAM; 1M
works (48 s, 2.8 GB). Around 2M, saving `data.json` runs out of memory.
One Telegram channel is also limited to about 28,800 messages a day, so
publishing 100k entries would take days. The
[stress report](https://github.com/ao3575911/telegraph/blob/main/docs/STRESS-REPORT.md)
has the numbers and the seven bugs it found, now fixed on main: two
crafted channel posts could crash every node, a failed send was marked
published and never retried, a flood of 10,000 new chats reset every rate
limit, and `data.json` was not crash-safe. 0.3.0 fixed
[#15](https://github.com/ao3575911/telegraph/issues/15) (any later
register could take over a handle). Checkpoints that grow with every user
remain.

**Who owns a handle?**
Whoever registered it first. After that only the owner's key can change
its DID, revoke it or swap it; a register from any other key is ignored by
every node, whatever its timestamp. A revoked handle is free for the next
register. Handles registered through the bot are owned by that node's key
and bound to your Telegram account, so only you can change or revoke them
there.

**How do I install without Go?**
`curl -fsSL https://raw.githubusercontent.com/ao3575911/telegraph/main/scripts/install.sh | sh`
downloads the release for your OS, checks its SHA-256 and installs it to
`~/.local/bin`. With Go, `go install …/cmd/telegraph@latest` takes the
newest tag (the module proxy can take a little while to see a brand-new
one); pin a version such as `@v0.3.0` for a reproducible install. Every
node of a network must run a compatible version.

**Can a swap leave one side without a handle?**
Not if both sides sync before their locks expire. The responder's lock
expires 48 hours before the initiator's, so the responder always has time
to claim after the preimage is revealed. Otherwise both sides reclaim.

**Why does `resolve` show no DID after a swap?**
The DID belonged to the previous owner. The new owner sets one with
`telegraph register`.

**Which Go version?**
Go 1.22 or later. There are no third-party dependencies.

**Is the Telegram transport tested?**
The Bot API client is tested against a fake Bot API server in CI. Multi-node
tests and demos use the file bus; there is no live-bot test in CI.

**Can 0.1.0 and 0.2.0 nodes share a network?**
No. 0.1.0 nodes do not know the checkpoint `Heads` field and reject 0.2.0
checkpoints. Upgrade every node together.
