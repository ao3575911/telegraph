package transport

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/ao3575911/telegraph/internal/tlog"
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

// /stats counts follow the ownership fold: a later register from another
// key is void and a stranger's revoke is ignored, so neither turns an
// owned handle into "revoked".
func TestStatsUseOwnershipFold(t *testing.T) {
	st := memStore()
	_, a, _ := ed25519.GenerateKey(rand.Reader)
	_, b, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().Unix()
	add := func(priv ed25519.PrivateKey, kind tlog.Kind, h string, ts int64) {
		e, err := tlog.New(tlog.Payload{Kind: kind, Handle: h, DID: "did:x:1", TS: ts, Nonce: []byte{byte(ts)}}, priv, st.Tip(priv.Public().(ed25519.PublicKey)))
		dieOnT(t, err)
		st.AppendEntry(e)
	}
	add(a, tlog.KindRegister, "x@nym", now-50)
	add(b, tlog.KindRevoke, "x@nym", now-40) // stranger: ignored
	add(a, tlog.KindRegister, "y@nym", now-30)
	add(b, tlog.KindRegister, "y@nym", now-20) // later key: void
	add(a, tlog.KindRegister, "z@nym", now-10)
	add(a, tlog.KindRevoke, "z@nym", now-5) // owner revoke
	s := Stats(st, time.Now())
	if s.Active != 2 || s.Revoked != 1 || s.Ever != 3 {
		t.Fatalf("active %d revoked %d ever %d, want 2 1 3", s.Active, s.Revoked, s.Ever)
	}
}
