package tlog

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
)

func TestCommitteeAllowlist(t *testing.T) {
	t.Cleanup(func() { SetCommittee(nil) })
	key := func() (ed25519.PublicKey, ed25519.PrivateKey) {
		pub, priv, _ := ed25519.GenerateKey(rand.Reader)
		return pub, priv
	}
	pa, a := key()
	pb, b := key()
	px, _ := key()
	cp := func(comm ...[]byte) *Entry {
		e, err := New(Payload{Kind: KindCheckpoint, TS: 10, Comm: comm, Nonce: []byte{byte(len(comm))}}, a, nil)
		if err != nil {
			t.Fatal(err)
		}
		cs, _ := CoSign(e, b)
		e.CoSigs = []Cosig{cs}
		return e
	}
	ok := cp(pa, pb)
	if ok.Verify() != nil {
		t.Fatal("without an allowlist any committee verifies")
	}
	SetCommittee([][]byte{pa, pb})
	if err := ok.Verify(); err != nil {
		t.Fatalf("allowlisted committee: %v", err)
	}
	SetCommittee([][]byte{pa, px})
	if err := ok.Verify(); !errors.Is(err, ErrOffCommittee) || !Unattested(err) {
		t.Fatalf("off-allowlist committee: %v", err)
	}
	if err := cp().Verify(); !errors.Is(err, ErrOffCommittee) {
		t.Fatalf("empty committee under an allowlist: %v", err)
	}
	SetCommittee(nil)
	if ok.Verify() != nil {
		t.Fatal("clearing the allowlist must clear cached verdicts too")
	}
}
