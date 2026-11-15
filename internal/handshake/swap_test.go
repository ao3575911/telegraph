package handshake

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"

	"github.com/ao3575911/telegraph/internal/tlog"
)

type act struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func newAct(t *testing.T) act {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return act{pub, priv}
}

func reg(t *testing.T, a act, handle string, ts int64) tlog.Entry {
	t.Helper()
	e, err := tlog.New(tlog.Payload{Kind: tlog.KindRegister, Handle: handle,
		DID: "did:" + handle, TS: ts, Nonce: []byte(handle)}, a.priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	return *e
}

func lock(t *testing.T, handle string, peer []byte, hash string, exp, ts int64, signer act) tlog.Entry {
	t.Helper()
	e, err := tlog.New(tlog.Payload{Kind: tlog.KindLock, Handle: handle,
		Peer: peer, Hash: hash, Expiry: exp, TS: ts, SwapID: "s1", Nonce: []byte{1},
	}, signer.priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	return *e
}

func claim(t *testing.T, handle string, pre []byte, ts int64, signer act) tlog.Entry {
	t.Helper()
	return claimSID(t, "s1", handle, pre, ts, signer)
}

func claimSID(t *testing.T, sid, handle string, pre []byte, ts int64, signer act) tlog.Entry {
	t.Helper()
	e, err := tlog.New(tlog.Payload{Kind: tlog.KindClaim, Handle: handle,
		SwapID: sid, Pre: pre, TS: ts, Nonce: []byte{2}}, signer.priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	return *e
}

func TestHappyPathSwap(t *testing.T) {
	A, B := newAct(t), newAct(t)
	pre, hash := NewSecret()
	sid := Terms{"alice@nym", "bob@nym", A.pub, B.pub, 5000}.SwapID()

	give, err := tlog.New(tlog.Payload{Kind: tlog.KindLock, Handle: "alice@nym",
		SwapID: sid, Peer: B.pub, Hash: hash, Expiry: 5000 + ClaimWindow, TS: 4000,
		Nonce: []byte{3}}, A.priv, nil)
	dieOnS(t, err)
	take, err := tlog.New(tlog.Payload{Kind: tlog.KindLock, Handle: "bob@nym",
		SwapID: sid, Peer: A.pub, Hash: hash, Expiry: 5000, TS: 4000, Nonce: []byte{4}},
		B.priv, nil)
	dieOnS(t, err)
	dieOnS(t, ValidateStagger(give, take))

	entries := []tlog.Entry{reg(t, A, "alice@nym", 1000), reg(t, B, "bob@nym", 1100),
		*give, *take}
	// A claims bob first (revealing S), then B claims alice
	entries = append(entries, claimSID(t, sid, "bob@nym", pre, 4100, A))
	entries = append(entries, claimSID(t, sid, "alice@nym", pre, 4200, B))

	pa, sa, _ := Owner("alice@nym", entries)
	pb, sb, _ := Owner("bob@nym", entries)
	if !bytes.Equal(pa, B.pub) || !bytes.Equal(pb, A.pub) || sa != StateOwned || sb != StateOwned {
		t.Fatal("swap did not complete")
	}
}

func TestAbandonedSwapReverts(t *testing.T) {
	A, B := newAct(t), newAct(t)
	pre, hash := NewSecret()
	entries := []tlog.Entry{
		reg(t, A, "alice@nym", 1000),
		lock(t, "alice@nym", B.pub, hash, 5000, 4000, A),
		reg(t, B, "bob@nym", 1100),
		claim(t, "alice@nym", pre, 5010, B), // B claims LATE -> invalid
	}
	// A reclaims post-expiry
	rc, err := tlog.New(tlog.Payload{Kind: tlog.KindReclaim, Handle: "alice@nym",
		SwapID: "s1", TS: 5100, Nonce: []byte{5}}, A.priv, nil)
	dieOnS(t, err)
	entries = append(entries, *rc)
	pa, sa, _ := Owner("alice@nym", entries)
	if !bytes.Equal(pa, A.pub) || sa != StateHeld {
		t.Fatalf("revert failed: state=%d", sa)
	}
}

// BUG #2: mid-lock re-key by old owner
func TestMidLockReKeyAttack(t *testing.T) {
	A, B := newAct(t), newAct(t)
	pre, hash := NewSecret()
	entries := []tlog.Entry{
		reg(t, A, "alice@nym", 1000),
		reg(t, B, "bob@nym", 1100),
		lock(t, "alice@nym", B.pub, hash, 5000+ClaimWindow, 4000, A),
		lock(t, "bob@nym", A.pub, hash, 5000, 4000, B),
		claim(t, "bob@nym", pre, 4100, A),
	}
	Rogue := newAct(t)
	entries = append(entries, reg(t, Rogue, "alice@nym", 4990)) // re-key inside window
	pa, _, _ := Owner("alice@nym", entries)
	if bytes.Equal(pa, Rogue.pub) {
		t.Fatal("BUG: mid-lock re-key stole the handle")
	}
	// B can still claim with the revealed preimage
	entries = append(entries, claim(t, "alice@nym", pre, 4200, B))
	pa2, st2, _ := Owner("alice@nym", entries)
	if !bytes.Equal(pa2, B.pub) || st2 != StateOwned {
		t.Fatal("re-key attack broke the swap")
	}
}

// BUG #1: equal-expiry late-claim race — ValidateStagger must reject
func TestStaggerValidation(t *testing.T) {
	A, B := newAct(t), newAct(t)
	_, hash := NewSecret()
	give := lock(t, "alice@nym", B.pub, hash, 5000, 4000, A) // NO +Δ
	take := lock(t, "bob@nym", A.pub, hash, 5000, 4000, B)
	if err := ValidateStagger(&give, &take); err == nil {
		t.Fatal("equal-expiry locks accepted — late-claim race is live")
	}
}

// preimage reuse across swaps
func TestPreimageReuse(t *testing.T) {
	A, B := newAct(t), newAct(t)
	pre, hash := NewSecret()
	// swap 1 completes, revealing S publicly
	entries := []tlog.Entry{
		reg(t, A, "alice@nym", 1000), reg(t, B, "bob@nym", 1100),
		lock(t, "alice@nym", B.pub, hash, 5000+ClaimWindow, 4000, A),
		claim(t, "alice@nym", pre, 4200, B),
	}
	// attacker noticed S, tries to claim in a second swap reusing it
	// (blocked because S is per-swap in the tool — but the fold also
	// only honours claims whose sid matches a lock naming them)
	rogueClaim := claim(t, "alice@nym", pre, 4300, B)
	rogueClaim.Payload.SwapID = "s2" // fabricated sid
	if ValidClaim(&entries[2], &rogueClaim) == nil {
		t.Fatal("cross-swap preimage replay accepted")
	}
	_ = A
}

func TestCardRoundtrip(t *testing.T) {
	A := newAct(t)
	re := reg(t, A, "alice@nym", 1000)
	line := CardLine("alice@nym", &re)
	h, hex8, ok := ParseCardLine(line)
	if !ok || h != "alice@nym" || hex8 != re.HashHex()[:8] {
		t.Fatal("card parse roundtrip failed")
	}
	card, err := BuildCard(line, 2000, A.priv, nil)
	dieOnS(t, err)
	entries := []tlog.Entry{re, *card}
	if !VerifyCard(line, "alice@nym", entries) {
		t.Fatal("own card failed verification")
	}
	fake := "⟁ alice@nym · deadbeef"
	if VerifyCard(fake, "alice@nym", entries) {
		t.Fatal("forged card line verified")
	}
}

func dieOnS(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

var _, _ = time.Now, hex.EncodeToString

// A lock signed by someone who does not own the handle must not freeze
// or transfer it.
func TestForeignLockIgnored(t *testing.T) {
	A, B, M := newAct(t), newAct(t), newAct(t)
	pre, hash := NewSecret()
	entries := []tlog.Entry{
		reg(t, A, "alice@nym", 1000),
		lock(t, "alice@nym", M.pub, hash, 9000, 4000, M), // M never owned alice
		claim(t, "alice@nym", pre, 4100, M),
	}
	pa, sa, _ := Owner("alice@nym", entries)
	if !bytes.Equal(pa, A.pub) || sa != StateRegistered {
		t.Fatalf("foreign lock changed ownership: state=%s", sa)
	}
	_ = B
}
