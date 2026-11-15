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
The test suite and demos use the file bus. The Bot API client is small
(`getUpdates`, `sendMessage`) but is not exercised in CI.
