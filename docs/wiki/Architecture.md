# Architecture

## Entries and logs (`internal/tlog`, `internal/merkle`)

Every action is an entry: register, revoke, checkpoint, heartbeat, swap
offer, lock, claim, reclaim, or card attestation. An entry carries a
payload, the issuer's Ed25519 public key, the hash of the issuer's previous
entry, its own SHA-256 hash and a signature. Each issuer's entries form a
hash chain, so a missing or reordered entry shows up as a gap.

A Merkle root is computed over the sorted entry hashes, so any two nodes
holding the same set of entries derive the same root regardless of arrival
order.

## Registry (`internal/crdt`, `internal/store`)

The registry is a CRDT. Merging is a union of entries de-duplicated by hash
and applied in hash order, so merge is commutative, associative and
idempotent. Plain register and revoke entries resolve last-writer-wins.
The store persists entries in `TELEGRAPH_HOME/data.json` (written through a
temporary file, fsync and rename) and tracks which local entries still need
publishing. The JSON document is the practical cap: a node runs out of
memory saving around 2M entries. See the
[stress report](https://github.com/ao3575911/telegraph/blob/main/docs/STRESS-REPORT.md).

## Checkpoints (`internal/checkpoint`)

A checkpoint commits to the Merkle root and count of the entries it
covers, plus the previous checkpoint's hash. Since 0.2.0 it also names the
head of every issuer chain it covers (`Heads`): the covered set is each
chain from genesis to its head. An entry that reaches an auditor but not
the issuer is therefore uncovered (reported as late if it is stamped
before the checkpoint) rather than breaking the root. Checkpoints without
`Heads` (0.1.0) cover the entries stamped strictly before their timestamp.

- **Committee.** The target is `sha256("telegraph/cp/<root>/<count>")`. The
  three issuer keys closest to the target by symmetric ring distance form
  the committee, so it rotates with the log and cannot be chosen by any one
  issuer.
- **Quorum.** The closest member (the leader) issues; the others co-sign
  when they sync. If the leader is offline, the second member issues once
  the oldest uncheckpointed entry is `Interval` (6 h) + 1 h old, and the
  third at + 2 h. Serving nodes check every 10 minutes. A checkpoint is valid with 2 of 3 committee signatures (1
  of 1 for a single-issuer network). Until then it is held as pending.
- **Audit.** `telegraph audit` recomputes the root and count, checks the
  committee and signatures, and walks the checkpoint chain for forks and
  gaps.
- **Pins.** A new node trusts only a checkpoint hash shared out of band
  (`sync --pin`). A dump that does not contain it is rejected as a whole.
  Nodes of a brand-new network, before any checkpoint exists, use
  `sync --genesis` instead.
  `pin --rotate` advances the pin to the latest quorum-verified checkpoint.

## Swaps and cards (`internal/handshake`)

Two owners exchange handles HTLC-style. The initiator offers and locks
their handle under `sha256(preimage)`; the responder locks theirs to the
initiator with an expiry 48 hours earlier. The initiator claims the
responder's handle, which reveals the preimage; the responder uses it to
claim the initiator's handle. If anyone stops, locks can be reclaimed after
expiry. A lock counts only if its signer owned the handle immediately
before it.

A bio card is one line, `⟁ handle@nym · <key prefix>`, that the owner signs
with `attest`. `scan` checks the line against the current owner and reports
VERIFIED, unattested, or STALE after a transfer.

## Transport (`internal/transport`)

The Telegram transport uses only the Bot API (`getUpdates`, `sendMessage`).
Entries are framed into messages and posted to every configured channel;
nodes poll, ingest, co-sign pending checkpoints and send heartbeats.
`sync --serve` also answers `/dump` requests so new nodes can bootstrap,
and replies to people (`/resolve`, `/register`, `/revoke`, `/status`,
`/stats`, `/pin`, `/about`, `/version`, `/help`, `/start`) from the local store; see
[TELEGRAM.md](https://github.com/ao3575911/telegraph/blob/main/docs/TELEGRAM.md). Private-chat text is never parsed as a
frame; the only entries the bot writes are custodial `/register` and
`/revoke` entries signed by the node (opt-in, `--bot-register`); replies use HTML with
escaped input and are rate-limited per chat (5 per 30 s). Frame reassembly
is bounded (32,768 parts, 512 unfinished entries, 128 MiB) so crafted
channel posts cannot crash a node. A failed send leaves the entry pending
and is retried.
The file bus (`TELEGRAPH_BUS`) implements the same interface on a local
append-only file for tests and demos.

## CLI (`cmd/telegraph`, `internal/ui`)

One binary with compact output. Colour is used only on a terminal and never
when `NO_COLOR` is set.
