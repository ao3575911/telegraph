# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- **`TELEGRAPH_COMMITTEE`: committee allowlist (protocol-affecting:
  configure every node identically and restart them together).** Any key
  that wrote a single entry could be picked for the 2-of-3 checkpoint
  committee. With the allowlist set, only listed keys are issuers; a
  checkpoint naming or signed by another key is kept for chain continuity
  but attests nothing (`ErrOffCommittee`: never the latest checkpoint, a
  pin or a finality witness; `audit` reports it). Existing checkpoints stay
  valid when their committee is within the list. `config` and `committee`
  show the allowlist; a serving node without one logs a warning.

- `TELEGRAPH_REGISTER_BOT` / `--register-bot`: a node with bot
  registration off names the bot that holds handles (e.g.
  `@dsmdioefncoedcbot`). Its `/register`, `/revoke`, `/card`, `/start`,
  `/help`, unknown-handle replies, command menu and bot descriptions point
  users there, with an Open button, instead of the generic CLI text.
  Ignored when the node registers itself. `telegraph config` shows it.
- `scripts/telegraph-boot.sh`: starts the supervisor for each
  `~/.config/telegraph/*.env` node that is not already running, for hosts
  with no systemd, launchd or cron. Idempotent and silent with `-q`, so it
  can run from `~/.bashrc`. `TELEGRAPH_BOOT=0` skips a node.

### Changed

- `telegraph resolve` exits 1 when the handle is unregistered or revoked
  (it printed the warning but exited 0), so scripts can test it.
- On a node with registration off, `/register` and `/revoke` in a group
  get the "this node does not hold handles" reply instead of "DM me".

### Fixed

- **The pin check is mandatory.** Following the README in order (`init`,
  `register`, then `sync`) skipped the trust check: it ran only on an
  empty store, so a node that registered first merged whatever the
  channel sent, unchecked. Every `sync` (and `sync --serve`) now checks
  the log against the pin first: a store holding only its own entries
  bootstraps with `--pin`; one holding other nodes' entries must contain
  the pinned, quorate checkpoint and audit clean; with no pin, `sync`
  stops. `--genesis` stages its window and merges nothing if it carries a
  checkpoint whose committee this node is not on; a node on the first
  committee pins that checkpoint automatically, and `--genesis` is ignored
  once a pin is set. New `--insecure-skip-pin` turns the check off with a
  warning on every run. A staged checkpoint now keeps co-signatures that
  arrive in later copies. README, quickstart and wiki: join with `--pin`
  first, then register.
- **Duplicate handles no longer split the network (protocol change:
  upgrade every node together).** When two nodes registered the same
  handle before syncing, each refused the other's register as a
  "backdated takeover", so they kept different logs, resolved the handle
  differently and failed verify and audit forever. Admission no longer
  depends on arrival order: every verifiable entry is kept and the
  ownership fold settles the handle identically on every node (earliest
  register wins; timestamp, signer chain, then entry hash). Backdated
  takeovers are still void: once a trusted checkpoint (at least two
  committee signatures, naming chain heads) covers a register, an
  uncovered register from another key dated at or before it never counts.
- `scripts/node-join.sh` skipped registering the node's handle, because
  `resolve` exited 0 for an unregistered handle; the node never became an
  issuer and could not join the committee. It now checks the output too.
- `scripts/node-join.sh` and `scripts/install-service.sh` ignored
  `TELEGRAPH_BIN` in the node's env file and ran whatever `telegraph` was
  on `PATH`. They now use it, and the systemd unit reads it per node.
  `install-service.sh` also no longer fails when `$USER` is unset.

## [0.3.0] - Unreleased

Production readiness. **Protocol change: upgrade every node** (see
"Changed").

### Changed

