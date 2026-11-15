# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

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
[0.2.3]: https://github.com/ao3575911/telegraph/compare/v0.2.2...v0.2.3
[0.2.2]: https://github.com/ao3575911/telegraph/compare/v0.2.1...v0.2.2
[0.2.1]: https://github.com/ao3575911/telegraph/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/ao3575911/telegraph/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/ao3575911/telegraph/releases/tag/v0.1.0
