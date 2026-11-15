# Stress run outputs

Raw output of `go run ./cmd/stress` behind [STRESS-REPORT.md](../STRESS-REPORT.md),
with Go runtime stack dumps trimmed after out-of-memory exits.

| File | Code | Command |
|---|---|---|
| `before-1k.txt`, `before-100k.txt` | main `f8baf9d` | `-n 1000` / `-n 100000 -bot-users 30`, budget 60 s |
| `base-1k-extra.txt`, `base-100k-gossip.txt` | main `f8baf9d` | gossip and bot phases only |
| `final-1k.txt`, `final-100k.txt` | this branch | `-n 1000` / `-n 100000 -bot-users 2000` |
| `final-1m.txt` | this branch | `-n 1000000`, `ulimit -v 9000000`; the harness itself ran out of memory copying 1M entries in the swap phase |
| `final-1m-bot.txt` | this branch | `-n 1000000 -phases bot -bot-users 300` (cold verify cache) |
| `final-1m-cold.txt` | this branch | `-n 1000000 -phases registry`: whole-log verification with nothing cached |
| `final-2m-store.txt` | this branch | `-n 2000000 -phases ingest,save,load`, `ulimit -v 10000000` |
| `final-10m-gen.txt` | this branch | `-n 10000000`, holding every entry in memory, `ulimit -v 10000000` |
| `final-10m-stream.txt` | this branch | `-n 10000000 -phases stream`: signs every entry, keeps only hashes |

Machine: 8 vCPU, 16 GB RAM (about 8–12 GB free), Go 1.27.3, Linux.
