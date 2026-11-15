package handshake

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/ao3575911/telegraph/internal/tlog"
)

// chained signs p on top of prev (nil for a fresh chain).
func chained(t *testing.T, a act, p tlog.Payload, prev *tlog.Entry) *tlog.Entry {
	t.Helper()
	var ph []byte
	if prev != nil {
		ph = prev.Hash
	}
	e, err := tlog.New(p, a.priv, ph)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// sameSecondSwap builds a whole swap whose every entry shares one
// timestamp, the shape CI hit in issue #17: each side registers, alice
// offers and locks, bob locks, both claim.
func sameSecondSwap(t *testing.T) (A, B act, es []tlog.Entry, lockBeforeReg bool) {
	const ts, exp = 1794718847, 1794718847 + 3600
	A, B = newAct(t), newAct(t)
	pre, hash := NewSecret()
	terms := Terms{"alice@nym", "bob@nym", A.pub, B.pub, exp}
	sid := terms.SwapID()
	ra := chained(t, A, tlog.Payload{Kind: tlog.KindRegister, Handle: "alice@nym", DID: "did:a", TS: ts, Nonce: []byte{1}}, nil)
	of := chained(t, A, tlog.Payload{Kind: tlog.KindOffer, Handle: "alice@nym", DID: "bob@nym", SwapID: sid, Hash: hash, Expiry: exp, TS: ts, Nonce: []byte{2}}, ra)
	la := chained(t, A, tlog.Payload{Kind: tlog.KindLock, Handle: "alice@nym", SwapID: sid, Peer: B.pub, Hash: hash, Expiry: exp + ClaimWindow, TS: ts, Nonce: []byte{3}}, of)
	rb := chained(t, B, tlog.Payload{Kind: tlog.KindRegister, Handle: "bob@nym", DID: "did:b", TS: ts, Nonce: []byte{4}}, nil)
	lb := chained(t, B, tlog.Payload{Kind: tlog.KindLock, Handle: "bob@nym", SwapID: sid, Peer: A.pub, Hash: hash, Expiry: exp, TS: ts, Nonce: []byte{5}}, rb)
	ca := chained(t, A, tlog.Payload{Kind: tlog.KindClaim, Handle: "bob@nym", SwapID: sid, Pre: pre, TS: ts, Nonce: []byte{6}}, la)
	cb := chained(t, B, tlog.Payload{Kind: tlog.KindClaim, Handle: "alice@nym", SwapID: sid, Pre: pre, TS: ts, Nonce: []byte{7}}, lb)
	es = []tlog.Entry{*ra, *of, *la, *rb, *lb, *ca, *cb}
	return A, B, es, bytes.Compare(la.Hash, ra.Hash) < 0 || bytes.Compare(lb.Hash, rb.Hash) < 0
}

// TestSameSecondLockHolds is the #17 regression: with register, offer and
// lock in one second, the lock must count no matter how the hashes fall
// or in what order a node received the entries.
func TestSameSecondLockHolds(t *testing.T) {
	hashInverted := 0
	for seed := int64(0); seed < 200; seed++ {
		A, B, es, inv := sameSecondSwap(t)
		if inv {
			hashInverted++
		}
		locked := es[:5] // registers, offer and both locks; no claims yet
		rng := rand.New(rand.NewSource(seed))
		for perm := 0; perm < 20; perm++ {
			in := append([]tlog.Entry(nil), locked...)
			rng.Shuffle(len(in), func(i, j int) { in[i], in[j] = in[j], in[i] })
			if p, s, _ := Owner("alice@nym", in); s != StateLocked || !bytes.Equal(p, A.pub) {
				t.Fatalf("seed %d perm %d: alice@nym %s, want LOCKED by alice", seed, perm, s)
			}
			if p, s, _ := Owner("bob@nym", in); s != StateLocked || !bytes.Equal(p, B.pub) {
				t.Fatalf("seed %d perm %d: bob@nym %s, want LOCKED by bob", seed, perm, s)
			}
			all := append([]tlog.Entry(nil), es...)
			rng.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
			if p, s, _ := Owner("alice@nym", all); s != StateOwned || !bytes.Equal(p, B.pub) {
				t.Fatalf("seed %d perm %d: alice@nym %s, want SWAPPED to bob", seed, perm, s)
			}
			if p, s, _ := Owner("bob@nym", all); s != StateOwned || !bytes.Equal(p, A.pub) {
				t.Fatalf("seed %d perm %d: bob@nym %s, want SWAPPED to alice", seed, perm, s)
			}
		}
	}
	// (TS, hash) order alone puts a lock before its register in about
	// 3 of 4 runs here; make sure the test really covered that case.
	if hashInverted < 50 {
		t.Fatalf("only %d of 200 runs had a lock hash-sorted before its register", hashInverted)
	}
}

// TestSameSecondLWW: a revoke chained after a register in the same second
// is the newer entry, whatever the hashes.
func TestSameSecondLWW(t *testing.T) {
	for seed := int64(0); seed < 200; seed++ {
		A := newAct(t)
		r := chained(t, A, tlog.Payload{Kind: tlog.KindRegister, Handle: "x@nym", DID: "did:x", TS: 500, Nonce: []byte{1}}, nil)
		r2 := chained(t, A, tlog.Payload{Kind: tlog.KindRegister, Handle: "x@nym", DID: "did:y", TS: 500, Nonce: []byte{2}}, r)
		v := chained(t, A, tlog.Payload{Kind: tlog.KindRevoke, Handle: "x@nym", TS: 500, Nonce: []byte{3}}, r2)
		in := []tlog.Entry{*r, *r2, *v}
		rand.New(rand.NewSource(seed)).Shuffle(3, func(i, j int) { in[i], in[j] = in[j], in[i] })
		rel := []*tlog.Entry{&in[0], &in[1], &in[2]}
		tlog.SortCausal(rel, in)
		if !bytes.Equal(rel[2].Hash, v.Hash) || !bytes.Equal(rel[0].Hash, r.Hash) {
			t.Fatalf("seed %d: chain order lost", seed)
		}
	}
}
