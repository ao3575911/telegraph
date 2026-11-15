package transport

import (
	"strings"
	"testing"
)

func TestStatsAndEarlyAdopterBadge(t *testing.T) {
	r, st := regResponder(t), memStore()
	if got := send(t, r, st, 1, "/register first"); !strings.Contains(got, "Founding 100 · handle #1") {
		t.Fatalf("first registration lacks badge:\n%s", got)
	}
	got := send(t, r, st, 2, "/register second")
	if !strings.Contains(got, "handle #2") {
		t.Fatalf("second registration:\n%s", got)
	}
	if got := send(t, r, st, 2, "/register second did:key:z6MkNew"); strings.Contains(got, "#") {
		t.Fatalf("a DID update must not award a badge:\n%s", got)
	}
	send(t, r, st, 1, "/revoke first")
	got = send(t, r, st, 2, "/stats")
	for _, want := range []string{"Handles 1 active · 1 revoked · 2 ever", "Held by this bot for Telegram users 1",
		"(yours 1 of 3)", "Newest handle <b>second@nym</b>", "98 Founding 100 spots left"} {
		if !strings.Contains(got, want) {
			t.Errorf("/stats lacks %q:\n%s", want, got)
		}
	}
	for n, want := range map[int]string{0: "", 1: "🏅 Founding 100", 100: "🏅 Founding 100", 101: "🎖 Early adopter", 1000: "🎖 Early adopter", 1001: ""} {
		if Badge(n) != want {
			t.Errorf("Badge(%d) = %q, want %q", n, Badge(n), want)
		}
	}
}
