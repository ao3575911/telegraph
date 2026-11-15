# Demo environment: three Telegraph nodes on one machine, gossiping over a
# local file bus (TELEGRAPH_BUS) instead of Telegram. Source from the repo
# root:  . docs/demo/env.sh
# Then `alice …`, `bob …`, `carol …` run `telegraph …` as that node.

TG_DEMO=${TG_DEMO:-/tmp/telegraph-demo}
export TG_DEMO
rm -rf "$TG_DEMO" && mkdir -p "$TG_DEMO/bin"
go build -o "$TG_DEMO/bin/telegraph" ./cmd/telegraph || return 1
export PATH="$TG_DEMO/bin:$PATH"
export TELEGRAPH_BUS="$TG_DEMO/bus.jsonl"

alice() { TELEGRAPH_HOME="$TG_DEMO/alice" telegraph "$@"; }
bob()   { TELEGRAPH_HOME="$TG_DEMO/bob" telegraph "$@"; }
carol() { TELEGRAPH_HOME="$TG_DEMO/carol" telegraph "$@"; }

# sync-all: two gossip rounds across all nodes, one summary line.
sync-all() {
	local n
	for n in alice bob carol alice bob carol; do $n sync --secs 0 --genesis >/dev/null || return 1; done
	printf '↻ synced  '
	for n in alice bob carol; do
		printf '%s %s  ' "$n" "$(grep -c '"ha"' "$TG_DEMO/$n/data.json")"
	done
	printf 'entries\n'
}

# checkpoint-all: every node tries; only the committee leader issues.
checkpoint-all() {
	local n force=
	[ -t 1 ] && force=1 # keep colour through the pipe on a terminal
	for n in alice bob carol; do
		printf '%-6s ' "$n"
		CLICOLOR_FORCE=$force $n checkpoint | head -1
	done
}

# sid-of NODE: the swap id of NODE's latest offer (from its preimage store).
sid-of() { ls -t "$TG_DEMO/$1/preimages" | head -1; }

for n in alice bob carol; do $n init >/dev/null; done
