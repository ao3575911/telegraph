# Stress report: 1k, 100k, 1M and 10M users

Measured on 15 Nov 2026 with `cmd/stress`, on an 8 vCPU, 16 GB box running Go 1.27.3.
The harness drives the real packages in memory: `tlog`, `store`, `crdt`,
`handshake`, `checkpoint`, `merkle`, the bot `Responder` and the `Syncer` over a
`FileBus`. It does not touch Telegram. In this model each user is one CLI register
entry signed with that user's own key. The bot phase then registers extra users
through `/register` direct messages.

Raw outputs are in [docs/stress/](stress/). **Before** means main `f8baf9d`
(v0.2.2) and **after** means this branch. A number marked *est.* is an
extrapolation from measured parts and was not run. Telegram timings assume a bot
sends at most 20 messages a minute to one channel.

## Results by scale

| Metric | 1k | 100k | 1M | 10M |
|---|---|---|---|---|
| Sign one register entry per user | 138k/s | 190k/s | 190k/s | 194k/s (stream, 51 s) |
| Admit entries to a store (`AppendEntry`) | 89 → 46 ms | **≥7 min** (37,225 done in 60 s, quadratic) → **4.6 s** | 48 s | 2M: 96 s; 10M est. 8 min |
| RSS after admitting | 13 MB | 257 MB | 2.8 GB | 2M: 5.9 GB; 10M est. ≥29 GB |
| `data.json` size | 0.6 → 0.5 MB | 60.6 → 50.2 MB | 504 MB | est. 5.3 GB |
| Full `data.json` rewrite | 4 ms | 1.94 → 0.33 s | 5.4 s | 2M: **out of memory** (7.5 GB in use) |
| `store.Open` (startup load) | 4 → 3 ms | 595 → 298 ms | 3.6 s | est. 36 s |
| Verify the whole log, cold | 44 ms | 5.9 s | 45 s | est. 7.5 min |
| Resolve one handle (in memory) | p50 51 → 12 µs | 2.2 → 1.9 ms | 44 ms | est. 0.4 s |
| `checkpoint.Audit` | 68 → 4 ms | 6.2 → 0.4 s | 5.9 s | n/a |
| Checkpoint entry (lists every chain head) | 68 KB, 25 frames | 6.7 MB, 2,394 frames | 67 MB, 23,929 frames | est. 670 MB, 239k frames |
| …time to publish it | 75 s | **2 h** | **20 h** | est. **8.3 days** |
| Merkle root | <1 ms | 53 ms | 0.73 s | 9.4 s (measured) |
| Pin check on a bootstrap dump (`DumpVerdict`) | 47 → 6 ms | 7.7 → 0.8 s | 11.4 s | n/a |
| Merge 3 partial nodes (CRDT) | 1 ms, converged | 0.3 s, converged | 4.7 s, converged | n/a |
| Swaps: wrong owner after 1,000 swaps (checked 200) | 0 | 0 | 0 (resolve p50 77 ms) | n/a |
| Gossip ingest over FileBus with saves | 1k: 1.47 s → 13 ms | 20k: **10 min 13 s → 0.70 s** | 20k: 0.71 s | n/a |
| Bot `/register`, p50 | 52 ms → **0.17 ms** | **5.7 s → 8.5 ms** | 194 ms (before: est. ≥45 s) | n/a |
| Bot `/resolve`, p50 | 121 ms → 22 µs | est. 12 s → **2.2 ms** | 51 ms; first call after restart 56 s | n/a |
| Bot `/stats` (new), p50 | 0.4 ms | 32 ms | 0.94 s | n/a |
| Publish every entry once | 50 min | 83 h | 35 days | **347 days** |

Stayed correct at every scale tested:

- Merge order did not change the result.
- No swap ended with the wrong owner.
- The 3-handle cap held.
- The rate limit answered 5 messages, sent 1 notice, then dropped the rest.
- `-race` was clean.

### Where 10M broke

There were two attempts:

