package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChannelURLRejectsInviteLinks(t *testing.T) {
	for u, ok := range map[string]bool{
		"":                               true, // default
		"https://t.me/tgraph_net":        true,
		"https://t.me/+KEEgOfyWFC1iZGE9": false,
		"https://t.me/joinchat/abc":      false,
		"http://t.me/tgraph_net":         false,
		"https://t.me/tgraph_net/12":     false,
		"https://example.com/x":          false,
	} {
		t.Setenv("TELEGRAPH_CHANNEL_URL", u)
		got, err := channelURL()
		if (err == nil) != ok {
			t.Errorf("%q: err %v", u, err)
		}
		if u == "" && got != "https://t.me/tgraph_net" {
			t.Errorf("default channel url %q", got)
		}
	}
}

func TestPeerKeys(t *testing.T) {
	t.Setenv("TELEGRAPH_PEERS", strings.Repeat("ab", 32)+", "+strings.Repeat("cd", 32))
	if ps, err := peerKeys(); err != nil || len(ps) != 2 {
		t.Fatalf("peers %d, %v", len(ps), err)
	}
	t.Setenv("TELEGRAPH_PEERS", "abcd")
	if _, err := peerKeys(); err == nil {
		t.Fatal("short key accepted")
	}
}

func TestBotTokensSources(t *testing.T) {
	t.Setenv("TELEGRAPH_BOTS_FILE", "")
	t.Setenv("TELEGRAPH_BOTS", "1:a")
	t.Setenv("NODE2_TOKEN", "2:b")
	if ts, src, _ := botTokens(); len(ts) != 1 || ts[0] != "1:a" || src != "env TELEGRAPH_BOTS" {
		t.Fatalf("default env: %v %s", ts, src)
	}
	t.Setenv("TELEGRAPH_TOKEN_ENV", "NODE2_TOKEN")
	if ts, _, _ := botTokens(); len(ts) != 1 || ts[0] != "2:b" {
		t.Fatalf("named env: %v", ts)
	}
	f := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(f, []byte("3:c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TELEGRAPH_BOTS_FILE", f)
	if _, _, err := botTokens(); err == nil {
		t.Fatal("world-readable token file accepted")
	}
	if err := os.Chmod(f, 0o600); err != nil {
		t.Fatal(err)
	}
	if ts, src, err := botTokens(); err != nil || len(ts) != 1 || ts[0] != "3:c" || strings.Contains(src, "3:c") {
		t.Fatalf("file: %v %s %v", ts, src, err)
	}
}
