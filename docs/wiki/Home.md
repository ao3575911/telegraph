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

Register a handle: DM a node's bot `/register adz` (custodial, the node's
key owns it), or with your own key run `telegraph init`, then
`telegraph register --handle adz@nym --did did:key:adz` and `telegraph sync`.

Install:

```sh
curl -fsSL https://raw.githubusercontent.com/ao3575911/telegraph/main/scripts/install.sh | sh
```

Status: pre-1.0 (0.2.2). The protocol and on-disk format may change between minor
versions. Known limits are tracked as
[issues](https://github.com/ao3575911/telegraph/issues).
