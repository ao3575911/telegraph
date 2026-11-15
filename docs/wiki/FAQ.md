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
`/start`, `/help`, `/resolve handle@nym`, `/pin` and `/status` in a direct
message. Nothing sent to the bot is written to the log.

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
