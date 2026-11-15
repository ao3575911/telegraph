package main

import (
	"encoding/json"
	"math/big"
	"os"

	"github.com/ao3575911/telegraph/internal/ui"
)

// cmdDoc is one command's help: `telegraph <cmd> -h` or `telegraph help <cmd>`.
type cmdDoc struct {
	name, usage, summary, details string
	json                          bool // accepts --json
}

var cmdDocs = []cmdDoc{
	{"init", "init", "create the identity key and local state", "Creates TELEGRAPH_HOME (default ~/.telegraph) with identity.key (the node key, keep it private and backed up) and data.json. Safe to rerun.", false},
	{"register", "register --handle H [--did D]", "claim or update a handle with this key",
		"--handle H   handle, e.g. alice@nym\n--did D      DID to bind (default: did:key derived from this node's key)\n\nFirst register wins a handle; only its owner can change it. Publish with `sync`.", false},
	{"revoke", "revoke --handle H", "release a handle this key owns", "--handle H   handle to revoke\n\nThe handle becomes free for anyone to register.", false},
	{"resolve", "resolve H [--json]", "handle → DID", "Prints the owner, DID, status and entry. Exits 1 when H is unregistered or revoked.", true},
	{"verify", "verify H [--json]", "resolve plus checkpoint attestation", "ATTESTED when the latest checkpoint covers H's entry; UNCOVERED when it is newer; exits 1 when unregistered, 2 when the log diverges.", true},
	{"audit", "audit [--json]", "check signatures, chains and the latest checkpoint", "Recomputes the checkpoint's root, count and committee and walks the checkpoint chain. Exits 1 on bad signatures, orphans or divergence.", true},
	{"sync", "sync [--serve] [--secs N] [--pin P | --genesis] [--insecure-skip-pin] [--bot-register] [--register-bot @bot] [--bus FILE]", "publish and merge gossip",
		"--pin P               trusted checkpoint hash (from an operator, out of band); required to join\n--genesis             start a network that has no checkpoint yet\n--insecure-skip-pin   skip the mandatory pin check (dangerous)\n--serve               stay up: gossip, heartbeat, checkpoint, answer /dump and DMs\n--secs N              poll window (default 5)\n--bot-register        let Telegram users /register (custodial)\n--register-bot @bot   where /register points users on a node without it\n--bus FILE            local file bus instead of Telegram\n\nTokens come from TELEGRAPH_BOTS (never --bots on a shared host).", false},
	{"checkpoint", "checkpoint", "issue a checkpoint if this node leads the committee", "Fallback members issue when the leader is overdue.", false},
	{"pin", "pin [--rotate]", "show or advance the trust root", "--rotate   advance to the latest quorum-verified checkpoint", false},
	{"peers", "peers", "known peers and liveness", "", false},
	{"net", "net [--json]", "network health", "Pin, last checkpoint, peer liveness and pin drift.", true},
	{"card", "card --handle H", "print the bio card line", "", false},
	{"attest", "attest --line L", "sign a card line", "", false},
	{"scan", `scan "⟁ handle@nym · a1b2c3d4"`, "verify a card line", "", false},
	{"swap", "swap offer|accept|claim|reclaim|status [flags]", "atomic handle swap", "Run `telegraph swap <step> -h` for each step's flags.", false},
	{"committee", "committee [--json]", "next checkpoint committee and peers", "Shows the TELEGRAPH_COMMITTEE allowlist, issuers and who leads the next checkpoint.", true},
	{"config", "config [key] [--json]", "effective node settings (no secrets)", "`config key` prints only this node's public key.", true},
	{"redact-dids", "redact-dids [--apply]", "replace did:tg:<user id> DIDs this node holds", "Stop the serving node first; publish with `sync`.", false},
	{"version", "version", "print the version", "", false},
}

func docFor(name string) (cmdDoc, bool) {
	for _, d := range cmdDocs {
		if d.name == name {
			return d, true
		}
	}
	return cmdDoc{}, false
}

// printCmdHelp prints one command's help.
func printCmdHelp(p *ui.Printer, d cmdDoc) {
	p.Printf("%s telegraph %s\n\n%s\n", p.Bold("usage:"), d.usage, d.summary)
	if d.details != "" {
		p.Printf("\n%s\n", d.details)
	}
	if d.json {
		p.Printf("\n--json   machine-readable output on stdout\n")
	}
}

func isHelpFlag(a string) bool { return a == "-h" || a == "--help" || a == "-help" }

// wantsHelp reports whether args ask for help (before any "--").
func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if isHelpFlag(a) {
			return true
		}
	}
	return false
}

// jsonMode is set by --json on commands that support it.
var jsonMode bool

// takeJSON removes --json from args for commands that accept it.
func takeJSON(name string, args []string) []string {
	d, ok := docFor(name)
	if !ok || !d.json {
		return args
	}
	out := args[:0:0]
	for _, a := range args {
		if a == "--json" || a == "-json" {
			jsonMode = true
			continue
		}
		out = append(out, a)
	}
	return out
}

// emitJSON prints v as indented JSON on stdout.
func emitJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	dieOn(enc.Encode(v))
}

// keyDID is the did:key for an Ed25519 public key: "z" + base58btc of the
// multicodec prefix 0xed 0x01 and the key (W3C did:key method).
func keyDID(pub []byte) string {
	return "did:key:z" + base58(append([]byte{0xed, 0x01}, pub...))
}

const b58 = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

func base58(b []byte) string {
	n := new(big.Int).SetBytes(b)
	mod, base := new(big.Int), big.NewInt(58)
	var out []byte
	for n.Sign() > 0 {
		n.DivMod(n, base, mod)
		out = append(out, b58[mod.Int64()])
	}
	for _, c := range b {
		if c != 0 {
			break
		}
		out = append(out, '1')
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}
