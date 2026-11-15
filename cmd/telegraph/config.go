package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/ao3575911/telegraph/internal/checkpoint"
	"github.com/ao3575911/telegraph/internal/merkle"
	"github.com/ao3575911/telegraph/internal/transport"
)

// Node configuration comes from the environment (an EnvironmentFile under
// systemd), so one machine can run several nodes, each with its own home
// (identity key and log), token and peers. See docs/NODE-OPERATIONS.md.
//
//	TELEGRAPH_HOME         node directory: identity.key, data.json
//	TELEGRAPH_BOTS         bot token(s), comma-separated
//	TELEGRAPH_BOTS_FILE    file holding the token(s) instead (mode 0600)
//	TELEGRAPH_TOKEN_ENV    name of the variable holding the token(s), for
//	                       nodes sharing one environment (default TELEGRAPH_BOTS)
//	TELEGRAPH_CHANNELS     gossip channel id(s) or @username(s)
//	TELEGRAPH_CHANNEL_URL  public channel link for bot buttons
//	TELEGRAPH_PEERS        hex public keys of the other committee nodes
//	TELEGRAPH_NODE_NAME    label for logs
//	TELEGRAPH_BOT_REGISTER 1: let Telegram users /register (custodial)
//	TELEGRAPH_LOG_FORMAT   text (default) or json
//	TELEGRAPH_LOG_LEVEL    debug, info (default), warn, error

// botTokens returns the configured bot tokens and where they came from.
// The token is never logged or printed; callers report only the source.
func botTokens() ([]string, string, error) {
	if f := os.Getenv("TELEGRAPH_BOTS_FILE"); f != "" {
		fi, err := os.Stat(f)
		if err != nil {
			return nil, "", fmt.Errorf("TELEGRAPH_BOTS_FILE: %w", err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			return nil, "", fmt.Errorf("TELEGRAPH_BOTS_FILE %s is readable by others: chmod 600 it", f)
		}
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, "", fmt.Errorf("TELEGRAPH_BOTS_FILE: %w", err)
		}
		return splitCSV(strings.TrimSpace(string(b))), "file " + f, nil
	}
	name := envOr("TELEGRAPH_TOKEN_ENV", "TELEGRAPH_BOTS")
	return splitCSV(os.Getenv(name)), "env " + name, nil
}

// channelURL validates the public channel link shown on bot buttons.
func channelURL() (string, error) {
	u := envOr("TELEGRAPH_CHANNEL_URL", transport.DefaultChannelURL)
	rest, ok := strings.CutPrefix(u, "https://t.me/")
	if !ok || rest == "" || strings.HasPrefix(rest, "+") || strings.HasPrefix(rest, "joinchat") || strings.ContainsAny(rest, "/?#") {
		return "", fmt.Errorf("TELEGRAPH_CHANNEL_URL %q: want a public https://t.me/<username> link, not an invite link", u)
	}
	return u, nil
}

// peerKeys parses TELEGRAPH_PEERS: hex ed25519 public keys.
func peerKeys() ([][]byte, error) {
	var out [][]byte
	for _, p := range splitCSV(os.Getenv("TELEGRAPH_PEERS")) {
		b, err := hex.DecodeString(p)
		if err != nil || len(b) != 32 {
			return nil, fmt.Errorf("TELEGRAPH_PEERS: %q is not a 64-hex public key", short(p))
		}
		out = append(out, b)
	}
	return out, nil
}

