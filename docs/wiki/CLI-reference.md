# CLI reference

```
telegraph <command> [flags]
```

| Command | Flags | Does |
|---|---|---|
| `init` | | Create the identity key and local state in `TELEGRAPH_HOME` |
| `register` | `--handle H --did D` | Claim a handle for a DID |
| `revoke` | `--handle H` | Revoke a handle you own |
| `resolve H` | | Handle → DID, owner and state |
| `verify H` | | `resolve` plus checkpoint attestation |
| `audit` | | Check signatures, chains and the latest checkpoint |
| `sync` | `--secs N` (5), `--serve`, `--pin P`, `--bots`, `--channels`, `--bus`, `--checkpoint-only` | Publish pending entries and merge gossip |
| `checkpoint` | | Issue a checkpoint if this node leads the committee |
| `pin` | `--rotate` | Show the trust root, or advance it |
| `peers` | | Known peers and liveness |
| `net` | | Network health |
| `card` | `--handle H` | Print the bio card line |
| `attest` | `--line L` | Sign a card line |
| `scan L` | | Verify a card line |
| `swap offer` | `--gives H --wants H [--hours 24]` | Offer a swap and lock your handle |
| `swap accept` | `--sid ID` | Lock your handle to the initiator |
| `swap claim` | `--sid ID` | Take the handle locked to you |
| `swap reclaim` | `--sid ID` | Recover your handle after expiry |
| `swap status` | `--sid ID` | Show locks, claims and reclaims |
| `version` | | Print the version |

## Environment

| Variable | |
|---|---|
| `TELEGRAPH_HOME` | Node state directory (default `~/.telegraph`) |
| `TELEGRAPH_BOTS` | Comma-separated Telegram bot tokens |
| `TELEGRAPH_CHANNELS` | Comma-separated channels, e.g. `@chan1,@chan2` |
| `TELEGRAPH_PIN` | Checkpoint hash to bootstrap from |
| `TELEGRAPH_BUS` | Local file bus path, used instead of Telegram |
| `NO_COLOR` | Disable colour |
| `CLICOLOR_FORCE` | Force colour when stdout is not a terminal |

Exit status is 0 on success and 1 on any error; errors print as
`✗ telegraph: …` on stderr.
