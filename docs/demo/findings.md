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

## Changed in 0.2.0

| Area | Problem | Fix |
|---|---|---|
| checkpoints | Coverage was by timestamp, so an entry stamped before a checkpoint but delivered to the issuer after it broke the checkpoint's root on other nodes ([#1](https://github.com/ao3575911/telegraph/issues/1)). | Checkpoints name the head of each issuer chain they cover (`Heads`); auditors recompute over those chains, and later entries are reported as uncovered or late instead of failing the root. |
| sync | A node with an empty store could not join a network that had no checkpoint yet ([#2](https://github.com/ao3575911/telegraph/issues/2)). | `sync --genesis` starts or joins such a network; it is refused when a pin is set. |
| checkpoints | No checkpoint was issued while the committee leader was offline ([#3](https://github.com/ao3575911/telegraph/issues/3)). | Committee members 2 and 3 issue in the leader's place once the oldest unchecked entry is `Interval` + 1 h or + 2 h old. |
| bot | Direct messages to the bot got no answer, and their text was handed to the gossip parser. | Private chats are answered (`/start`, `/help`, `/resolve`, `/pin`, `/status`), rate-limited per chat, and never ingested. |

## Open issues

- After a swap the new owner has no DID until they `register` the handle;
  `resolve` shows this explicitly
  ([#4](https://github.com/ao3575911/telegraph/issues/4)).
- The file bus is for local testing and demos only. It has no locking
  beyond single `O_APPEND` writes and is not a network transport
  ([#5](https://github.com/ao3575911/telegraph/issues/5)).
- The Telegram client is tested against a fake Bot API, not a live bot, in
  CI ([#6](https://github.com/ao3575911/telegraph/issues/6)).
- 0.1.0 nodes do not know the `Heads` field, so they reject checkpoints
  issued by 0.2.0 nodes. Upgrade every node in a network together.
