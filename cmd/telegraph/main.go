// Command telegraph is the CLI for the Telegraph handle registry.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ao3575911/telegraph/internal/store"
	"github.com/ao3575911/telegraph/internal/tlog"
	"github.com/ao3575911/telegraph/internal/ui"
)

// Version is the release version, overridable at build time with
// -ldflags "-X main.Version=...".
var Version = "0.3.1"

const tagline = "decentralised handle registry gossiped over Telegram"

var (
	out    = ui.New(os.Stdout, ui.FromEnv(os.Stdout))
	errOut = ui.New(os.Stderr, ui.FromEnv(os.Stderr))
)

func main() {
	if len(os.Args) < 2 {
		help(out)
		os.Exit(2)
	}
	dir := envOr("TELEGRAPH_HOME", defaultHome())
	args := os.Args[2:]
	setupLogging()
	comm, err := committeeKeys()
	dieOn(err)
	tlog.SetCommittee(comm)

	// swap steps have their own flag help (`swap offer -h`)
	if d, ok := docFor(os.Args[1]); ok && wantsHelp(args) && (os.Args[1] != "swap" || isHelpFlag(args[0])) {
		printCmdHelp(out, d)
		return
	}
	args = takeJSON(os.Args[1], args)

	switch os.Args[1] {
	case "help", "-h", "--help":
		if len(args) == 1 {
			if d, ok := docFor(args[0]); ok {
				printCmdHelp(out, d)
				return
			}
		}
		help(out)
	case "version", "--version":
		out.Println("telegraph " + Version)
	case "init":
		doInit(dir)
	case "register":
		doRegister(dir, args)
	case "revoke":
		doRevoke(dir, args)
	case "resolve":
		doResolve(dir, oneArg("resolve <handle>", args))
	case "verify":
		doVerify(dir, oneArg("verify <handle>", args))
	case "audit":
		doAudit(dir)
	case "sync":
		doSync(dir, args)
	case "checkpoint":
		doSync(dir, append([]string{"--checkpoint-only"}, args...))
	case "pin":
		doPin(dir, args)
	case "peers":
		doPeers(dir)
	case "net":
		doNet(dir)
	case "card":
		doCard(dir, args)
	case "attest":
		doAttest(dir, args)
	case "scan":
		doScan(dir, oneArg(`scan "⟁ handle@nym · a1b2c3d4"`, args))
	case "swap":
		doSwap(dir, args)
	case "committee":
		doCommittee(dir)
	case "config":
		doConfig(dir, args)
	case "redact-dids":
		doRedactDIDs(dir, args)
	default:
		errOut.Fail("unknown command %q", os.Args[1])
		usage(errOut)
		os.Exit(2)
	}
}

func oneArg(form string, args []string) string {
	if len(args) != 1 {
		die(fmt.Errorf("usage: telegraph %s", form))
	}
	return args[0]
}

// help prints the banner and usage. The banner is decoration: Banner
// returns "" when colour is off (not a TTY, or NO_COLOR set).
func help(p *ui.Printer) {
	p.Printf("%s", p.Banner(Version, tagline))
	usage(p)
}

func usage(p *ui.Printer) {
	cmds := make([][]string, 0, len(cmdDocs))
	for _, d := range cmdDocs {
		cmds = append(cmds, []string{d.name, d.summary})
	}
	p.Printf("%s telegraph <command> [flags]\n\n", p.Bold("usage:"))
	rows := make([][]string, len(cmds))
	for i, c := range cmds {
		rows[i] = []string{p.Cyan(c[0]), p.Dim(c[1])}
	}
	p.Table("  ", rows)
	p.Printf("\n%s telegraph <command> -h   %s\n", p.Bold("help:"), p.Dim("flags and details; --json on resolve verify audit net committee config"))
	p.Printf("\n%s TELEGRAPH_HOME TELEGRAPH_BOTS[_FILE] TELEGRAPH_CHANNELS TELEGRAPH_CHANNEL_URL\n"+
		"     TELEGRAPH_PEERS TELEGRAPH_COMMITTEE TELEGRAPH_BOT_REGISTER TELEGRAPH_REGISTER_BOT TELEGRAPH_LOG_FORMAT TELEGRAPH_PIN\n"+
		"     TELEGRAPH_BUS NO_COLOR\n"+
		"     (docs/NODE-OPERATIONS.md)\n", p.Bold("env:"))
}

// --- helpers ---

func defaultHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		die(err)
	}
	return filepath.Join(home, ".telegraph")
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func dataPath(dir string) string { return filepath.Join(dir, "data.json") }
func keyPath(dir string) string  { return filepath.Join(dir, "identity.key") }

func loadKey(dir string) (ed25519.PrivateKey, error) {
	p := keyPath(dir)
	if b, err := os.ReadFile(p); err == nil {
		if len(b) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("bad keyfile %s", p)
		}
		return ed25519.PrivateKey(b), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, priv, 0o600); err != nil {
		return nil, err
	}
	return priv, nil
}

// mustHome stops with a hint when TELEGRAPH_HOME has not been initialised.
func mustHome(dir string) {
	if _, err := os.Stat(keyPath(dir)); err != nil {
		die(fmt.Errorf("%s is not initialised: run `telegraph init` first", dir))
	}
}

func mustKey(dir string) ed25519.PrivateKey {
	mustHome(dir)
	k, err := loadKey(dir)
	dieOn(err)
	return k
}

func pubOf(priv ed25519.PrivateKey) []byte { return []byte(priv.Public().(ed25519.PublicKey)) }

func mustStore(dir string) *store.Store {
	mustHome(dir)
	st, err := store.Open(dataPath(dir))
	dieOn(err)
	return st
}

func short(h string) string { return ui.Short(h, 8) }

func humanDur(secs int64) string {
	if secs < 0 {
		return "?"
	}
	d, h, m := secs/86400, (secs%86400)/3600, (secs%3600)/60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd%dh", d, h)
	case h > 0:
		return fmt.Sprintf("%dh%dm", h, m)
	default:
		return fmt.Sprintf("%dm", m)
	}
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func die(err error) {
	errOut.Fail("telegraph: %v", err)
	os.Exit(1)
}

func dieOn(err error) {
	if err != nil {
		die(err)
	}
}