// setupLogging installs a slog handler as the default logger, so the
// standard log package (used across internal/) emits structured records.
func setupLogging() {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(envOr("TELEGRAPH_LOG_LEVEL", "info"))); err != nil {
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	if strings.EqualFold(os.Getenv("TELEGRAPH_LOG_FORMAT"), "json") {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	l := slog.New(h)
	if name := os.Getenv("TELEGRAPH_NODE_NAME"); name != "" {
		l = l.With("node", name)
	}
	slog.SetDefault(l)
}

// doConfig prints the effective node configuration without secrets;
// `config key` prints only this node's public key (for TELEGRAPH_PEERS).
func doConfig(dir string, args []string) {
	if len(args) == 1 && args[0] == "key" {
		out.Println(fmt.Sprintf("%x", pubOf(mustKey(dir))))
		return
	}
	tokens, src, err := botTokens()
	tok := fmt.Sprintf("%d from %s", len(tokens), src)
	if err != nil {
		tok = "error: " + err.Error()
	}
	cu, err := channelURL()
	if err != nil {
		cu = "error: " + err.Error()
	}
	peers, err := peerKeys()
	ps := fmt.Sprintf("%d", len(peers))
	if err != nil {
		ps = "error: " + err.Error()
	}
	key := "not initialised (run telegraph init)"
	if _, err := os.Stat(keyPath(dir)); err == nil {
		k, err := loadKey(dir)
		dieOn(err)
		key = fmt.Sprintf("%x", pubOf(k))
	}
	out.KV([][2]string{
		{"version", Version},
		{"home", dir},
		{"node key", key},
		{"node name", envOr("TELEGRAPH_NODE_NAME", "-")},
		{"bot tokens", tok},
		{"channels", envOr("TELEGRAPH_CHANNELS", "-")},
		{"channel url", cu},
		{"peers", ps},
		{"bot register", fmt.Sprint(os.Getenv("TELEGRAPH_BOT_REGISTER") == "1")},
		{"log", envOr("TELEGRAPH_LOG_FORMAT", "text") + "/" + envOr("TELEGRAPH_LOG_LEVEL", "info")},
	})
}

// doCommittee shows who signs the next checkpoint and whether this node
// and its configured peers are on that committee.
func doCommittee(dir string) {
	priv := mustKey(dir)
	st := mustStore(dir)
	me := pubOf(priv)
	peers, err := peerKeys()
	dieOn(err)
	heads := checkpoint.ChainHeads(st.Entries)
	log, _ := checkpoint.CoverHeads(st.Entries, heads)
	issuers := checkpoint.Issuers(log)
	isIssuer := false
	for _, p := range issuers {
		isIssuer = isIssuer || string(p) == string(me)
	}
	out.KV([][2]string{
		{"this node", fmt.Sprintf("%x", me)},
		{"issuers", fmt.Sprint(len(issuers))},
		{"entries", fmt.Sprint(len(log))},
	})
	if len(log) == 0 {
		out.Info("empty log: no committee yet")
		return
	}
	comm := checkpoint.SelectCommittee(checkpoint.Target(merkle.Root(checkpoint.Hashes(log)), int64(len(log))), issuers)
	now := time.Now().Unix()
	rows := [][]string{}
	for i, c := range comm {
		role := "leader"
		if i > 0 {
			role = fmt.Sprintf("fallback %d", i)
		}
		who := ""
		switch {
		case string(c) == string(me):
			who = "this node"
		case containsKey(peers, c):
			who = "peer"
		}
		rows = append(rows, []string{role, short(hex.EncodeToString(c)), who,
			checkpoint.PeerStatus(st, hex.EncodeToString(c), now)})
	}
	out.Table("  ", rows)
	if !isIssuer {
		out.Warn("this node has signed no register yet, so it cannot be on a committee: " +
			"run `telegraph register --handle <name>@nym --did <did>` and sync")
	}
	for _, p := range peers {
		if !containsKey(issuers, p) {
			out.Warn("peer %s is not an issuer yet (no register seen from it)", short(hex.EncodeToString(p)))
		}
	}
}

func containsKey(ks [][]byte, k []byte) bool {
	for _, x := range ks {
		if string(x) == string(k) {
			return true
		}
	}
	return false
}

var errNoTokens = errors.New("no bot token: set TELEGRAPH_BOTS (or TELEGRAPH_BOTS_FILE / TELEGRAPH_TOKEN_ENV), or --bus for a local bus")
