package crdt

import (
	"crypto/ed25519"
	"crypto/rand"
	"reflect"
	"testing"
	"time"

	"github.com/ao3575911/telegraph/internal/tlog"
)

func mk(t *testing.T, handle, did string, ts int64) tlog.Entry {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	e, err := tlog.New(tlog.Payload{
		Kind: tlog.KindRegister, Handle: handle, DID: did,
		TS: ts, Nonce: []byte(handle + did),
	}, priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	return *e
}

func TestMergeConvergence(t *testing.T) {
	entries := []tlog.Entry{
		mk(t, "alice@nym", "did:A", 1000),
		mk(t, "alice@nym", "did:B", 2000),
		mk(t, "bob@nym", "did:C", 1500),
		mk(t, "alice@nym", "did:D", 2000),
		mk(t, "carol@nym", "did:E", 900),
	}
	orders := [][]int{{0, 1, 2, 3, 4}, {4, 3, 2, 1, 0}, {2, 0, 4, 3, 1}, {3, 1, 0, 2, 4}}
	var states []*Registry
	for _, order := range orders {
		r := New()
		for _, i := range order {
			r.Apply(entries[i])
		}
		states = append(states, r)
	}
	for i := 1; i < len(states); i++ {
		a, b := states[0], states[i]
		m1, m2 := Merge(a, b), Merge(b, a)
		if !reflect.DeepEqual(m1.All, m2.All) {
			t.Fatalf("order %d: merge not commutative over All", i)
		}
		if !reflect.DeepEqual(m1.Head, m2.Head) {
			t.Fatalf("order %d: merge not commutative over Head", i)
		}
	}
}

func TestDeterministicTiebreak(t *testing.T) {
	a := mk(t, "x@nym", "did:1", 5000)
	b := mk(t, "x@nym", "did:2", 5000)
	r1, r2 := New(), New()
	r1.Apply(a)
	r2.Apply(b)
	m1, m2 := Merge(r1, r2), Merge(r2, r1)
	if !reflect.DeepEqual(m1.Head, m2.Head) {
		t.Fatal("merge not commutative")
	}
	_, s1, _ := m1.Resolve("x@nym")
	_, s2, _ := m2.Resolve("x@nym")
	if s1 != s2 {
		t.Fatal("winner differs between merge orders")
	}
}

var _ = time.Now

// TestMergeKeepsFirstOwner (#15): whatever order replicas merge in, the
// first register keeps the handle against a later key.
func TestMergeKeepsFirstOwner(t *testing.T) {
	first := mk(t, "x@nym", "did:first", 1000)
	later := mk(t, "x@nym", "did:later", 2000)
	r1, r2 := New(), New()
	r1.Apply(later)
	r2.Apply(first)
	for _, m := range []*Registry{Merge(r1, r2), Merge(r2, r1), Merge(r1, r2, r1)} {
		e, _, ok := m.Resolve("x@nym")
		if !ok || e.Payload.DID != "did:first" {
			t.Fatalf("resolved %q, want did:first", e.Payload.DID)
		}
	}
}
