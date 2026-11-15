# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

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

[Unreleased]: https://github.com/ao3575911/telegraph/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/ao3575911/telegraph/releases/tag/v0.1.0
