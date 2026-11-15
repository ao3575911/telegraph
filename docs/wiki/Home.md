# Telegraph

Telegraph is a decentralised handle registry. Each node keeps a signed,
hash-chained log of handle claims, gossips it over Telegram channels, and
resolves handles with a deterministic merge. Rotating 3-member committees
sign Merkle checkpoints, valid once 2 of 3 have signed, so a node can check
that what it sees matches what everyone else sees.

- [[Quickstart]]: three nodes on one machine, no Telegram needed
- [[Architecture]]: logs, merge, checkpoints, swaps, transport
- [[CLI reference|CLI-reference]]: every command and flag
- [[FAQ]]
- [Telegram bot](https://github.com/ao3575911/telegraph/blob/main/docs/TELEGRAM.md): commands, `/register` and custody
- [[Run a node|Run-a-node]]: services, multi-node and committee onboarding

Register a handle: DM a node's bot `/register adz` (custodial, the node's
key owns it), or with your own key run `telegraph init`, then
`telegraph register --handle adz@nym --did did:key:adz` and `telegraph sync`.
`/card` returns your bio line (`⟁ adz@nym · 1a2b3c4d`) to tap and copy.
`/revoke adz` drops a handle you registered through the bot. `/stats`
shows network counts and how many early-adopter spots are left. The first
100 handles ever registered get "Founding 100"; handles 101–1000 get
"Early adopter".

Scale and recent fixes are in the
[stress report](https://github.com/ao3575911/telegraph/blob/main/docs/STRESS-REPORT.md)
(1k, 100k, 1M and 10M users). Main has the seven bugs found there:
crafted channel posts can no longer crash a node, a failed send is retried
instead of being marked published, `data.json` is written crash-safe, and
ingest and bot lookups stay fast at 100k users. The JSON store still caps
a node at around 1–2M users.

Install:

```sh
curl -fsSL https://raw.githubusercontent.com/ao3575911/telegraph/main/scripts/install.sh | sh
```

Status: pre-1.0 (0.3.1). The protocol and on-disk format may change between minor
versions. Known limits are tracked as
[issues](https://github.com/ao3575911/telegraph/issues).