- **Full node model (entries, index, `data.json`).**
  - 1M works end to end, with peak RSS of 4.8 GB during save.
  - 2M admitted fine at 5.9 GB RSS. The first `Save` then ran out of memory, because `json.Marshal` builds the whole 1 GB document plus growth copies in RAM.
  - 10M needs an estimated ≥29 GB of RAM and a 5.3 GB `data.json` rewrite on every change. It cannot run on this box, and the JSON store is the limit.
- **Holding 10M signed entries in memory**, without a store, ran out of memory just past 2M entries, at 8 GB of address space.
- **Streaming 10M** signed every entry and kept only the hashes. That gave the measured 10M column:
  - signing: 51 s
  - wire size: 3.0 GiB
  - TGP1 frames: 10M
  - Merkle root: 9.4 s
  - hash index: 2.0 s, at 3 GB RSS

## Bugs found

File and line numbers refer to main `f8baf9d`.

| # | Severity | Where | What happened | Status |
|---|---|---|---|---|
| 1 | **Critical** | `internal/transport/frame.go:66-69` | Two crafted frames crash every node. Frames for one id that disagree on `total` index past the buffer (fuzz found `index out of range [5] with length 3`), and `total=2147483647` allocates 16 GB up front. Unfinished entries were never evicted. | Fixed: at most 32,768 parts, 512 unfinished entries and 128 MiB; mismatched totals rejected. Fuzzed 2.5 min after the fix. |
| 2 | High | `internal/transport/sync.go:87` | `PublishPending` marked an entry seen even when every send failed (a 429 or an outage), so it never reached the channel. | Fixed: it stays pending, and the serve loop retries it. |
| 3 | High | `internal/store/store.go:104` (`has`), `:181`, `:222` | Every admission scanned and hex-encoded the whole log, which made ingest O(n²). 100k entries took about 7 min. Bootstrap staging was O(n²) too. | Fixed: hash index and staged set. |
| 4 | High | `internal/transport/sync.go:119`, `:258` | `data.json` was fully rewritten after **every** gossiped entry and on every idle poll. Catching up on 20k entries took 10 min. | Fixed: one save per batch of updates; idle polls skip the save. |
| 5 | High | `internal/transport/dm.go:362,377,422,429`, `register.go:193` | Every `/register`, `/resolve` and `/status` re-verified every signature (`st.Registry()`) and re-ran `Audit`, which is O(n) ed25519 work per message. One bad entry anywhere broke every `/resolve`. | Fixed: memoised verification, lookups through `handshake.Owner` (which verifies the handle's own entries), and `Audit` cached until the log changes. |
| 6 | Medium | `internal/store/store.go:70-74` | `os.WriteFile` writes in place, so a crash or full disk mid-save corrupts `data.json`. | Fixed: temporary file, sync, then rename; also written compact (17% smaller). |
| 7 | Medium | `internal/transport/dm.go:112` | Once more than 10,000 chats had messaged the bot, every rate limit was cleared. A flood of new chats gave everyone fresh limits (measured: the victim was answered 5, then 5 again within one window). | Fixed: only expired chats are pruned; newcomers are refused while 10,000 live chats are tracked. |
| 8 | Design | `internal/checkpoint/heads.go:21`, `tlog/entry.go:59` | A checkpoint lists every issuer's chain head, which grows O(users): 67 MB and 20 h to publish at 1M users, against a 6 h checkpoint interval. | Not fixed. See P0 below. |
| 9 | Design | `internal/handshake/owner.go:40-47` | `Owner` scans the whole log for each lookup: 44 ms at 1M. | Not fixed. P1: per-handle index. |
| 10 | Design | `internal/transport/register.go:140,185`, `store.go` | Each bot registration rewrites `data.json` twice: 5.4 s each at 1M. | Not fixed. P0: storage engine. |
| 11 | Medium | `internal/transport/frame.go:69` | A channel member who sees part 0 of an entry can post a fake part 1 first. The real part is dropped as a duplicate, and the entry fails to verify until a `/dump`. | Not fixed. P2: keep alternative chunks per slot and try them on verify failure. |
| — | Known | `internal/handshake/owner.go` `compute` | [#15](https://github.com/ao3575911/telegraph/issues/15): a later register from any key takes over a handle. | Fixed in 0.3.0: first-come ownership, owner-only updates, backdated takeovers refused at ingest. |
| — | Known | `docs/demo/simulate.sh` | [#17](https://github.com/ao3575911/telegraph/issues/17): same-second entries can drop a swap lock (Simulation flake). | Not reproduced here: `simulate.sh` gave 66 ok, 0 unexpected. Left open. |

Fuzzing that found nothing:

- `FuzzDecodeVerify`: 4 min (28M runs in the first 2), no panic, and nothing verified without a valid signature.
- `FuzzHandle`: 5.5 min (18M runs in the first 3), no panic and no empty reply.

## Prioritised fix list

1. **P0, checkpoint size.** Commit to chain heads with a Merkle root, and gossip the list separately or not at all. A checkpoint should be a constant ~1 KB. This needs a protocol version bump; see "Versioned protocol".
2. **P0, storage.** Replace the single JSON file with an append-only, length-prefixed entry log plus a small state file (pin, peers, custody, seen watermark). Each change then costs O(entry), not O(log), and memory stops being bounded by `json.Marshal`. This is stdlib-only (`os`, `bufio`, `encoding/binary`).
3. **P1, indexes.** Keep per-handle entry lists and a verified watermark in the store, so lookups cost O(entries for the handle). A restart then skips the 45 s re-verification at 1M, or runs it in the background.
4. **P1, Telegram throughput.** One channel carries about 28,800 messages a day, so a channel saturates at a few thousand registrations a day. Batch several small entries per message (a TGP2 frame) and shard channels by handle prefix.
5. **P2:** frame-slot poisoning (#11); [#15](https://github.com/ao3575911/telegraph/issues/15) ownership; `/stats` and the `/register` ordinal are O(n) (0.9 s at 1M); cache them on log length.

## Professionalising

- **Structured logs:** `log/slog` with JSON output, and fields `entry`, `peer`, `kind`, `took_ms`, with no token in any field.
- **Metrics:** an opt-in `--metrics 127.0.0.1:9464` that serves Prometheus text through `net/http`:
  - entries, handles and pending
  - publish failures and 429s
  - poll latency
  - save duration
  - verify cache hits
- **Storage engine:** as in P0 above, plus `telegraph compact`, which rewrites the log without superseded heartbeats.
- **Sharding and compaction:** shard channels by handle prefix. Heartbeats older than the pinned checkpoint can be dropped from the gossip set.
- **systemd unit:** `contrib/telegraph.service` with `Restart=always`, `MemoryMax=`, `ProtectSystem=strict` and an `EnvironmentFile` for the bot token. This would replace the hand-run `serve.sh`, which does not survive a reboot.
- **Versioned protocol:** frames already carry `TGP1`. Add a `v` field to entries, have nodes reject unknown major versions, and document the format in `docs/PROTOCOL.md`.

## Interaction and gamification

**Shipped in this PR:**

- `/stats` shows active, revoked and total handles, registrars, nodes online, swaps, checkpoints, how many of 3 handles you hold here, and the number of Founding 100 or Early adopter spots left.
- `/register` now replies with "🏅 Founding 100: handle #N on the network". Handles #101–#1000 get "🎖 Early adopter".
- The counts come from the signed log, so they are verifiable and not invented.

**Proposed next:**

- Handle rarity badges: 1–2 characters legendary, 3 rare, 4–5 uncommon.
- Node uptime streaks from heartbeats, shown in `/status`.
- A weekly channel digest: new handles, swaps and the top registrars.
- Achievements: first swap, first co-signed checkpoint, a 30-day node streak.
- A `/leaderboard` of nodes by co-signed checkpoints.

None of these needs new trust: each can be derived from entries every node already holds.

## Reproduce

```
go run ./cmd/stress -n 100000 -budget 60s -dir /tmp            # all phases
go run ./cmd/stress -n 10000000 -phases stream                 # 10M without holding entries
go test ./internal/transport -run XXX -fuzz FuzzAssembler -fuzztime 2m
```
