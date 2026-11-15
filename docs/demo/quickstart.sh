# Quickstart: three Telegraph nodes on one machine over a local file bus.
# The same commands as docs/QUICKSTART.md; CI runs this file.
# Needs `telegraph` on PATH (scripts/install.sh, or go install github.com/ao3575911/telegraph/cmd/telegraph@latest).
set -eu
export TG=/tmp/telegraph-quickstart && rm -rf "$TG" && mkdir -p "$TG"
export TELEGRAPH_BUS="$TG/bus.jsonl"
alice() { TELEGRAPH_HOME="$TG/alice" telegraph "$@"; }
bob()   { TELEGRAPH_HOME="$TG/bob"   telegraph "$@"; }
carol() { TELEGRAPH_HOME="$TG/carol" telegraph "$@"; }
gossip() { for n in alice bob carol alice bob carol; do $n sync --secs 0 --genesis >/dev/null; done; }

alice init && bob init && carol init
alice register --handle alice@nym --did did:key:alice
bob   register --handle bob@nym   --did did:key:bob
carol register --handle carol@nym --did did:key:carol
gossip
alice checkpoint; bob checkpoint; carol checkpoint
gossip
carol verify alice@nym
carol audit

LINE=$(alice card --handle alice@nym | head -1)
alice attest --line "$LINE"
gossip
carol scan "$LINE"

SID=$(alice swap offer --gives alice@nym --wants bob@nym --hours 1 | awk '/swap id/{print $3}')
gossip
bob swap accept --sid "$SID"
gossip
alice swap claim --sid "$SID"
gossip
bob swap claim --sid "$SID"
gossip
carol swap status --sid "$SID"
carol resolve alice@nym