- **Handle ownership is first-come** (#15). A handle belongs to the key
  of its first valid register (in causal order); only that key's register
  (DID change), revoke or lock counts afterwards. Registers, revokes and
  locks from other keys are ignored by the fold, whatever their timestamp,
  so a later key can no longer take a handle over. A revoke by the owner
  frees the handle for the next register; a swap still moves ownership to
  the claimant, who is then the owner under the same rule. Nodes also
  refuse to admit a register from another key dated at or before the
  owner's tenure began (a backdated takeover). 0.2.x nodes still resolve
  last-writer-wins and can disagree with 0.3.0 about a handle two keys
  registered: upgrade all nodes together. The live network had no such
  conflicts when 0.3.0 was cut.
- `telegraph register` and `revoke` refuse handles owned by another key or
  locked in a swap instead of writing entries the network would ignore.
- The bot's `/register` follows the same rule: it never signs for a handle
  another key owns, binds custody to the requesting Telegram user before
  signing, and drops custody records for handles that were revoked or
  swapped away (they no longer count toward the 3-handle cap).
- Every bot reply rewritten: concise, current version, consistent errors
  (`⚠️ Usage: …`, unknown handle with a `/register` hint, `⏳` rate limit,
  one generic internal-error message). Internal errors are logged, never
  shown; a panic in a command is caught and answered generically.
- Without `--bot-register`, `/register`, `/revoke` and `/card` are left out
  of the command menu and `/help`.
- Logs are structured (`log/slog`): text by default,
  `TELEGRAPH_LOG_FORMAT=json` for JSON, `TELEGRAPH_LOG_LEVEL`,
  `TELEGRAPH_NODE_NAME` on every record; a startup record lists version,
  node key prefix, channels, peers and settings (never the token).
- `/resolve` and the bot's ownership checks use a per-handle index instead
  of scanning the whole log.

### Added

- `/card [name]`: your bio line `⟁ name@nym · 1a2b3c4d` in a code entity,
  so one tap copies it, for every handle you hold on the node. The node
  signs a card attestation the first time, so `telegraph scan` reports it
  VERIFIED. Clear reply when you hold no handle.
- The channel link is configuration: `TELEGRAPH_CHANNEL_URL`, default
  `https://t.me/tgraph_net`. `/start`, `/about` and the bot description use
  it; invite links are refused.
- The node sets its bot description and short description
  (`setMyDescription`, `setMyShortDescription`) on start, skipping
  unchanged ones.
- Graceful shutdown: SIGTERM/SIGINT abort the long poll, save the store and
  exit 0.
- Reboot-safe services (#18): `scripts/install-service.sh` installs a
  systemd user unit (`scripts/service/telegraph@.service`, restart always
  with 5 s to 5 min backoff, one instance per node) or a launchd agent, and
  `scripts/telegraph-supervise.sh` restarts a node with backoff where there
  is no systemd. Settings and the token come from
  `~/.config/telegraph/<node>.env` (0600), never unit files or arguments.
- Multi-node configuration: `TELEGRAPH_BOTS_FILE` (token file, must be
  0600), `TELEGRAPH_TOKEN_ENV` (name of the token variable),
  `TELEGRAPH_PEERS` (other committee nodes' keys).
- `telegraph committee` shows the next checkpoint committee, which member
  is this node or a peer, and warns when a node is not an issuer yet;
  `telegraph config [key]` prints settings without secrets, or the node key.
- `scripts/node-join.sh` and the runbook in `docs/NODE-OPERATIONS.md`
  onboard a second or third committee node: bootstrap from the pin,
  register the node's handle so it becomes an issuer, print its key for
  peers, install the service.
- `docs/PINNED-POST.md`: a draft pinned post for @tgraph_net.

### Fixed

- Poll recovery is logged once after a failure streak.
- The Telegram backoff sleep is interruptible, so shutdown never waits out
  a 35 s long poll.

## [0.2.3] - 2026-11-15

### Fixed

- Same-second entries were folded in hash order, which is effectively
  random. A register, swap offer and lock signed in one second could put
  the lock before the register that makes it valid, so about half the time
  the lock was ignored and `swap accept` failed with `lock … not in force
  (REGISTERED)` (#17). Entries sharing a second are now ordered by each
  signer's own chain (an entry follows its PrevHash ancestors), then by
  hash: deterministic, independent of arrival order, and causal. This also
  fixes existing logs, and `resolve` picks the newest of a signer's
  same-second register/revoke by chain order too.
- Every entry a node signs (register, revoke, card, offer, lock, claim,
  reclaim, heartbeat, bot `/register`) now gets a timestamp strictly after
  its signer's previous entry, so one signer never puts two entries in the
  same second. Nodes on v0.2.2 or earlier, which still order ties by hash,
  agree with v0.2.3 on everything signed from now on. Upgrade nodes that
  hold same-second swap history from older releases.

## [0.2.2] - 2026-11-15

### Added

- `/register name [did]` in a private chat with the bot: the node signs
  `name@nym` and gossips it to the channel. Registration is custodial (the
  node's key owns the handle) and off unless the node runs with
  `--bot-register` or `TELEGRAPH_BOT_REGISTER=1`. Without a DID the handle
  gets `did:tg:<telegram user id>`, which is public. Without a name the bot
  asks for one.
- `/revoke name`: revoke a handle you registered through the bot.
- The node records which Telegram user each bot-registered handle belongs
  to (`custody` in `data.json`, never gossiped); only that user can change
  its DID or revoke it. Handles owned by other keys, by the node operator or
  by another Telegram user are refused, as are groups. Three handles per
  Telegram user per node.
- `/stats` in the bot: active, revoked and total handles, registrars,
  nodes online, swaps, checkpoints, your handles on this node, and how many
  early-adopter spots are left. `/register` now tells you your handle's
  number on the network, with a badge for the first 100 (Founding 100) and
  the first 1000 (Early adopter).
- `cmd/stress`, a load harness that drives the real packages at 1k to
  10M users, and fuzz targets for frame reassembly, entry decoding and bot
  commands. Results are in `docs/STRESS-REPORT.md`.

### Changed

- `telegraph register` and the bot share one handle rule (one `@`, no
  spaces, at most 64 characters); the CLI now rejects handles that break it.

### Fixed

- Two crafted channel posts could crash every node: frames for one entry
  that disagreed on the part count indexed past the buffer, and a huge
  part count allocated memory up front. Reassembly is now bounded
  (32768 parts, 512 unfinished entries, 128 MiB).
- A failed send (Telegram 429, network outage) marked entries as published,
  so they never reached the channel. They now stay pending and the serving
  loop retries them.
- A flood of messages from 10,000 new chats reset every chat's rate limit.
  Only expired chats are pruned now.
- `data.json` is written atomically (temporary file, sync, rename), so a
  crash or full disk mid-write no longer corrupts the store. It is also
  written compact, about 17% smaller.

### Performance

- Admitting an entry is O(1) instead of a scan of the whole log, so a node
  ingesting 100k entries takes 4.6 s instead of an estimated 7 minutes;
  bootstrap staging is linear too.
- Gossip ingest saves `data.json` once per batch instead of once per entry
  (20k entries over the file bus: 10 min 13 s before, 0.7 s now). Idle
  polls no longer rewrite the file.
- Successful signature checks are memoised. Bot lookups no longer
  re-verify the whole log, and the checkpoint audit is cached between log
  changes: `/register` on a 100k-entry node went from 5.7 s to 8.5 ms (p50).

## [0.2.1] - 2026-11-15

### Fixed

- Direct messages never reached the node when Telegram kept an old update
  filter: every poll now sends `allowed_updates`.
- Long polls hung behind egress proxies: HTTP/1.1-only transport, 20 s
  long-poll with a per-request deadline (poll time + 15 s), idle
  connections dropped after a failure, and backoff with jitter (honouring
  `retry_after`).
- The bot token appeared in logged request errors; errors are redacted.
- Repeated identical errors flooded the log; each is logged at most once a
  minute with a repeat count.
- `/help` showed shell commands and CLI flags instead of the bot's
  commands; bot replies no longer contain CLI details.
- The bot's command menu was missing commands and advertised `/dump`.
- Any group could request a log dump or feed entries; only configured
  channels gossip now.
- `/resolve` without a handle gave only a usage line; it now asks for one.

### Added

- Command menu set on start (`setMyCommands`, default and private scopes)
  and menu button; `/help` and the menu come from one table.
- `/about` and `/version`; a handle sent on its own is resolved.
- Inline GitHub, Wiki, Channel and Release buttons.
- Groups: commands addressed to the bot (`/status@botname`) are answered as
  replies; other group messages are ignored.
- Rate-limit notice ("Try again in N s") once per window.
- `scripts/install.sh`: install a release without Go, with SHA-256
  verification.
- `docs/TELEGRAM.md`.

### Changed

- README install section: script first, then `go install` pinned to a
  version (`@latest` can lag behind new tags), then building from source.

## [0.2.0] - 2026-11-15

### Added

- Bot replies to direct messages on serving nodes: `/start` (what Telegraph
  is, install, join with the pin, links), `/help` (quickstart),
  `/resolve handle@nym` (owner, DID, status, attestation), `/pin` and
  `/status`. HTML replies with escaped input, rate-limited per chat; DM text
  never enters the gossip log.
- `sync --genesis` to start or join a network that has no checkpoint yet
  ([#2](https://github.com/ao3575911/telegraph/issues/2)).
- Fallback checkpoint issuers: the second and third committee members issue
  when the leader is 1 h or 2 h overdue
  ([#3](https://github.com/ao3575911/telegraph/issues/3)). Serving nodes
  check every 10 minutes.
- Tests for the Telegram client against a fake Bot API (`httptest`).
- `audit` reports late-delivered entries and covered entries not yet
  received.

### Changed

- Checkpoints name the head of every issuer chain they cover (`Heads`) and
  cover those chains instead of a timestamp cut. An entry delivered to the
  issuer after the checkpoint is now uncovered instead of breaking its root
  ([#1](https://github.com/ao3575911/telegraph/issues/1)). 0.1.0
  checkpoints keep the timestamp rule.
- A serving leader issues once the oldest uncheckpointed entry is
  `Interval` (6 h) old, rather than every 6 h of uptime.
- Direct messages are no longer parsed as gossip frames.

### Compatibility

- 0.1.0 nodes reject checkpoints issued by 0.2.0 nodes (unknown `Heads`
  field). Upgrade every node in a network together. Stored 0.1.0 data and
  checkpoints remain valid.

## [0.1.0] - 2026-11-15

### Added

- Signed, hash-chained per-issuer logs (Ed25519, SHA-256).
- CRDT registry: last-writer-wins handles with a canonical, order-free merge.
- Merkle checkpoints with a deterministic 3-member rotating committee and
  2-of-3 co-signing; checkpoint chains, fork and withholding detection.
- Pinned bootstrap with transactional staging, pin rotation and partition
  healing.
- HTLC-style atomic handle swaps with staggered expiries.
- Bio cards (`⟁ handle@nym · a1b2c3d4`) with signed attestations.
- Telegram Bot API transport, plus a local file bus (`TELEGRAPH_BUS`) for
  tests and demos.
- CLI with compact coloured output (honours `NO_COLOR`, off when not a
  terminal), a banner on help, and `version`.
- Multi-node simulation (`docs/demo/simulate.sh`) and its usage report.
- Quickstart, wiki sources and a GitHub Pages site under `docs/`.
- CI (build, gofmt, vet, race tests, staticcheck, simulation, quickstart),
  release workflow with binaries for Linux, macOS and Windows, and Pages
  deployment.
- Module path `github.com/ao3575911/telegraph`; Apache-2.0 licence.

### Fixed (relative to the initial draft)

- Local entries were never published by `sync`.
- Multi-member checkpoints were rejected before they could be co-signed,
  and co-signatures never propagated.
- Concurrent committee members forked the checkpoint chain; only the leader
  now issues.
- Checkpoint coverage raced entries stamped in the checkpoint's own second.
- A lock by a non-owner could freeze or take a handle.
- The CLI swap flow could not complete (missing give-lock, wrong swap id, no
  preimage for the responder).
- Committee selection used one-directional ring distance; it is now the
  shortest distance either way round the ring.
- Merge order changed the merged entry order.
- Auditor panics on short ids in hostile input.

[Unreleased]: https://github.com/ao3575911/telegraph/compare/v0.2.3...HEAD
[0.3.0]: https://github.com/ao3575911/telegraph/compare/v0.2.3...v0.3.0
[0.2.3]: https://github.com/ao3575911/telegraph/compare/v0.2.2...v0.2.3
[0.2.2]: https://github.com/ao3575911/telegraph/compare/v0.2.1...v0.2.2
[0.2.1]: https://github.com/ao3575911/telegraph/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/ao3575911/telegraph/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/ao3575911/telegraph/releases/tag/v0.1.0
