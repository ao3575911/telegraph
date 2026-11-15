<p align="center"><img src="docs/banner.svg" alt="Telegraph" width="100%"></p>

<p align="center">
  <a href="https://github.com/ao3575911/telegraph/actions/workflows/ci.yml"><img src="https://github.com/ao3575911/telegraph/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/ao3575911/telegraph/releases"><img src="https://img.shields.io/github/v/release/ao3575911/telegraph?sort=semver" alt="Release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/licence-Apache--2.0-blue" alt="Apache-2.0 licence"></a>
</p>

<p align="center"><img src="docs/demo/main.gif" alt="Three nodes register, gossip, checkpoint and verify" width="900"></p>

**Telegraph is a decentralised handle registry: signed, hash-chained logs gossiped over Telegram channels and attested by rotating 2-of-3 checkpoint committees.**

No blockchain and no server you do not run yourself. One static Go binary,
standard library only.

## What it does

| Feature | |
|---|---|
| Signed logs | Every entry is Ed25519-signed and hash-chained to its issuer's previous entry. |
| Convergence | Handles resolve by a deterministic CRDT fold, so any two nodes holding the same entries agree, whatever the arrival order. |
| Checkpoints | A Merkle root over the log, issued by the leader of a 3-key committee chosen from the log itself and valid once 2 of 3 have signed. Audits detect forks, splices and withheld checkpoints. |
| Pinned bootstrap | A new node trusts only a checkpoint hash shared out of band; a dump without it is rejected as a whole. |
| Atomic swaps | Two owners exchange handles with an HTLC-style hashlock and staggered expiries; either both move or neither does. [Demo](docs/demo/swap.gif) |
| Bio cards | `⟁ alice@nym · 1a2b3c4d` in a profile, verifiable against the owner's signed attestation. [Demo](docs/demo/card.gif) |

Docs: [quickstart](docs/QUICKSTART.md) · [wiki](https://github.com/ao3575911/telegraph/wiki) ·
[site](https://ao3575911.github.io/telegraph/). More recordings: [help](docs/demo/help.gif) · [sync and merge](docs/demo/sync.gif) ·
[checkpoint and audit](docs/demo/checkpoint.gif). A measured run of every
command is in the [usage report](docs/USAGE-REPORT.md).

## Install

Requires Go 1.22 or later.

```sh
go install github.com/ao3575911/telegraph/cmd/telegraph@latest
telegraph version
```

Prebuilt binaries for Linux, macOS and Windows are attached to each
[release](https://github.com/ao3575911/telegraph/releases).
See the [quickstart](docs/QUICKSTART.md) for a complete local run.

## Quickstart (local, no Telegram)

Three nodes on one machine, gossiping over a local file bus. These are the
commands the recordings and `docs/demo/simulate.sh` run.

```sh
. docs/demo/env.sh        # builds telegraph; defines alice, bob, carol, sync-all
alice register --handle alice@nym --did did:key:alice
bob   register --handle bob@nym   --did did:key:bob
carol register --handle carol@nym --did did:key:carol
sync-all                  # each node publishes and merges
sleep 1                   # checkpoints cover entries from earlier seconds
checkpoint-all            # the committee leader issues, the others co-sign
sync-all
carol verify alice@nym    # ✓ ATTESTED by checkpoint …
carol audit               # ✓ root ✓ count ✓ committee ✓ chain
```

`alice …` is shorthand for `TELEGRAPH_HOME=/tmp/telegraph-demo/alice telegraph …`
with `TELEGRAPH_BUS` set.

## Usage over Telegram

Create one or more bots with [@BotFather](https://t.me/BotFather), add them
to one or more channels, then on each node:

```sh
telegraph init
telegraph register --handle you@nym --did did:key:z6Mk…
export TELEGRAPH_BOTS=<token>[,<token>…] TELEGRAPH_CHANNELS=@chan1[,@chan2…]
telegraph sync              # publish pending entries, merge for --secs (default 5)
telegraph sync --serve      # stay up: gossip, heartbeat, checkpoint, answer /dump
telegraph pin               # print the trust root to share out of band
```

A new node bootstraps from peers that are serving:

```sh
telegraph init && telegraph sync --pin <checkpoint-hash>
```

The Telegram transport is the Bot API (`getUpdates`, `sendMessage`); it is
not exercised by the test suite, which uses the file bus.

## Commands

| Command | |
|---|---|
| `init` | create the identity key and local state in `TELEGRAPH_HOME` (default `~/.telegraph`) |
| `register --handle H --did D` · `revoke --handle H` | claim or revoke a handle |
| `resolve H` · `verify H` | resolve a handle; `verify` adds checkpoint attestation |
| `audit` | verify signatures, chains and the latest checkpoint |
| `sync [--serve] [--secs N] [--pin P]` | publish and merge gossip |
| `checkpoint` | issue a checkpoint if this node leads the committee |
| `pin [--rotate]` · `peers` · `net` | trust root, peers, network health |
| `card --handle H` · `attest --line L` · `scan L` | bio cards |
| `swap offer\|accept\|claim\|reclaim\|status` | atomic handle swap |
| `version` | print the version |

Output is coloured on a terminal. Colour is off when stdout is not a
terminal or `NO_COLOR` is set; `CLICOLOR_FORCE=1` forces it on.

## Development

```sh
gofmt -l . && go vet ./... && go test -race ./...
bash docs/demo/simulate.sh            # regenerates docs/USAGE-REPORT.md
vhs docs/demo/main.tape               # re-record a demo (charmbracelet/vhs)
```

## Status

Pre-1.0 (0.1.0). The protocol and on-disk format may change. Known limits
are listed in the [usage report](docs/USAGE-REPORT.md#open-issues).

## Licence

[Apache-2.0](LICENSE) © 2026 Adam (ao3575911)
