package store

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/ao3575911/telegraph/internal/tlog"
)

func key(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func mem() *Store {
	return &Store{Tips: map[string]string{}, Seen: map[string]bool{}, Peers: map[string]*Peer{}}
}

func TestLocalEntriesStayPendingUntilPublished(t *testing.T) {
	_, priv := key(t)
	st := mem()
	e, _ := tlog.New(tlog.Payload{Kind: tlog.KindRegister, Handle: "a@nym", DID: "d",
		TS: time.Now().Unix(), Nonce: []byte{1}}, priv, nil)
	if !st.AppendEntry(e) || len(st.Pending()) != 1 {
		t.Fatal("own entry must be admitted and pending")
	}
	if st.AppendEntry(e) {
		t.Fatal("duplicate admitted")
	}
	st.MarkSeen(e.HashHex())
	if len(st.Pending()) != 0 {
		t.Fatal("published entry still pending")
	}
}

func TestPendingCheckpointCollectsCosigs(t *testing.T) {
	pa, a := key(t)
	pb, b := key(t)
	cp, err := tlog.New(tlog.Payload{Kind: tlog.KindCheckpoint, TS: time.Now().Unix(),
		Root: "00", Count: 2, Comm: [][]byte{pa, pb}, Nonce: []byte{2}}, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	st := mem()
	if !st.AppendEntry(cp) {
		t.Fatal("half-signed checkpoint must be kept for co-signing")
	}
	if !tlog.IsPendingQuorum(st.Entries[0].Verify()) {
		t.Fatal("expected pending quorum")
	}
	cs, _ := tlog.CoSign(cp, b)
	signed := *cp
	signed.CoSigs = []tlog.Cosig{cs}
	if !st.AppendReceived(&signed) {
		t.Fatal("co-signature not merged")
	}
	if err := st.Entries[0].Verify(); err != nil {
		t.Fatalf("checkpoint not quorate after merge: %v", err)
	}
	_, m := key(t)
	bogus, _ := tlog.CoSign(cp, m) // not on the committee
	signed.CoSigs = []tlog.Cosig{bogus}
	if st.AppendReceived(&signed) {
		t.Fatal("non-member co-signature merged")
	}
}
