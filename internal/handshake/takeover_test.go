package handshake

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/ao3575911/telegraph/internal/tlog"
)

// Issue #15: ownership is first-come; only the owner can re-register,
// revoke or lock; a revoked handle is free again; swaps still transfer.

func plainEntry(t *testing.T, a act, kind tlog.Kind, handle, did string, ts int64) tlog.Entry {
	t.Helper()
	e, err := tlog.New(tlog.Payload{Kind: kind, Handle: handle, DID: did, TS: ts,
		Nonce: []byte(did + string(rune(ts)))}, a.priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	return *e
}

func TestLaterRegisterCannotTakeOver(t *testing.T) {
	A, B := newAct(t), newAct(t)
	es := []tlog.Entry{
		plainEntry(t, A, tlog.KindRegister, "x@nym", "did:a", 100),
		plainEntry(t, B, tlog.KindRegister, "x@nym", "did:b", 200),
	}
	if p, _, _ := Owner("x@nym", es); !bytes.Equal(p, A.pub) {
		t.Fatal("a later register from another key took the handle")
	}
	if Free("x@nym", es) {
		t.Fatal("owned handle reported free")
	}
}

func TestStrangerRevokeAndLockIgnored(t *testing.T) {
	A, B := newAct(t), newAct(t)
	_, hash := NewSecret()
	es := []tlog.Entry{
		plainEntry(t, A, tlog.KindRegister, "x@nym", "did:a", 100),
		plainEntry(t, B, tlog.KindRevoke, "x@nym", "", 200),
		lock(t, "x@nym", B.pub, hash, 9000, 300, B),
		plainEntry(t, B, tlog.KindRegister, "x@nym", "did:b", 400),
	}
	p, st, _ := Owner("x@nym", es)
	if !bytes.Equal(p, A.pub) || st != StateRegistered || Free("x@nym", es) {
		t.Fatalf("stranger revoke/lock changed ownership: state %v", st)
	}
}

func TestOwnerUpdateAllowed(t *testing.T) {
	A, B := newAct(t), newAct(t)
	es := []tlog.Entry{
		plainEntry(t, A, tlog.KindRegister, "x@nym", "did:a1", 100),
		plainEntry(t, B, tlog.KindRegister, "x@nym", "did:b", 150),
		plainEntry(t, A, tlog.KindRegister, "x@nym", "did:a2", 200),
	}
	p, _, since := Owner("x@nym", es)
	if !bytes.Equal(p, A.pub) || since != 200 {
		t.Fatalf("owner update lost: since %d", since)
	}
	if _, acq := Tenure("x@nym", es); acq != 100 {
		t.Fatalf("tenure began %d, want 100", acq)
	}
}

func TestRevokeThenReclaim(t *testing.T) {
	A, B, C := newAct(t), newAct(t), newAct(t)
	es := []tlog.Entry{
		plainEntry(t, A, tlog.KindRegister, "x@nym", "did:a", 100),
		plainEntry(t, A, tlog.KindRevoke, "x@nym", "", 200),
	}
	if !Free("x@nym", es) {
		t.Fatal("revoked handle not free")
	}
	if p, _ := Tenure("x@nym", es); p != nil {
		t.Fatal("revoked handle has a tenure")
	}
	es = append(es,
		plainEntry(t, B, tlog.KindRegister, "x@nym", "did:b", 300),
		plainEntry(t, C, tlog.KindRegister, "x@nym", "did:c", 400),
		plainEntry(t, A, tlog.KindRegister, "x@nym", "did:a", 500),
	)
	if p, _, _ := Owner("x@nym", es); !bytes.Equal(p, B.pub) {
		t.Fatal("first register after revoke should own the handle")
	}
}

func TestSwapTransfersThenNewOwnerRules(t *testing.T) {
	A, B, C := newAct(t), newAct(t), newAct(t)
	pre, hash := NewSecret()
	es := []tlog.Entry{
		plainEntry(t, A, tlog.KindRegister, "x@nym", "did:a", 100),
		lock(t, "x@nym", B.pub, hash, 5000, 200, A),
		claim(t, "x@nym", pre, 300, B),
		plainEntry(t, A, tlog.KindRegister, "x@nym", "did:a-again", 400), // old owner: void
		plainEntry(t, B, tlog.KindRegister, "x@nym", "did:b", 500),       // new owner: counts
		plainEntry(t, C, tlog.KindRegister, "x@nym", "did:c", 600),       // stranger: void
	}
	p, st, since := Owner("x@nym", es)
	if !bytes.Equal(p, B.pub) || st != StateOwned || since != 500 {
		t.Fatalf("after swap: owner B=%v state %v since %d", bytes.Equal(p, B.pub), st, since)
	}
	if _, acq := Tenure("x@nym", es); acq != 300 {
		t.Fatalf("tenure began %d, want the claim at 300", acq)
	}
	es = append(es, plainEntry(t, B, tlog.KindRevoke, "x@nym", "", 700),
		plainEntry(t, C, tlog.KindRegister, "x@nym", "did:c2", 800))
	if p, _, _ := Owner("x@nym", es); !bytes.Equal(p, C.pub) {
		t.Fatal("handle revoked by the swap's new owner should be free to claim")
	}
}

// TestOwnershipOrderIndependent: every permutation of the entry set folds
// to the same owner, status and since.
func TestOwnershipOrderIndependent(t *testing.T) {
	A, B, C := newAct(t), newAct(t), newAct(t)
	pre, hash := NewSecret()
	base := []tlog.Entry{
		plainEntry(t, A, tlog.KindRegister, "x@nym", "did:a", 100),
		plainEntry(t, B, tlog.KindRegister, "x@nym", "did:b", 100),
		plainEntry(t, C, tlog.KindRevoke, "x@nym", "", 150),
		plainEntry(t, B, tlog.KindRegister, "x@nym", "did:b2", 200),
		lock(t, "x@nym", C.pub, hash, 5000, 250, A),
		lock(t, "x@nym", C.pub, hash, 5000, 250, B),
		claim(t, "x@nym", pre, 300, C),
	}
	wantP, wantS, wantT := Owner("x@nym", base)
	if wantP == nil {
		t.Fatal("no owner")
	}
	rng := rand.New(rand.NewSource(15))
	for i := 0; i < 300; i++ {
		es := append([]tlog.Entry(nil), base...)
		rng.Shuffle(len(es), func(i, j int) { es[i], es[j] = es[j], es[i] })
		p, s, ts := Owner("x@nym", es)
		if !bytes.Equal(p, wantP) || s != wantS || ts != wantT {
			t.Fatalf("permutation %d folded differently", i)
		}
	}
}
