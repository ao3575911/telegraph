## Rough edges found and fixed

Found while getting the tests green and while running this simulation.
Each is fixed in 0.1.0 and covered by a test.

| Area | Problem | Fix |
|---|---|---|
| gossip | Locally created entries were marked "seen" on creation, so `sync` never published them. | `store.AppendEntry` no longer marks entries seen; only entries received from the wire are (`AppendReceived`). |
| checkpoints | A checkpoint with a 2- or 3-member committee fails `Verify` until co-signed, so the store rejected it and no multi-node checkpoint could ever form. | Stores keep checkpoints that only lack quorum (`tlog.IsPendingQuorum`); audit reports them as pending, not bad. |
| checkpoints | Co-signatures never reached the issuer: the co-signed copy has the same hash and was dropped as a duplicate. | A known checkpoint arriving again merges any new, valid committee co-signatures (`tlog.ValidCosig`). |
| checkpoints | Every committee member issued its own checkpoint for the same log, creating sibling forks. | Only the committee leader (closest key to the target) issues; the others co-sign. `checkpoint` refuses to re-checkpoint an unchanged log. |
| checkpoints | Coverage used `TS <= checkpoint TS`, so entries created in the checkpoint's own second broke the root. | A checkpoint covers entries stamped strictly before its second (`checkpoint.Covered`), for issuer and auditor alike. |
| swaps | Anyone could publish a lock for any handle and freeze or take it. | `handshake.Owner` honours a lock only if its signer owned the handle just before it. |
| swaps | `swap offer` never locked the initiator's handle, and `swap accept` built its lock with a different swap id, so no swap could complete from the CLI. | `offer` also writes the give-lock (expiry + 48 h); `accept` rebuilds the full terms, checks the swap id and the stagger. |
| swaps | The responder had no way to obtain the preimage. | `swap claim` reads the preimage revealed by the initiator's claim. |
| resolve | `resolve` after a swap crashed on an empty entry. | Resolve falls back to the new owner's claim entry. |
| audit | Short or empty hashes in hostile input could panic the auditor (`[:8]` slicing). | All short ids go through a length-safe helper. |
| CLI | Commands on an uninitialised home failed with a raw file error. | They now say to run `telegraph init`. |

## Open issues

- Coverage is by timestamp. An entry stamped before a checkpoint but
  delivered to the issuer after it makes the checkpoint fail audit on other
  nodes (`root`/`count` mismatch) until the next checkpoint covers it.
- A node with an empty store cannot `sync` without `--pin`. On a brand-new
  network with no checkpoint yet, a node must register a handle before its
  first sync. Found while recording the sync demo; the error now says so.
- If the committee leader is offline, no checkpoint is issued for that log
  state; there is no fallback issuer yet.
- After a swap the new owner has no DID until they `register` the handle;
  `resolve` shows this explicitly.
- The file bus is for local testing and demos only. It has no locking
  beyond single `O_APPEND` writes and is not a network transport.
