package store

import (
	"errors"
	"testing"

	"github.com/ao3575911/telegraph/internal/tlog"
)

// TestCheckTakeoverRefusesBackdated: a register from another key dated
// at or before the owner's tenure began would win the first-come fold,
// so the store refuses it; later ones are void and harmless.
func TestCheckTakeoverRefusesBackdated(t *testing.T) {
	_, a := key(t)
	_, b := key(t)
	st := mem()
	own, _ := tlog.New(tlog.Payload{Kind: tlog.KindRegister, Handle: "x@nym", DID: "did:a", TS: 1000, Nonce: []byte{1}}, a, nil)
	if err := st.CheckTakeover(own); err != nil || !st.AppendEntry(own) {
		t.Fatal("first register refused")
	}
	early, _ := tlog.New(tlog.Payload{Kind: tlog.KindRegister, Handle: "x@nym", DID: "did:b", TS: 999, Nonce: []byte{2}}, b, nil)
	if err := st.CheckTakeover(early); !errors.Is(err, ErrBackdatedTakeover) {
		t.Fatalf("backdated takeover: %v", err)
	}
	same, _ := tlog.New(tlog.Payload{Kind: tlog.KindRegister, Handle: "x@nym", DID: "did:b", TS: 1000, Nonce: []byte{3}}, b, nil)
	if err := st.CheckTakeover(same); !errors.Is(err, ErrBackdatedTakeover) {
		t.Fatalf("same-second takeover: %v", err)
	}
	late, _ := tlog.New(tlog.Payload{Kind: tlog.KindRegister, Handle: "x@nym", DID: "did:b", TS: 2000, Nonce: []byte{4}}, b, nil)
	if err := st.CheckTakeover(late); err != nil {
		t.Fatalf("late (void) register: %v", err)
	}
	upd, _ := tlog.New(tlog.Payload{Kind: tlog.KindRegister, Handle: "x@nym", DID: "did:a2", TS: 500, Nonce: []byte{5}}, a, own.Hash)
	if err := st.CheckTakeover(upd); err != nil {
		t.Fatalf("owner's own entry: %v", err)
	}
	other, _ := tlog.New(tlog.Payload{Kind: tlog.KindRegister, Handle: "y@nym", DID: "did:b", TS: 1, Nonce: []byte{6}}, b, nil)
	if err := st.CheckTakeover(other); err != nil {
		t.Fatalf("free handle: %v", err)
	}
}
