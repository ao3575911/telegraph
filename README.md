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
| First-come ownership | The first register owns a handle; only its key can update, revoke or swap it. |
| Telegram bot | A serving node answers `/resolve`, `/status` and `/pin`, and can register handles for people (`/register`, `/card`; custodial and opt-in). [Bot docs](docs/TELEGRAM.md) · channel [@tgraph_net](https://t.me/tgraph_net) |

Docs: [quickstart](docs/QUICKSTART.md) · [wiki](https://github.com/ao3575911/telegraph/wiki) ·
[site](https://ao3575911.github.io/telegraph/). More recordings: [help](docs/demo/help.gif) · [sync and merge](docs/demo/sync.gif) ·
[checkpoint and audit](docs/demo/checkpoint.gif). A measured run of every
command is in the [usage report](docs/USAGE-REPORT.md).

## Install

**Script** (Linux and macOS, no Go needed): downloads the latest release
for your OS and CPU, verifies it against `SHA256SUMS`, and installs to
`~/.local/bin` (set `BINDIR` to change it, `TELEGRAPH_VERSION` to pin one).

```sh
curl -fsSL https://raw.githubusercontent.com/ao3575911/telegraph/main/scripts/install.sh | sh
telegraph version
```

**Go 1.22+** (the newest tag; for a reproducible install pin one, e.g.
`@v0.3.1`, and run the same version on every node of a network):

```sh
go install github.com/ao3575911/telegraph/cmd/telegraph@latest
```

**From source:**

```sh
git clone https://github.com/ao3575911/telegraph && cd telegraph
go build -o telegraph ./cmd/telegraph && ./telegraph version
```

Windows: download the `.zip` from the
[latest release](https://github.com/ao3575911/telegraph/releases/latest).
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

**Join an existing network** (e.g. @tgraph_net). Get its pin, the
hash of a checkpoint, from an operator out of band (a node's `/pin`, or
`telegraph pin` on a node you trust), then:

```sh
telegraph init
export TELEGRAPH_BOTS=<token>[,<token>…] TELEGRAPH_CHANNELS=@chan1[,@chan2…]
telegraph sync --pin <checkpoint-hash>   # 1. bootstrap: verify peers' history against the pin
telegraph register --handle you@nym --did did:key:z6Mk…
telegraph sync                           # 2. publish; later syncs re-check the pin
telegraph sync --serve                   # stay up: gossip, heartbeat, checkpoint, answer /dump
```

The pin check is mandatory: `sync` refuses to merge gossip until the log
has been verified against a pin (registering first is fine; the node then
bootstraps around its own entries). `--insecure-skip-pin` turns the check
off, with a warning on every run; never use it on a live network.

**Start a new network** (no checkpoint exists yet, so no pin):

```sh
telegraph init
telegraph register --handle you@nym --did did:key:z6Mk…
telegraph sync --genesis    # first nodes only; refused once the network has a checkpoint you did not sign
telegraph pin               # after the first checkpoint: the pin to share out of band
```

A `--genesis` node pins the first checkpoint whose committee it is on and
from then on runs the normal check.

A serving node also runs its bot for people, with every command in
Telegram's command menu and rate-limited:

| Bot command | |
|---|---|
| `/resolve handle@nym` | owner, DID, status and checkpoint attestation |
| `/register name [did]` | register `name@nym`, signed by this node for you (custodial; DM only; needs `--bot-register`) |
| `/card [name]` | your bio line `⟁ name@nym · 1a2b3c4d`, tap to copy |
| `/revoke name` | release a handle you hold through the bot |
| `/status` · `/stats` · `/pin` | node and network status · network counts and early-adopter spots · the trust pin |
| `/about` · `/version` · `/help` · `/start` | about, version, every command, welcome |

Text people send is never parsed as gossip: the only entries the bot
writes are `/register` and `/revoke`, signed by the node. See
[docs/TELEGRAM.md](docs/TELEGRAM.md).

The Telegram transport is the Bot API (`getUpdates`, `sendMessage`). The
test suite exercises it against a fake Bot API server; multi-node tests and
demos use the file bus.

## Run a node

Keep a serving node up across crashes and reboots with a user service (no
sudo; systemd on Linux, launchd on macOS):

```sh
sh scripts/install-service.sh node1     # writes ~/.config/telegraph/node1.env (0600)
$EDITOR ~/.config/telegraph/node1.env   # paste the bot token
sh scripts/install-service.sh node1     # installs and starts telegraph@node1
systemctl --user status telegraph@node1
journalctl --user -u telegraph@node1 -f
```

It restarts on failure with backoff, survives Telegram timeouts, and stops
cleanly on SIGTERM. Joining a second committee node:
`sh scripts/node-join.sh node2 <pin> node2`. Full runbook, multi-node
settings and hosts without systemd:
[docs/NODE-OPERATIONS.md](docs/NODE-OPERATIONS.md).

## Commands

| Command | |
|---|---|
| `init` | create the identity key and local state in `TELEGRAPH_HOME` (default `~/.telegraph`) |
| `register --handle H [--did D]` · `revoke --handle H` | claim or revoke a handle; `--did` defaults to this key's `did:key` |
| `resolve H` · `verify H` | resolve a handle; `verify` adds checkpoint attestation |
| `audit` | verify signatures, chains and the latest checkpoint |
| `sync [--serve] [--secs N] [--pin P \| --genesis] [--bot-register]` | publish and merge gossip after the mandatory pin check; `--pin` joins (bootstraps) a network, `--genesis` starts one with no checkpoint yet; `--bot-register` turns on the bot's `/register` |
| `checkpoint` | issue a checkpoint if this node leads the committee, or as a fallback when the leader is overdue |
| `pin [--rotate]` · `peers` · `net` | trust root, peers, network health |
| `card --handle H` · `attest --line L` · `scan L` | bio cards |
| `swap offer\|accept\|claim\|reclaim\|status` | atomic handle swap |
| `committee` · `config [key]` | next checkpoint committee and peers · effective settings (no secrets) |
| `version` | print the version |
| `<command> -h` · `help <command>` | per-command flags and details; `--json` on `resolve`, `verify`, `audit`, `net`, `committee`, `config` |

Output is coloured on a terminal. Colour is off when stdout is not a
terminal or `NO_COLOR` is set; `CLICOLOR_FORCE=1` forces it on.

## Development

```sh
gofmt -l . && go vet ./... && go test -race ./...
bash docs/demo/simulate.sh            # regenerates docs/USAGE-REPORT.md
vhs docs/demo/main.tape               # re-record a demo (charmbracelet/vhs)
```

## Status

Pre-1.0 (0.3.1). The protocol and on-disk format may change. Known limits
are listed in the [usage report](docs/USAGE-REPORT.md#open-issues).

## Licence

[Apache-2.0](LICENSE) © 2026 Adam (ao3575911)
