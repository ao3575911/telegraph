# Quickstart

Three nodes on one machine, gossiping over a local file bus instead of
Telegram. Every command below is run by CI ([`docs/demo/quickstart.sh`](https://github.com/ao3575911/telegraph/blob/main/docs/demo/quickstart.sh)).

## Install

```sh
go install github.com/ao3575911/telegraph/cmd/telegraph@latest   # needs Go 1.22+
telegraph version
```

Or download a binary from the [releases](https://github.com/ao3575911/telegraph/releases).

## Set up three nodes

```sh
export TG=/tmp/telegraph-quickstart && mkdir -p "$TG"
export TELEGRAPH_BUS="$TG/bus.jsonl"                  # local bus instead of Telegram
alice() { TELEGRAPH_HOME="$TG/alice" telegraph "$@"; } # one home directory per node
bob()   { TELEGRAPH_HOME="$TG/bob"   telegraph "$@"; }
carol() { TELEGRAPH_HOME="$TG/carol" telegraph "$@"; }
gossip() { for n in alice bob carol alice bob carol; do $n sync --secs 0 >/dev/null; done; }
alice init && bob init && carol init                  # create keys and local state
```

## Register and sync

```sh
alice register --handle alice@nym --did did:key:alice  # claim a handle
bob   register --handle bob@nym   --did did:key:bob
carol register --handle carol@nym --did did:key:carol
gossip                                                 # publish and merge
```

## Checkpoint and verify

```sh
sleep 1                                     # checkpoints cover earlier seconds
alice checkpoint; bob checkpoint; carol checkpoint  # leader issues, others co-sign
gossip
carol verify alice@nym                      # ✓ ATTESTED by checkpoint …
carol audit                                 # ✓ root ✓ count ✓ committee ✓ chain
```

## Bio card

```sh
LINE=$(alice card --handle alice@nym | head -1)  # ⟁ alice@nym · 84e5554b
alice attest --line "$LINE"                      # sign the card line
gossip
carol scan "$LINE"                               # ✓ VERIFIED
```

## Swap handles

```sh
SID=$(alice swap offer --gives alice@nym --wants bob@nym --hours 1 | awk '/swap id/{print $3}')
gossip
bob swap accept --sid "$SID"    # bob locks bob@nym to alice
gossip
alice swap claim --sid "$SID"   # alice takes bob@nym, revealing the preimage
gossip
bob swap claim --sid "$SID"     # bob takes alice@nym with it
gossip
carol swap status --sid "$SID"  # both locks SWAPPED
carol resolve alice@nym         # owner is now bob's key
```

## Over Telegram

```sh
export TELEGRAPH_BOTS=<token> TELEGRAPH_CHANNELS=@yourchannel  # replaces TELEGRAPH_BUS
telegraph sync --serve           # gossip, heartbeat, checkpoint, answer /dump
telegraph pin                    # share this hash out of band
telegraph init && telegraph sync --pin <hash>   # a new node bootstraps from it
```

The Telegram transport is not exercised by CI.
