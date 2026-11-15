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
only if it contains that checkpoint.

**How do I start a brand-new network?**
There is no pin until the first checkpoint, so the first nodes run
`telegraph sync --genesis`. After the first checkpoint, `telegraph pin`
prints the pin to share.

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

**How do I install without Go?**
`curl -fsSL https://raw.githubusercontent.com/ao3575911/telegraph/main/scripts/install.sh | sh`
downloads the release for your OS, checks its SHA-256 and installs it to
`~/.local/bin`. With Go, pin the version (`@v0.2.2`): `@latest` can lag
behind a new tag on the module proxy.

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
